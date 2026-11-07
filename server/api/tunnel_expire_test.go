package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// expireStore: TunnelsExpired testi icin bellek ici minimal depo. Supurucu
// satirlari silmis gibi davranmak icin tuneller degistirilebilir.
type expireStore struct {
	store.Store
	tokenID, tokenHash string

	mu      sync.Mutex
	tunnels []store.Tunnel // ListTunnelsByClient'in dondurdugu (silme sonrasi) durum
}

func (m *expireStore) GetClientByTokenID(_ context.Context, id string) (store.Client, error) {
	if id != m.tokenID {
		return store.Client{}, store.ErrNotFound
	}
	return store.Client{ID: "cli_a", TenantID: "ten_a", Name: "pc", TokenID: m.tokenID, TokenHash: m.tokenHash}, nil
}
func (m *expireStore) ListTunnelsByClient(_ context.Context, clientID string) ([]store.Tunnel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Tunnel
	for _, t := range m.tunnels {
		if t.ClientID == clientID {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *expireStore) ListHostnamesByClient(context.Context, string) ([]store.Hostname, error) {
	return nil, nil
}
func (m *expireStore) UpdateDeviceInfo(context.Context, string, store.DeviceInfo) error { return nil }
func (m *expireStore) TouchDeviceSeen(context.Context, string) error                    { return nil }
func (m *expireStore) GetDeviceConfigByClient(context.Context, string) (*protocol.AgentSettings, error) {
	return nil, nil
}

// removeTunnel: supurucunun DELETE'ini taklit eder.
func (m *expireStore) removeTunnel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.tunnels[:0]
	for _, t := range m.tunnels {
		if t.ID != id {
			kept = append(kept, t)
		}
	}
	m.tunnels = kept
}

// expireAgent: tunel Handler'ina WS ile baglanan sahte ajan; aldigi
// config_update mesajlarindaki tunel kimliklerini sirayla kaydeder.
type expireAgent struct {
	mu      sync.Mutex
	updates [][]string
}

func (a *expireAgent) snapshot() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]string(nil), a.updates...)
}

// waitUpdates: en az n config_update gelene dek bekler (zaman asimi: 3 sn).
func (a *expireAgent) waitUpdates(t *testing.T, n int) [][]string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := a.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d config_update bekleniyordu, alinan: %v", n, a.snapshot())
	return nil
}

func newExpireEnv(t *testing.T, tunnels []store.Tunnel) (*Server, *expireStore, *expireAgent, <-chan events.Event, *int) {
	t.Helper()
	full, tokID, hash, err := auth.GenerateClient()
	if err != nil {
		t.Fatal(err)
	}
	st := &expireStore{tokenID: tokID, tokenHash: hash, tunnels: tunnels}
	hub := tunnel.NewHub()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	tsrv := httptest.NewServer(&tunnel.Handler{Store: st, Hub: hub, Log: quiet})
	t.Cleanup(tsrv.Close)

	broker := events.New()
	evCh, unsub := broker.Subscribe()
	t.Cleanup(unsub)
	changed := 0
	srv := &Server{Store: st, Hub: hub, Events: broker, Logger: quiet,
		OnTunnelChange: func() { changed++ }}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(tsrv.URL, "http")+"/",
		&websocket.DialOptions{HTTPHeader: map[string][]string{"Authorization": {"Bearer " + full}}})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(protocol.WSReadLimit)
	t.Cleanup(func() { conn.CloseNow() })
	hello, _ := protocol.Marshal(protocol.Hello{Type: protocol.TypeHello, ClientVersion: "0.0.0-test",
		Platform: "linux/amd64"}, protocol.TypeHello)
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	rctx, rc := context.WithTimeout(ctx, 5*time.Second)
	_, data, err := conn.Read(rctx)
	rc()
	if err != nil {
		t.Fatalf("hello_ack: %v", err)
	}
	if pt, _ := protocol.PeekType(data); pt != protocol.TypeHelloAck {
		t.Fatalf("hello_ack bekleniyordu: %s", pt)
	}

	ag := &expireAgent{}
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			if pt, _ := protocol.PeekType(data); pt != protocol.TypeConfigUpdate {
				continue
			}
			var cu protocol.ConfigUpdate
			if json.Unmarshal(data, &cu) != nil {
				continue
			}
			ids := []string{}
			for _, sp := range cu.Tunnels {
				ids = append(ids, sp.ID)
			}
			ag.mu.Lock()
			ag.updates = append(ag.updates, ids)
			ag.mu.Unlock()
		}
	}()
	// Sunucu oturumu Hub'a kaydolana dek bekle.
	for i := 0; i < 200; i++ {
		if _, ok := hub.Get("cli_a"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return srv, st, ag, evCh, &changed
}

func nextEvent(t *testing.T, ch <-chan events.Event) events.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("olay bekleniyordu")
		return events.Event{}
	}
}

