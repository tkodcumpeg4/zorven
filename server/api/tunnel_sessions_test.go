package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type sessAPIStore struct {
	*doorAPIStore
	revs   map[string]time.Time
	events []accesslog.Event
}

func (f *sessAPIStore) SetTunnelAccessPolicy(_ context.Context, _ string, p store.TunnelAccessPolicy) error {
	f.policy = p
	return nil
}
func (f *sessAPIStore) RecordDoorRevocation(_ context.Context, _, id string, at time.Time) error {
	f.revs[id] = at
	return nil
}
func (f *sessAPIStore) InsertAccessEvents(_ context.Context, ev []accesslog.Event) error {
	f.events = append(f.events, ev...)
	return nil
}

func sessServer() (*Server, *sessAPIStore) {
	s, st := doorServer(true)
	ss := &sessAPIStore{doorAPIStore: st, revs: map[string]time.Time{}}
	s.Store = ss
	return s, ss
}

func sessCall(s *Server, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/tunnels/tun_1/access", strings.NewReader(body))
	r = r.WithContext(withTenant(r.Context(), "ten_1"))
	r.SetPathValue("id", "tun_1")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestSetAccessSessionTTLValidationAndEvent(t *testing.T) {
	s, st := sessServer()
	for _, bad := range []string{"1", "14", "43201", "-1"} {
		w := sessCall(s, s.setTunnelAccess, `{"mode":"oauth","enabled":true,"config":{"session_ttl_minutes":`+bad+`}}`)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid_session_ttl") {
			t.Errorf("ttl=%s: %d %s", bad, w.Code, w.Body.String())
		}
	}
	w := sessCall(s, s.setTunnelAccess, `{"mode":"oauth","enabled":true,"config":{"session_ttl_minutes":60,"session_sliding":true}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"session_ttl_minutes":60`) || !strings.Contains(w.Body.String(), `"session_sliding":true`) {
		t.Fatalf("kayit/cikti: %d %s", w.Code, w.Body.String())
	}
	if len(st.events) != 1 || st.events[0].Reason != accesslog.ReasonSessionTTLChanged {
		t.Fatalf("TTL degisimi olayi yazilmali: %+v", st.events)
	}
	// Alan verilmezse mevcut deger korunur, degisim yok -> yeni olay yok.
	sessCall(s, s.setTunnelAccess, `{"mode":"oauth","enabled":true,"config":{"providers":["google"]}}`)
	if len(st.events) != 1 || !strings.Contains(string(st.policy.Config), `"session_ttl_minutes":60`) {
		t.Fatalf("mevcut deger korunmali: %s events=%d", st.policy.Config, len(st.events))
	}
	// 0 = varsayilana don -> olay.
	sessCall(s, s.setTunnelAccess, `{"mode":"oauth","enabled":true,"config":{"session_ttl_minutes":0,"session_sliding":false}}`)
	if len(st.events) != 2 {
		t.Fatalf("varsayilana donus olayi: %d", len(st.events))
	}
}

func TestRevokeVisitorSessions(t *testing.T) {
	s, st := sessServer()
	w := sessCall(s, s.revokeVisitorSessions, ``)
	if w.Code != http.StatusOK || st.revs["*"].IsZero() {
		t.Fatalf("tum oturumlar: %d %s revs=%v", w.Code, w.Body.String(), st.revs)
	}
	if len(st.events) != 1 || st.events[0].Reason != accesslog.ReasonSessionsRevokedAll {
		t.Fatalf("olay: %+v", st.events)
	}
	w = sessCall(s, s.revokeVisitorSessions, `{"identity":"  Bob@Example.com "}`)
	if w.Code != http.StatusOK || st.revs["bob@example.com"].IsZero() {
		t.Fatalf("kimlik: %d revs=%v", w.Code, st.revs)
	}
	if st.events[1].Reason != accesslog.ReasonSessionRevokedForUser || !strings.Contains(st.events[1].Identity, "bob@example.com") {
		t.Fatalf("kimlik olayi: %+v", st.events[1])
	}
}
