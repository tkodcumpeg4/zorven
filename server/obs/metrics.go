// Package obs, sunucunun gozlemlenebilirlik katmanidir: sade metin biciminde
// (Prometheus text exposition 0.0.4) metrikler, ayri dinleyici, /readyz ve pprof.
//
// Neden prometheus client eklenmedi: ihtiyac sabit bir metrik kumesi (sayac,
// gauge, tek histogram); client_golang buyuk bir bagimlilik agaci getirir ve
// kardinalite kontrolunu (etiketler) bizim yerimize gevsetir. Burada etiketler
// kodda SABIT kumelerdir; kiraci/alan adi etiketi ASLA yoktur.
//
// Sicak yol: yalniz atomic sayaclar; kilit yok, bellek ayirma yok.
package obs

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Durum sinifi indeksleri (1xx..5xx + diger).
const (
	classOther = iota
	class1xx
	class2xx
	class3xx
	class4xx
	class5xx
	numClasses
)

var classNames = [numClasses]string{"other", "1xx", "2xx", "3xx", "4xx", "5xx"}

// Red sinifi indeksleri. Sabit kume: kardinalite sinirli.
const (
	RejectWAF = iota
	RejectBot
	RejectRate
	RejectGeo
	RejectIP
	RejectPolicy
	RejectAuth
	RejectOther
	numRejects
)

var rejectNames = [numRejects]string{"waf", "bot", "rate_limit", "geo", "ip_filter", "policy", "auth", "other"}

// Gecikme kovalari (saniye).
var latencyBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	reqByClass  [numClasses]atomic.Uint64
	rejects     [numRejects]atomic.Uint64
	bytesIn     atomic.Uint64
	bytesOut    atomic.Uint64
	latCounts   [len(latencyBuckets) + 1]atomic.Uint64 // son: +Inf
	latSumNanos atomic.Uint64
	latTotal    atomic.Uint64

	clusterFwd    atomic.Uint64
	clusterFwdErr atomic.Uint64

	reloadTotal    atomic.Uint64
	reloadFailures atomic.Uint64
	reloadLastNs   atomic.Int64 // son Reload suresi
	reloadOKUnix   atomic.Int64 // son basarili Reload (unix sn)

	snapSavedUnix atomic.Int64 // yonlendirme goruntusunun veri zamani (unix sn)
	snapWrites    atomic.Uint64
	snapWriteErrs atomic.Uint64
	snapFromDisk  atomic.Bool // su an goruntuden (DB'siz) hizmet veriliyor

	certNotAfter atomic.Int64 // unix sn; 0 = bilinmiyor
)

// ObserveRequest, tamamlanan bir HTTP istegini sayar (sicak yol, kilitsiz).
func ObserveRequest(status int, d time.Duration) {
	c := classOther
	if status >= 100 && status < 600 {
		c = status / 100
	}
	reqByClass[c].Add(1)
	sec := d.Seconds()
	i := 0
	for i < len(latencyBuckets) && sec > latencyBuckets[i] {
		i++
	}
	latCounts[i].Add(1)
	latSumNanos.Add(uint64(d.Nanoseconds()))
	latTotal.Add(1)
}

// AddBytes, istek (in) ve yanit (out) govde bayt sayaclarini artirir.
func AddBytes(in, out int64) {
	if in > 0 {
		bytesIn.Add(uint64(in))
	}
	if out > 0 {
		bytesOut.Add(uint64(out))
	}
}

// CountReject, reddi sinifina gore sayar. reason ingress'in RejectReason metnidir
// ("waf:sqli", "bot:fail", "rate_limited", "geo:TR" ...); yalniz on ek bakilir.
func CountReject(reason string) {
	rejects[classifyReject(reason)].Add(1)
}

func classifyReject(r string) int {
	switch {
	case strings.HasPrefix(r, "waf"):
		return RejectWAF
	case strings.HasPrefix(r, "bot:"):
		return RejectBot
	case strings.HasPrefix(r, "rate"):
		return RejectRate
	case strings.HasPrefix(r, "geo:"):
		return RejectGeo
	case strings.HasPrefix(r, "ip_forbidden"):
		return RejectIP
	case strings.HasPrefix(r, "policy_"):
		return RejectPolicy
	case strings.HasPrefix(r, "auth_required"), strings.HasPrefix(r, "access_denied"),
		strings.HasPrefix(r, "mtls"), strings.HasPrefix(r, "mcp_"):
		return RejectAuth
	}
	return RejectOther
}

// ClusterForward, dugumler arasi aktarim denemesini sayar; basarisizsa hata da.
func ClusterForward(err error) {
	clusterFwd.Add(1)
	if err != nil {
		clusterFwdErr.Add(1)
	}
}

