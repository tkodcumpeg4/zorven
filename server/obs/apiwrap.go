package obs

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// HeaderRequestID, istek kimligi basligi (API yankisi ve tunel hedefi).
const HeaderRequestID = "X-Request-Id"

// ValidRequestID, disaridan gelen kimligin korunabilir olup olmadigini soyler:
// ^[A-Za-z0-9._-]{1,64}$. Neden: eskiden deger dogrulamasiz ve sinirsiz
// yankilaniyordu (300+ karakter, "<script>" dahil, A5 #7); loglara ve yanit
// basligina ham kullanici girdisi tasimak log sahteleme / XSS yuzeyidir.
func ValidRequestID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// NewRequestID, "req_<24 hex>" bicimli yeni kimlik uretir.
func NewRequestID() string {
	b := make([]byte, 12)
	if _, err := crand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}

// RequestIDFor, gelen X-Request-Id gecerliyse onu, degilse yenisini doner.
func RequestIDFor(r *http.Request) string {
	if v := r.Header.Get(HeaderRequestID); ValidRequestID(v) {
		return v
	}
	return NewRequestID()
}

// InstrumentAPI, kontrol API'sini saran ara katman:
//
//   - istek kimligi: gecerli X-Request-Id korunur, degilse uretilir; yanit
//     basligina yazilir (s.fail ve 5xx govdesi buradan okur),
//   - metrik: zorven_api_requests_total{route,method,code_class} ve sure
//     histogrami; route = routeOf(r) (ServeMux kalibi, ham yol DEGIL),
//   - panik kurtarma: net/http isleyici panigini kendisi kurtarir ama istemci
//     yalniz EOF gorur ve metrik/kimlik yoktur. Burada ERROR log (yigin + kimlik),
//     zorven_panics_total{where="api"} ve (baslik yazilmadiysa) 500 JSON doner.
//     http.ErrAbortHandler YENIDEN firlatilir: kasitli baglanti kesme (P3) bozulmasin.
//
// Hijack edilen (WebSocket) isteklerde sure histograma girmez (oturum suresi
// API gecikmesi degildir); sayac 1xx sinifina duser.
func InstrumentAPI(next http.Handler, routeOf func(*http.Request) string, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rid := RequestIDFor(r)
		w.Header().Set(HeaderRequestID, rid)
		rec := &statusWriter{ResponseWriter: w}
		defer func() {
			p := recover()
			if p != nil {
				if p == http.ErrAbortHandler {
					observeAPIDone(rec, r, routeOf, started)
					panic(p)
				}
				CountPanic("api")
				if log != nil {
					log.Error("API isleyicisi panikledi", "request_id", rid, "metot", r.Method,
						"rota", routeOf(r), "panik", p, "yigin", string(debug.Stack()))
				}
				if rec.status == 0 && !rec.hijacked {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					rec.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(rec).Encode(map[string]any{"error": map[string]string{
						"code": "internal", "message": "beklenmeyen hata", "request_id": rid,
					}})
				} else if rec.status == 0 {
					rec.status = http.StatusInternalServerError
				}
			}
			observeAPIDone(rec, r, routeOf, started)
		}()
		next.ServeHTTP(rec, r)
	})
}

func observeAPIDone(rec *statusWriter, r *http.Request, routeOf func(*http.Request) string, started time.Time) {
	st := rec.status
	if st == 0 {
		st = http.StatusOK
	}
	route := ""
	if routeOf != nil {
		route = routeOf(r)
	}
	if rec.hijacked {
		apiStatFor(route).codes[apiMethodIndex(r.Method)][apiClass1xx].Add(1)
		return
	}
	ObserveAPI(route, r.Method, st, time.Since(started))
}
