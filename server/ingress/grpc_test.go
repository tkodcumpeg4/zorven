package ingress

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsGRPCContentType(t *testing.T) {
	cases := map[string]bool{
		"application/grpc":                true,
		"application/grpc+proto":          true,
		"application/grpc+json":           true,
		"Application/GRPC; charset=utf-8": true,
		"application/grpc-web":            false, // HTTP/1.1 uyumlu, ozel yol yok
		"application/grpc-web+proto":      false,
		"application/grpc-web-text":       false,
		"application/json":                false,
		"":                                false,
		"application/grpcx":               false,
		"text/plain; x=application/grpc":  false,
	}
	for ct, want := range cases {
		if got := isGRPCContentType(ct); got != want {
			t.Errorf("isGRPCContentType(%q) = %v, beklenen %v", ct, got, want)
		}
	}
}

func TestIsGRPCRequestNeedsHTTP2(t *testing.T) {
	r := httptest.NewRequest("POST", "http://x/svc/M", nil)
	r.Header.Set("Content-Type", "application/grpc")
	r.ProtoMajor = 1
	if isGRPCRequest(r) {
		t.Fatal("HTTP/1.1 uzerinden gelen istek gRPC sayilmamali")
	}
	r.ProtoMajor = 2
	if !isGRPCRequest(r) {
		t.Fatal("h2 + application/grpc gRPC olmali")
	}
}

func TestTETrailers(t *testing.T) {
	r := httptest.NewRequest("POST", "http://x/", nil)
	if teTrailers(r) {
		t.Fatal("TE yokken false olmali")
	}
	r.Header.Set("Te", "gzip, Trailers")
	if !teTrailers(r) {
		t.Fatal("TE: trailers taninmali")
	}
}

func TestGRPCEscapeMessage(t *testing.T) {
	if got := grpcEscapeMessage("hata 100%"); got != "hata 100%25" {
		t.Fatalf("got %q", got)
	}
	if got := grpcEscapeMessage("çok"); got != "%C3%A7ok" {
		t.Fatalf("got %q", got)
	}
}

// writeTrailers: govde yazildiktan sonra trailer'lar HTTP/2'de HEADERS olarak gider.
func TestWriteTrailersHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.WriteHeader(200)
		io.WriteString(w, "veri")
		writeTrailers(w, http.Header{
			"Grpc-Status":  {"0"},
			"Grpc-Message": {"tamam"},
			"Connection":   {"close"}, // hop-by-hop: yazilmaz
		})
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	c := srv.Client()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.ProtoMajor != 2 || string(b) != "veri" {
		t.Fatalf("proto=%s govde=%q", resp.Proto, b)
	}
	if resp.Trailer.Get("Grpc-Status") != "0" || resp.Trailer.Get("Grpc-Message") != "tamam" {
		t.Fatalf("trailer eksik: %v", resp.Trailer)
	}
	if resp.Trailer.Get("Connection") != "" {
		t.Fatalf("hop-by-hop trailer yazilmamali: %v", resp.Trailer)
	}
}

// HTTP/1.1 ziyaretcide trailer'lar chunked govdenin sonunda gelir.
func TestWriteTrailersHTTP1Chunked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "veri")
		w.(http.Flusher).Flush() // ingress her yazimi flush eder; chunked'a gecis
		writeTrailers(w, http.Header{"X-Sum": {"7"}})
	}))
	defer srv.Close()

	tr := &http.Transport{TLSClientConfig: &tls.Config{}}
	resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.ProtoMajor != 1 || string(b) != "veri" {
		t.Fatalf("proto=%s govde=%q", resp.Proto, b)
	}
	if resp.Trailer.Get("X-Sum") != "7" {
		t.Fatalf("chunked trailer eksik: %v", resp.Trailer)
	}
}

// writeGRPCError: trailers-only yanit (HTTP 200 + grpc-status basliklarda).
func TestWriteGRPCError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeGRPCError(rec, grpcStatusUnavailable, "ajan eski")
	if rec.Code != 200 || rec.Header().Get("Grpc-Status") != "14" ||
		rec.Header().Get("Content-Type") != "application/grpc" ||
		rec.Header().Get("Grpc-Message") != "ajan eski" {
		t.Fatalf("beklenmeyen: %d %v", rec.Code, rec.Header())
	}
}
