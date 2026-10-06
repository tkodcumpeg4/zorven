package ingress

// FAZ 2 / F10 — Protokol uyumluluk matrisinin otomatik karsiligi.
// docs/protokol-uyumluluk.md icindeki her satirin burada bir testi vardir.
// Bir davranis degisirse test kirmizi doner ve belge ile kod birlikte guncellenir.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// MATRIS: hop-by-hop basliklar iletilmez.
func TestCompatHopByHopHeadersDropped(t *testing.T) {
	want := []string{
		"connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"proxy-connection", "te", "trailer", "transfer-encoding", "upgrade",
	}
	for _, h := range want {
		if !hopByHop[h] {
			t.Errorf("%q hop-by-hop listesinde olmali (belge boyle diyor)", h)
		}
	}
	if len(hopByHop) != len(want) {
		t.Errorf("hop-by-hop listesi %d giris, belgede %d var — belge guncellenmeli",
			len(hopByHop), len(want))
	}
}

// MATRIS: gRPC desteklenmiyor — cunku trailer basliklari dusuruluyor.
// Bu test, "trailer destegi eklendi ama belge guncellenmedi" durumunu yakalar.
func TestCompatTrailersNotForwarded(t *testing.T) {
	if !hopByHop["trailer"] {
		t.Fatal("trailer artik iletiliyor: gRPC satiri ve matris guncellenmeli")
	}
}

// MATRIS: WebSocket yukseltmeleri ayri yola ayrilir.
func TestCompatWebSocketUpgradeDetected(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"websocket", true},
		{"WebSocket", true}, // buyuk/kucuk duyarsiz olmali (RFC)
		{"WEBSOCKET", true},
		{"h2c", false},
		{"", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
		if c.header != "" {
			r.Header.Set("Upgrade", c.header)
		}
		if got := isWebSocketUpgrade(r); got != c.want {
			t.Errorf("Upgrade=%q -> %v, beklenen %v", c.header, got, c.want)
		}
	}
}

// MATRIS: SSE ve akan yanitlar icin her yazmada flush yapilir.
// Flush olmazsa SSE istemcide tamponda bekler ve "canli" olmaz.
func TestCompatStreamingFlushesEveryWrite(t *testing.T) {
	rec := &countingFlusher{ResponseRecorder: httptest.NewRecorder()}
	w := newFlushWriter(rec)

	for i := 0; i < 3; i++ {
		if _, err := w.Write([]byte("data: x\n\n")); err != nil {
			t.Fatal(err)
		}
	}
	if rec.flushes != 3 {
		t.Errorf("%d flush yapildi, her yazmada bir tane beklenirdi (3)", rec.flushes)
	}
}

// Sifir bayt yazimi flush tetiklememeli: bos flush akisa deger katmaz.
func TestCompatFlushWriterSkipsEmptyWrites(t *testing.T) {
	rec := &countingFlusher{ResponseRecorder: httptest.NewRecorder()}
	w := newFlushWriter(rec)
	if _, err := w.Write(nil); err != nil {
		t.Fatal(err)
	}
	if rec.flushes != 0 {
		t.Errorf("bos yazim flush tetikledi (%d)", rec.flushes)
	}
}

// Flusher desteklemeyen writer'da newFlushWriter panik etmemeli.
func TestCompatFlushWriterWithoutFlusher(t *testing.T) {
	var buf bytes.Buffer
	w := newFlushWriter(nopResponseWriter{&buf})
	if _, err := w.Write([]byte("merhaba")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "merhaba" {
		t.Errorf("yazim kayboldu: %q", buf.String())
	}
}

// MATRIS: belgedeki sayilar koddaki sabitlerle ayni olmali.
func TestCompatDocumentedLimits(t *testing.T) {
	if protocol.MaxBodyBytes != 32<<20 {
		t.Errorf("MaxBodyBytes = %d; belge 32 MB diyor", protocol.MaxBodyBytes)
	}
	if protocol.BodyChunkSize != 64<<10 {
		t.Errorf("BodyChunkSize = %d; belge 64 KB diyor", protocol.BodyChunkSize)
	}
	if protocol.WSReadLimit != 1<<20 {
		t.Errorf("WSReadLimit = %d; belge 1 MB diyor", protocol.WSReadLimit)
	}
	if UpstreamTimeout.Seconds() != 30 {
		t.Errorf("UpstreamTimeout = %s; belge 30 sn diyor", UpstreamTimeout)
	}
}

// MATRIS: belge dosyasi gercekten var ve matris basligini iceriyor.
// Kod ile belgenin birlikte tasindigini garantiler.
func TestCompatDocumentExists(t *testing.T) {
	b, err := readRepoFile("../../docs/protokol-uyumluluk.md")
	if err != nil {
		t.Fatalf("uyumluluk belgesi okunamadi: %v", err)
	}
	doc := string(b)
	for _, must := range []string{"Protokol Uyumluluk Matrisi", "gRPC", "HTTP/3", "SSE", "WebSocket"} {
		if !strings.Contains(doc, must) {
			t.Errorf("belgede %q gecmiyor", must)
		}
	}
}

// --- yardimcilar ---

type countingFlusher struct {
	*httptest.ResponseRecorder
	flushes int
}

func (c *countingFlusher) Flush() { c.flushes++ }

type nopResponseWriter struct{ buf *bytes.Buffer }

func (n nopResponseWriter) Header() http.Header         { return http.Header{} }
func (n nopResponseWriter) Write(p []byte) (int, error) { return n.buf.Write(p) }
func (n nopResponseWriter) WriteHeader(int)             {}

func readRepoFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
