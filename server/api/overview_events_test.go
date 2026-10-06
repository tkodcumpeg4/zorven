package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// SSE: tunel/istemci olaylari yalnizca sahibi kiraciya gorunur.
func TestStreamEventVisible_TenantScoped(t *testing.T) {
	ev := events.Event{Type: events.TypeTunnelCreated, Data: map[string]string{"id": "t1"}, Tenant: "ten_a"}
	if !streamEventVisible(ev, "ten_a") {
		t.Error("sahibi kiraci olayi gormeli")
	}
	if streamEventVisible(ev, "ten_b") {
		t.Error("baska kiraci olayi GORMEMELI")
	}
	// Etiketsiz olay fail-closed.
	if streamEventVisible(events.Event{Type: events.TypeTunnelDeleted}, "ten_a") {
		t.Error("etiketsiz olay gizlenmeli")
	}
	// Izleyen kiraci bilinmiyorsa hicbir sey gorunmez.
	if streamEventVisible(ev, "") {
		t.Error("kiracisiz izleyici olay gormemeli")
	}
	// Istek kaydi: etiket yoksa kayittaki kiraci kullanilir.
	rq := events.Event{Type: events.TypeRequestCompleted, Data: reqlog.Entry{TenantID: "ten_a"}}
	if !streamEventVisible(rq, "ten_a") || streamEventVisible(rq, "ten_b") {
		t.Error("request.completed kiraci suzmesi bozuk")
	}
}

func TestDecodeOptional(t *testing.T) {
	var body struct {
		Name string `json:"name"`
	}
	// Bos govde gecerli, yanit yazilmaz.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nil)
	if !decodeOptional(w, r, &body) || w.Body.Len() != 0 {
		t.Fatalf("bos govde kabul edilmeliydi: %d %q", w.Code, w.Body.String())
	}
	// Dolu govde okunur.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/x", strings.NewReader(`{"name":"pc"}`))
	if !decodeOptional(w, r, &body) || body.Name != "pc" {
		t.Fatalf("govde okunamadi: %+v", body)
	}
	// Bozuk govde tek 400 yazar.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/x", strings.NewReader(`{bozuk`))
	if decodeOptional(w, r, &body) || w.Code != 400 {
		t.Fatalf("bozuk govde 400 olmaliydi: %d", w.Code)
	}
}
