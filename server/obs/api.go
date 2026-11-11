package obs

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// API (kontrol duzlemi) metrikleri (O17, A5 #6).
//
// Etiketler:
//   - route: http.ServeMux kalip dizgisi ("GET /api/v1/tunnels/{id}"), HAM YOL
//     DEGIL. Kalip kumesi kodda kayitli rotalarla sinirli oldugu icin kardinalite
//     sabittir; 2000 farkli /api/v1/tunnels/<id> tek seri uretir. Eslesmeyen
//     istekler "unmatched" kovasina duser.
//   - method: sabit kume (GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS/other).
//   - code_class: 1xx/2xx/3xx/4xx/429/5xx/other. 429 4xx'ten AYRI tutulur ki hiz
//     siniri alarmi istemci hatalariyla karismasin.
//
// Savunma: beklenmedik bir kalip kaynagi olursa diye seri sayisi apiMaxSeries ile
// sinirlidir; asim "overflow" kovasina gider.

const (
	apiRouteUnmatched = "unmatched"
	apiRouteOverflow  = "overflow"
	apiMaxSeries      = 1024
)

var apiMethodNames = [...]string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "other"}

const (
	apiClass1xx = iota
	apiClass2xx
	apiClass3xx
	apiClass4xx
	apiClass429
	apiClass5xx
	apiClassOther
	apiNumClasses
)

var apiClassNames = [apiNumClasses]string{"1xx", "2xx", "3xx", "4xx", "429", "5xx", "other"}

type apiRouteStat struct {
	codes [len(apiMethodNames)][apiNumClasses]atomic.Uint64
	lat   [len(latencyBuckets) + 1]atomic.Uint64
	sumNs atomic.Uint64
	count atomic.Uint64
}

var (
	apiMu     sync.RWMutex
	apiRoutes = map[string]*apiRouteStat{}
)

func apiMethodIndex(m string) int {
	for i, n := range apiMethodNames[:len(apiMethodNames)-1] {
		if n == m {
			return i
		}
	}
	return len(apiMethodNames) - 1
}

func apiClassIndex(status int) int {
	switch {
	case status == http.StatusTooManyRequests:
		return apiClass429
	case status >= 100 && status < 200:
		return apiClass1xx
	case status >= 200 && status < 300:
		return apiClass2xx
	case status >= 300 && status < 400:
		return apiClass3xx
	case status >= 400 && status < 500:
		return apiClass4xx
	case status >= 500 && status < 600:
		return apiClass5xx
	}
	return apiClassOther
}

func apiStatFor(route string) *apiRouteStat {
	if route == "" {
		route = apiRouteUnmatched
	}
	apiMu.RLock()
	st := apiRoutes[route]
	apiMu.RUnlock()
	if st != nil {
		return st
	}
	apiMu.Lock()
	defer apiMu.Unlock()
	if st = apiRoutes[route]; st != nil {
		return st
	}
	if len(apiRoutes) >= apiMaxSeries {
		route = apiRouteOverflow
		if st = apiRoutes[route]; st != nil {
			return st
		}
	}
	st = &apiRouteStat{}
	apiRoutes[route] = st
	return st
}

// ObserveAPI, tamamlanan bir API istegini sayar.
func ObserveAPI(route, method string, status int, d time.Duration) {
	st := apiStatFor(route)
	st.codes[apiMethodIndex(method)][apiClassIndex(status)].Add(1)
	sec := d.Seconds()
	i := 0
	for i < len(latencyBuckets) && sec > latencyBuckets[i] {
		i++
	}
	st.lat[i].Add(1)
	st.sumNs.Add(uint64(d.Nanoseconds()))
	st.count.Add(1)
}

// APIRequests, (rota, yontem, sinif) sayaci (test/teshis).
func APIRequests(route, method, class string) uint64 {
	apiMu.RLock()
	st := apiRoutes[route]
	apiMu.RUnlock()
	if st == nil {
		return 0
	}
	for c, n := range apiClassNames {
		if n == class {
			return st.codes[apiMethodIndex(method)][c].Load()
		}
	}
	return 0
}

func writeAPI(b *bytes.Buffer) {
	apiMu.RLock()
	names := make([]string, 0, len(apiRoutes))
	for k := range apiRoutes {
		names = append(names, k)
	}
	stats := make(map[string]*apiRouteStat, len(apiRoutes))
	for k, v := range apiRoutes {
		stats[k] = v
	}
	apiMu.RUnlock()
	sort.Strings(names)

	head(b, "zorven_api_requests_total", "Kontrol API isteklerinin rota sablonu, yontem ve durum sinifina gore sayisi.", "counter")
	for _, rt := range names {
		st := stats[rt]
		for m := range apiMethodNames {
			for c := range apiClassNames {
				if v := st.codes[m][c].Load(); v > 0 {
					fmt.Fprintf(b, "zorven_api_requests_total{route=%q,method=%q,code_class=%q} %d\n", rt, apiMethodNames[m], apiClassNames[c], v)
				}
			}
		}
	}
	head(b, "zorven_api_request_duration_seconds", "Kontrol API istek suresi (rota sablonuna gore).", "histogram")
	for _, rt := range names {
		st := stats[rt]
		var cum uint64
		for i, le := range latencyBuckets {
			cum += st.lat[i].Load()
			fmt.Fprintf(b, "zorven_api_request_duration_seconds_bucket{route=%q,le=%q} %d\n", rt, fmtF(le), cum)
		}
		cum += st.lat[len(latencyBuckets)].Load()
		fmt.Fprintf(b, "zorven_api_request_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\n", rt, cum)
		fmt.Fprintf(b, "zorven_api_request_duration_seconds_sum{route=%q} %s\n", rt, fmtF(float64(st.sumNs.Load())/1e9))
		fmt.Fprintf(b, "zorven_api_request_duration_seconds_count{route=%q} %d\n", rt, st.count.Load())
	}
}
