package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Idempotency-Key destegi (Stripe benzeri). Secili kritik POST uclarini sarar:
//
//   - Anahtar yok               -> normal calisir.
//   - Ilk istek                 -> anahtar ayrilir, yanit saklanir (24 sa).
//   - Ayni anahtar+ayni istek   -> saklanan yanit "Idempotent-Replayed: true" ile tekrarlanir.
//   - Ayni anahtar+farkli istek -> 422 idempotency_key_mismatch.
//   - Ilk istek hala suruyor    -> 409 idempotency_in_progress (Retry-After: 1).
//
// Depo IdempotencyStore'u desteklemiyorsa anahtar sessizce yok sayilir.
// Saklanan yanitlar: 2xx ve 4xx (401/403/408/409/425/429 haric: bunlar yeniden
// denemeyle duzelebilir). Digerlerinde (5xx dahil) ayirma silinir, istemci tekrar deneyebilir.

const (
	idempotencyKeyHeader  = "Idempotency-Key"
	idempotencyMaxKeyLen  = 128
	idempotencyMaxBody    = 2 << 20
	idempotencyFinishWait = 5 * time.Second
)

func validIdempotencyKey(k string) bool {
	if k == "" || len(k) > idempotencyMaxKeyLen {
		return false
	}
	for _, c := range k {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '-', c == ':', c == '.':
		default:
			return false
		}
	}
	return true
}

func idempotencyCacheable(status int) bool {
	if status >= 200 && status < 300 {
		return true
	}
	if status >= 400 && status < 500 {
		switch status {
		case 401, 403, 408, 409, 425, 429:
			return false
		}
		return true
	}
	return false
}

// idempotentFunc, h'yi Idempotency-Key destegiyle sarar.
func (s *Server) idempotentFunc(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(idempotencyKeyHeader)
		if key == "" {
			h(w, r)
			return
		}
		var ist store.IdempotencyStore
		if s.Store != nil {
			ist, _ = s.Store.(store.IdempotencyStore)
		}
		if ist == nil {
			h(w, r) // desteklenmiyor: atla
			return
		}
		if !validIdempotencyKey(key) {
			writeJSONError(w, http.StatusBadRequest, "invalid_idempotency_key",
				"Idempotency-Key 1-128 karakter olmali ([A-Za-z0-9_-:.])")
			return
		}
		// Govdeyi oku (hash icin) ve handler'a geri ver.
		var body []byte
		if r.Body != nil {
			var err error
			body, err = io.ReadAll(io.LimitReader(r.Body, idempotencyMaxBody+1))
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "bad_request", "govde okunamadi")
				return
			}
			if len(body) > idempotencyMaxBody {
				// Cok buyuk: idempotency uygulanmaz, handler kendi sinirini uygular.
				r.Body = struct {
					io.Reader
					io.Closer
				}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
				h(w, r)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		tenantID, ok := tenantFromContext(r.Context())
		if !ok {
			tenantID = "_platform" // admin anahtari (kiracisiz) uclari
		}
		sum := sha256.New()
		sum.Write([]byte(r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery + "\n"))
		sum.Write(body)
		reqSHA := sum.Sum(nil)

		rec, created, err := ist.BeginIdempotency(r.Context(), tenantID, key, reqSHA)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !created {
			if !bytes.Equal(rec.RequestSHA, reqSHA) {
				writeJSONError(w, http.StatusUnprocessableEntity, "idempotency_key_mismatch",
					"Bu Idempotency-Key baska bir istekle kullanilmis")
				return
			}
			if rec.Status == 0 {
				w.Header().Set("Retry-After", "1")
				writeJSONError(w, http.StatusConflict, "idempotency_in_progress",
					"Ayni Idempotency-Key ile bir istek hala isleniyor")
				return
			}
			if rec.ContentType != "" {
				w.Header().Set("Content-Type", rec.ContentType)
			}
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(rec.Status)
			_, _ = w.Write(rec.Response)
			return
		}

		rw := &idemRecorder{ResponseWriter: w, status: http.StatusOK}
		finished := false
		defer func() {
			// Panik ya da kaydedilemeyen yanit: ayirmayi kaldir ki istemci tekrar deneyebilsin.
			if finished {
				return
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), idempotencyFinishWait)
			defer cancel()
			_ = ist.DeleteIdempotency(ctx, tenantID, key)
		}()
		h(rw, r)
		if idempotencyCacheable(rw.status) && !rw.overflow {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), idempotencyFinishWait)
			defer cancel()
			if ist.CompleteIdempotency(ctx, tenantID, key, rw.status, rw.buf.Bytes(), rw.Header().Get("Content-Type")) == nil {
				finished = true
			}
		}
	}
}

// idemRecorder, yaniti istemciye gecirirken kopyalar.
type idemRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	buf         bytes.Buffer
	overflow    bool
}

func (c *idemRecorder) WriteHeader(code int) {
	if !c.wroteHeader {
		c.status = code
		c.wroteHeader = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *idemRecorder) Write(b []byte) (int, error) {
	c.wroteHeader = true
	if c.buf.Len()+len(b) > idempotencyMaxBody {
		c.overflow = true
	} else {
		c.buf.Write(b)
	}
	return c.ResponseWriter.Write(b)
}

func (c *idemRecorder) Unwrap() http.ResponseWriter { return c.ResponseWriter }