// ReloadDone, yonlendirme yenilemesinin sonucunu kaydeder.
func ReloadDone(d time.Duration, err error) {
	reloadTotal.Add(1)
	reloadLastNs.Store(d.Nanoseconds())
	if err != nil {
		reloadFailures.Add(1)
		return
	}
	reloadOKUnix.Store(time.Now().Unix())
}

// LastReloadOK, son basarili Reload zamani (sifir = hic).
func LastReloadOK() time.Time {
	if v := reloadOKUnix.Load(); v > 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// SetSnapshotTime, yonlendirme goruntusunun veri zamanini isaretler.
func SetSnapshotTime(t time.Time) { snapSavedUnix.Store(t.Unix()) }

// SnapshotTime, goruntu veri zamani (sifir = yok).
func SnapshotTime() time.Time {
	if v := snapSavedUnix.Load(); v > 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// SnapshotWrite, goruntu yazim sonucunu sayar.
func SnapshotWrite(err error) {
	snapWrites.Add(1)
	if err != nil {
		snapWriteErrs.Add(1)
	}
}

// SetServingFromSnapshot, yonlendirmenin DB yerine diskteki goruntuden verildigini isaretler.
func SetServingFromSnapshot(v bool) { snapFromDisk.Store(v) }

// ServingFromSnapshot, bkz. SetServingFromSnapshot.
func ServingFromSnapshot() bool { return snapFromDisk.Load() }

// SetCertNotAfter, varsayilan sunucu sertifikasinin bitis zamanini kaydeder.
func SetCertNotAfter(t time.Time) { certNotAfter.Store(t.Unix()) }

// CertNotAfter, bilinmiyorsa sifir zaman doner.
func CertNotAfter() time.Time {
	if v := certNotAfter.Load(); v != 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// --- gauge kaydi ---------------------------------------------------------------

type gauge struct {
	name, help string
	fn         func() float64
}

var (
	gaugeMu sync.RWMutex
	gauges  []gauge
)

// RegisterGauge, her scrape'te cagrilan bir gauge ekler. fn hizli olmali ve kilit
// bekletmemeli. Ayni ad tekrar eklenirse ustune yazilir.
func RegisterGauge(name, help string, fn func() float64) {
	gaugeMu.Lock()
	defer gaugeMu.Unlock()
	for i := range gauges {
		if gauges[i].name == name {
			gauges[i] = gauge{name, help, fn}
			return
		}
	}
	gauges = append(gauges, gauge{name, help, fn})
}

// --- metin cikti ------------------------------------------------------------------

func fmtF(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func head(b *bytes.Buffer, name, help, typ string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

// Write, tum metrikleri Prometheus metin biciminde yazar.
func Write(w io.Writer) error {
	var b bytes.Buffer

	head(&b, "zorven_http_requests_total", "Ingress isteklerinin durum sinifina gore sayisi.", "counter")
	for i := 0; i < numClasses; i++ {
		fmt.Fprintf(&b, "zorven_http_requests_total{class=%q} %d\n", classNames[i], reqByClass[i].Load())
	}

	head(&b, "zorven_http_request_duration_seconds", "Ingress istek suresi histogrami.", "histogram")
	var cum uint64
	for i, le := range latencyBuckets {
		cum += latCounts[i].Load()
		fmt.Fprintf(&b, "zorven_http_request_duration_seconds_bucket{le=%q} %d\n", fmtF(le), cum)
	}
	cum += latCounts[len(latencyBuckets)].Load()
	fmt.Fprintf(&b, "zorven_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", cum)
	fmt.Fprintf(&b, "zorven_http_request_duration_seconds_sum %s\n", fmtF(float64(latSumNanos.Load())/1e9))
	fmt.Fprintf(&b, "zorven_http_request_duration_seconds_count %d\n", latTotal.Load())

	head(&b, "zorven_http_bytes_total", "Ingress govde bayti (in: istek, out: yanit).", "counter")
	fmt.Fprintf(&b, "zorven_http_bytes_total{direction=\"in\"} %d\n", bytesIn.Load())
	fmt.Fprintf(&b, "zorven_http_bytes_total{direction=\"out\"} %d\n", bytesOut.Load())

	head(&b, "zorven_rejects_total", "Ingress'te reddedilen isteklerin sinifa gore sayisi.", "counter")
	for i := 0; i < numRejects; i++ {
		fmt.Fprintf(&b, "zorven_rejects_total{kind=%q} %d\n", rejectNames[i], rejects[i].Load())
	}

	head(&b, "zorven_cluster_forwards_total", "Baska dugumdeki istemciye aktarim denemeleri.", "counter")
	fmt.Fprintf(&b, "zorven_cluster_forwards_total %d\n", clusterFwd.Load())
	head(&b, "zorven_cluster_forward_errors_total", "Basarisiz dugumler arasi aktarimlar.", "counter")
	fmt.Fprintf(&b, "zorven_cluster_forward_errors_total %d\n", clusterFwdErr.Load())

	head(&b, "zorven_router_reloads_total", "Yonlendirme yenileme denemeleri.", "counter")
	fmt.Fprintf(&b, "zorven_router_reloads_total %d\n", reloadTotal.Load())
	head(&b, "zorven_router_reload_failures_total", "Basarisiz yonlendirme yenilemeleri.", "counter")
	fmt.Fprintf(&b, "zorven_router_reload_failures_total %d\n", reloadFailures.Load())
	head(&b, "zorven_router_reload_last_duration_seconds", "Son yenilemenin suresi.", "gauge")
	fmt.Fprintf(&b, "zorven_router_reload_last_duration_seconds %s\n", fmtF(float64(reloadLastNs.Load())/1e9))
	head(&b, "zorven_router_reload_last_success_age_seconds", "Son basarili yenilemeden beri gecen sure (-1: hic).", "gauge")
	fmt.Fprintf(&b, "zorven_router_reload_last_success_age_seconds %s\n", ageOrNeg(LastReloadOK()))

	head(&b, "zorven_route_snapshot_age_seconds", "Diskteki yonlendirme goruntusunun veri yasi (-1: yok).", "gauge")
	fmt.Fprintf(&b, "zorven_route_snapshot_age_seconds %s\n", ageOrNeg(SnapshotTime()))
	head(&b, "zorven_route_snapshot_writes_total", "Goruntu yazim denemeleri.", "counter")
	fmt.Fprintf(&b, "zorven_route_snapshot_writes_total %d\n", snapWrites.Load())
	head(&b, "zorven_route_snapshot_write_errors_total", "Basarisiz goruntu yazimlari.", "counter")
	fmt.Fprintf(&b, "zorven_route_snapshot_write_errors_total %d\n", snapWriteErrs.Load())
	head(&b, "zorven_route_serving_from_snapshot", "1: yonlendirme DB yerine goruntuden veriliyor.", "gauge")
	fmt.Fprintf(&b, "zorven_route_serving_from_snapshot %d\n", boolInt(snapFromDisk.Load()))

	head(&b, "zorven_tls_cert_days_remaining", "Varsayilan sertifikanin kalan gun sayisi (-1: bilinmiyor).", "gauge")
	if t := CertNotAfter(); t.IsZero() {
		b.WriteString("zorven_tls_cert_days_remaining -1\n")
	} else {
		fmt.Fprintf(&b, "zorven_tls_cert_days_remaining %s\n", fmtF(time.Until(t).Hours()/24))
	}

	gaugeMu.RLock()
	gs := append([]gauge(nil), gauges...)
	gaugeMu.RUnlock()
	sort.Slice(gs, func(i, j int) bool { return gs[i].name < gs[j].name })
	for _, g := range gs {
		head(&b, g.name, g.help, "gauge")
		fmt.Fprintf(&b, "%s %s\n", g.name, fmtF(g.fn()))
	}

	writeRuntime(&b)
	_, err := w.Write(b.Bytes())
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func ageOrNeg(t time.Time) string {
	if t.IsZero() {
		return "-1"
	}
	return fmtF(time.Since(t).Seconds())
}

func writeRuntime(b *bytes.Buffer) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	head(b, "go_goroutines", "Goroutine sayisi.", "gauge")
	fmt.Fprintf(b, "go_goroutines %d\n", runtime.NumGoroutine())
	head(b, "go_memstats_alloc_bytes", "Kullanimdaki heap baytlari.", "gauge")
	fmt.Fprintf(b, "go_memstats_alloc_bytes %d\n", m.Alloc)
	head(b, "go_memstats_sys_bytes", "Isletim sisteminden alinan toplam bellek.", "gauge")
	fmt.Fprintf(b, "go_memstats_sys_bytes %d\n", m.Sys)
	head(b, "go_memstats_heap_objects", "Canli heap nesnesi sayisi.", "gauge")
	fmt.Fprintf(b, "go_memstats_heap_objects %d\n", m.HeapObjects)
	head(b, "go_gc_cycles_total", "Tamamlanan GC dongusu sayisi.", "counter")
	fmt.Fprintf(b, "go_gc_cycles_total %d\n", m.NumGC)
	head(b, "go_gc_pause_seconds_total", "GC duraklamalarinin toplami.", "counter")
	fmt.Fprintf(b, "go_gc_pause_seconds_total %s\n", fmtF(float64(m.PauseTotalNs)/1e9))
}
