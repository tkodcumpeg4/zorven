package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// replicaStore, replika kaldirma cagrilarini kaydeder (acik surum: kapali Apps
// test deposu yerine store.Store gomulu en kucuk sahte).
type replicaStore struct {
	store.Store
	clients map[string]store.Client
	removed []string
}

func (s *replicaStore) GetClient(_ context.Context, tenantID, id string) (store.Client, error) {
	c, ok := s.clients[id]
	if !ok || c.TenantID != tenantID {
		return store.Client{}, store.ErrNotFound
	}
	return c, nil
}

func (s *replicaStore) RemoveTunnelReplica(_ context.Context, tenantID, tunnelID, clientID string) error {
	s.removed = append(s.removed, tenantID+"/"+tunnelID+"/"+clientID)
	return nil
}

// Baska kiracinin istemci kimligiyle kendi tunelinden replika kaldirmak 404
// olmali ve depoya (ve dolayisiyla o istemciye config_update itmesine) hic
// ulasmamali. Onceden 200 donup yoldaki yabanci istemciye itme tetikleniyordu.
func TestRemoveTunnelReplicaForeignClient(t *testing.T) {
	st := &replicaStore{clients: map[string]store.Client{
		"cli_own":   {ID: "cli_own", TenantID: "ten_1", Name: "own"},
		"cli_other": {ID: "cli_other", TenantID: "ten_2", Name: "other"},
	}}
	srv := &Server{Store: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Tickets: NewTicketStore()}
	do := func(clientID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "/api/v1/tunnels/tun_1/replicas/"+clientID, nil)
		r.SetPathValue("id", "tun_1")
		r.SetPathValue("clientID", clientID)
		r = r.WithContext(withUser(withTenant(r.Context(), "ten_1"),
			&AuthUser{ID: "u1", TenantID: "ten_1", Role: "owner"}))
		w := httptest.NewRecorder()
		srv.removeTunnelReplica(w, r)
		return w
	}

	if w := do("cli_other"); w.Code != http.StatusNotFound {
		t.Fatalf("yabanci istemci: beklenen 404, gelen %d %s", w.Code, w.Body.String())
	}
	if len(st.removed) != 0 {
		t.Fatalf("yabanci istemci icin depo cagrildi: %v", st.removed)
	}
	if w := do("cli_own"); w.Code != http.StatusOK {
		t.Fatalf("kendi istemcisi: beklenen 200, gelen %d %s", w.Code, w.Body.String())
	}
	if len(st.removed) != 1 || st.removed[0] != "ten_1/tun_1/cli_own" {
		t.Fatalf("kaldirma cagrisi beklenmiyor: %v", st.removed)
	}
}