// Supurucunun sildigi gecici tunel icin panelden silmedeki yan etkiler olusur:
// yonlendirme tazelenir, kiraciya tunnel.deleted olayi gider ve tunelin
// istemcisine tunelsiz guncel liste config_update ile itilir.
func TestTunnelsExpiredPublishesEventAndPushesConfig(t *testing.T) {
	exp := store.Tunnel{ID: "tun_exp", TenantID: "ten_a", ClientID: "cli_a", Target: "http://localhost:3000", Enabled: true, Ephemeral: true}
	keep := store.Tunnel{ID: "tun_keep", TenantID: "ten_a", ClientID: "cli_a", Target: "http://localhost:8080", Enabled: true}
	srv, st, ag, evCh, changed := newExpireEnv(t, []store.Tunnel{exp, keep})

	st.removeTunnel("tun_exp") // supurucu satiri sildi
	srv.TunnelsExpired([]store.Tunnel{exp})

	if *changed != 1 {
		t.Fatalf("yonlendirme bir kez tazelenmeli, alinan %d", *changed)
	}
	ev := nextEvent(t, evCh)
	if ev.Type != events.TypeTunnelDeleted || ev.Tenant != "ten_a" {
		t.Fatalf("tunnel.deleted/ten_a bekleniyordu: %+v", ev)
	}
	if m, ok := ev.Data.(map[string]string); !ok || m["id"] != "tun_exp" {
		t.Fatalf("olay verisi {id: tun_exp} olmali: %#v", ev.Data)
	}
	ups := ag.waitUpdates(t, 1)
	if len(ups[0]) != 1 || ups[0][0] != "tun_keep" {
		t.Fatalf("config_update yalniz kalan tuneli icermeli: %v", ups)
	}
}

// Ayni istemcinin ayni turda dolan birden cok tuneli tek config_update ile
// itilir; her tunel icin ayri olay yayilir ve yonlendirme bir kez tazelenir.
func TestTunnelsExpiredCoalescesPerClient(t *testing.T) {
	e1 := store.Tunnel{ID: "tun_e1", TenantID: "ten_a", ClientID: "cli_a", Enabled: true, Ephemeral: true}
	e2 := store.Tunnel{ID: "tun_e2", TenantID: "ten_a", ClientID: "cli_a", Enabled: true, Ephemeral: true}
	srv, st, ag, evCh, changed := newExpireEnv(t, []store.Tunnel{e1, e2})

	st.removeTunnel("tun_e1")
	st.removeTunnel("tun_e2")
	srv.TunnelsExpired([]store.Tunnel{e1, e2})

	if *changed != 1 {
		t.Fatalf("yonlendirme bir kez tazelenmeli, alinan %d", *changed)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev := nextEvent(t, evCh)
		if ev.Type != events.TypeTunnelDeleted {
			t.Fatalf("tunnel.deleted bekleniyordu: %+v", ev)
		}
		got[ev.Data.(map[string]string)["id"]] = true
	}
	if !got["tun_e1"] || !got["tun_e2"] {
		t.Fatalf("iki tunel icin de olay bekleniyordu: %v", got)
	}
	ups := ag.waitUpdates(t, 1)
	time.Sleep(100 * time.Millisecond) // ikinci bir itme olsaydi gelirdi
	if all := ag.snapshot(); len(all) != 1 || len(ups[0]) != 0 {
		t.Fatalf("tek ve bos tunel listeli config_update bekleniyordu: %v", all)
	}
}

// Bos liste (bos supurme turu) hicbir yan etki uretmez.
func TestTunnelsExpiredEmptyIsNoop(t *testing.T) {
	keep := store.Tunnel{ID: "tun_keep", TenantID: "ten_a", ClientID: "cli_a", Enabled: true}
	srv, _, ag, evCh, changed := newExpireEnv(t, []store.Tunnel{keep})

	srv.TunnelsExpired(nil)

	if *changed != 0 {
		t.Fatalf("bos turda yonlendirme tazelenmemeli: %d", *changed)
	}
	select {
	case ev := <-evCh:
		t.Fatalf("olay beklenmiyordu: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	if got := ag.snapshot(); len(got) != 0 {
		t.Fatalf("config_update beklenmiyordu: %v", got)
	}
}

// Istemci bagli degilse (ya da Hub/Events yoksa) panik olmaz; olay yine de gider.
func TestTunnelsExpiredOfflineClientStillPublishes(t *testing.T) {
	broker := events.New()
	evCh, unsub := broker.Subscribe()
	defer unsub()
	changed := 0
	srv := &Server{Events: broker, Hub: tunnel.NewHub(), OnTunnelChange: func() { changed++ }}

	srv.TunnelsExpired([]store.Tunnel{{ID: "tun_x", TenantID: "ten_b", ClientID: "cli_offline"}})

	if changed != 1 {
		t.Fatalf("yonlendirme tazelenmeli: %d", changed)
	}
	if ev := nextEvent(t, evCh); ev.Type != events.TypeTunnelDeleted || ev.Tenant != "ten_b" {
		t.Fatalf("tunnel.deleted/ten_b bekleniyordu: %+v", ev)
	}

	// Events ve Hub nil: yine panik yok.
	bare := &Server{}
	bare.TunnelsExpired([]store.Tunnel{{ID: "tun_y", TenantID: "ten_b", ClientID: "cli_offline"}})
}
