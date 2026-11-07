package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// Akis B1: uzunlugu bilinmeyen (chunked / gRPC) istek govdesi gercek okunan
// baytlarla sayilir; bilinen uzunlukta sayac kurulmaz (bytes_in = ContentLength).
func TestCountUnknownLenBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", io.NopCloser(strings.NewReader("0123456789")))
	r.ContentLength = -1
	cb := countUnknownLenBody(r)
	if cb == nil {
		t.Fatal("sayac kurulmadi")
	}
	b, _ := io.ReadAll(r.Body)
	if len(b) != 10 || cb.n.Load() != 10 {
		t.Fatalf("okunan=%d sayac=%d", len(b), cb.n.Load())
	}

	r2 := httptest.NewRequest("POST", "/", strings.NewReader("abc"))
	if countUnknownLenBody(r2) != nil {
		t.Fatal("bilinen uzunlukta sayac kurulmamali")
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.ContentLength = -1
	r3.Body = http.NoBody
	if countUnknownLenBody(r3) != nil {
		t.Fatal("govdesiz istekte sayac kurulmamali")
	}
}

type usageSink struct{ in []int64 }

func (u *usageSink) Record(_ context.Context, ev bandwidth.UsageEvent) error {
	u.in = append(u.in, ev.BytesIn)
	return nil
}
func (u *usageSink) Close() error { return nil }

func TestRecordWithIDChunkedBytesIn(t *testing.T) {
	sink := &usageSink{}
	h := &Handler{ReqLog: reqlog.New(10), BandwidthRecorder: sink}

	// chunked: sayac degeri hem logda hem kota kaydedicisinde
	r := httptest.NewRequest("POST", "/x", strings.NewReader("hello world"))
	r.ContentLength = -1
	rec := &recorder{ResponseWriter: httptest.NewRecorder(), status: 200}
	rec.in = countUnknownLenBody(r)
	io.Copy(io.Discard, r.Body)
	h.recordWithID("t1", "ten", false, r, rec, time.Now(), "")
	if l := h.ReqLog.List(10, ""); len(l) != 1 || l[0].BytesIn != 11 {
		t.Fatalf("log = %+v", l)
	}
	if sink.in[0] != 11 {
		t.Fatalf("kota bytes_in = %d", sink.in[0])
	}

	// ContentLength bilinen: ContentLength kullanilir (davranis degismez)
	r2 := httptest.NewRequest("POST", "/y", strings.NewReader("12345"))
	rec2 := &recorder{ResponseWriter: httptest.NewRecorder(), status: 200}
	h.recordWithID("t1", "ten", false, r2, rec2, time.Now(), "")
	if sink.in[1] != 5 {
		t.Fatalf("bilinen uzunluk bytes_in = %d", sink.in[1])
	}
}
