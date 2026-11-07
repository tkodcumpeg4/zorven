package obs

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// Instrument, bir http.Handler'i saran ara katman: durum sinifi, sure ve bayt
// sayar. Sicak yolda yalniz atomic islem yapar. Hijack/Flush/Unwrap korunur
// (WebSocket ve akis kesilmez).
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusWriter{ResponseWriter: w}
		var cb *countBody
		if r.Body != nil && r.Body != http.NoBody {
			cb = &countBody{ReadCloser: r.Body}
			r.Body = cb
		}
		defer func() {
			st := rec.status
			if st == 0 {
				st = http.StatusOK
			}
			if rec.hijacked {
				// Hijack edilen baglanti (WebSocket): durum 101 kabul edilir.
				st = http.StatusSwitchingProtocols
			}
			var in int64
			if cb != nil {
				in = cb.n
			}
			AddBytes(in, rec.n)
			ObserveRequest(st, time.Since(started))
		}()
		next.ServeHTTP(rec, r)
	})
}

type countBody struct {
	io.ReadCloser
	n int64
}

func (c *countBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}

type statusWriter struct {
	http.ResponseWriter
	status   int
	n        int64
	hijacked bool
}

func (s *statusWriter) WriteHeader(code int) {
	// 1xx ara yanitlar (103 Early Hints) son durum degildir.
	if code >= 200 && s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	s.n += int64(n)
	return n, err
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack desteklenmiyor")
	}
	c, rw, err := h.Hijack()
	if err == nil {
		s.hijacked = true
	}
	return c, rw, err
}

// Unwrap, http.ResponseController'in alttaki yaziciya ulasmasini saglar.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
