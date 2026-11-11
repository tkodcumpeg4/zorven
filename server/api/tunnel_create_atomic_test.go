package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// tunCreateStore: createTunnel'in ihtiyac duydugu depo yuzeyini bellekte taklit
// eder. Ad cakismasi / DB hatasi enjekte edilebilir.
type tunCreateStore struct {
	store.Store

	mu       sync.Mutex
	n        int
	tunnels  map[string]store.Tunnel
	created  int      // olusturulan tunel sayisi (silinenler dahil)
	deleted  []string // silinen tunel kimlikleri
	taken    map[string]bool
	hostAdds int // AddHostnameWithProject cagri sayisi
	addErr   error
	attached map[string]string // hostname id -> tunel id
	hosts    map[string]store.Hostname
	attachEr error

	// onAdd, AddHostnameWithProject icinde calisir (istek iptali taklidi icin).
	onAdd func()
}

func newTunCreateStore() *tunCreateStore {
	return &tunCreateStore{
		tunnels:  map[string]store.Tunnel{},
		taken:    map[string]bool{},
		attached: map[string]string{},
		hosts:    map[string]store.Hostname{},
	}
}

func (f *tunCreateStore) GetClient(_ context.Context, tenantID, id string) (store.Client, error) {
	if id != "cli_1" {
		return store.Client{}, store.ErrNotFound
	}
	return store.Client{ID: id, TenantID: tenantID}, nil
}
func (f *tunCreateStore) GetTenant(context.Context, string) (store.Tenant, error) {
	return store.Tenant{ID: "ten_1", Slug: "acme"}, nil
}
func (f *tunCreateStore) add(tenantID, clientID, target string, ephemeral bool) store.Tunnel {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.created++
	t := store.Tunnel{ID: fmt.Sprintf("tun_%d", f.n), TenantID: tenantID, ClientID: clientID,
		Target: target, Enabled: true, Ephemeral: ephemeral, Proto: store.ProtoHTTP}
	f.tunnels[t.ID] = t
	return t
}
func (f *tunCreateStore) CreateTunnelWithProject(_ context.Context, tenantID, clientID, target, _ string) (store.Tunnel, error) {
	return f.add(tenantID, clientID, target, false), nil
}
func (f *tunCreateStore) CreateEphemeralTunnel(_ context.Context, tenantID, clientID, target, _ string, _ time.Time) (store.Tunnel, error) {
	return f.add(tenantID, clientID, target, true), nil
}
func (f *tunCreateStore) DeleteTunnel(ctx context.Context, _, id string) error {
	// Iptal edilmis baglamla silme gercek depoda da basarisiz olurdu.
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.tunnels[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.tunnels, id)
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *tunCreateStore) AddHostnameWithProject(_ context.Context, tenantID, tunnelID, fqdn, typ, _ string) (store.Hostname, error) {
	f.mu.Lock()
	f.hostAdds++
	taken, addErr, onAdd := f.taken[fqdn], f.addErr, f.onAdd
	f.mu.Unlock()
	if onAdd != nil {
		onAdd()
	}
	if addErr != nil {
		return store.Hostname{}, addErr
	}
	if taken {
		return store.Hostname{}, store.ErrHostnameTaken
	}
	return store.Hostname{ID: "hst_new", TenantID: tenantID, TunnelID: tunnelID, FQDN: fqdn, Type: typ}, nil
}
func (f *tunCreateStore) GetHostnameByID(_ context.Context, _, id string) (store.Hostname, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hosts[id]
	if !ok {
		return store.Hostname{}, store.ErrNotFound
	}
	return h, nil
}
func (f *tunCreateStore) AttachHostname(_ context.Context, _, hostnameID, tunnelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attachEr != nil {
		return f.attachEr
	}
	f.attached[hostnameID] = tunnelID
	return nil
}

// GetHostnameByFQDN: alinmis adlar baska kiraciya/tunele ait sayilir (bos ad yeniden
// kullanimi, A4 #15, bu sahtede tetiklenmez; Postgres'li testte ayrica denenir).
func (f *tunCreateStore) GetHostnameByFQDN(context.Context, string) (store.Hostname, error) {
	return store.Hostname{}, store.ErrNotFound
}

func (f *tunCreateStore) tunnelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tunnels)
}

type tunCreateEnv struct {
	s       *Server
	st      *tunCreateStore
	evCh    <-chan events.Event
	changed *int
}

func newTunCreateEnv(t *testing.T) *tunCreateEnv {
	t.Helper()
	st := newTunCreateStore()
	broker := events.New()
	evCh, unsub := broker.Subscribe()
	t.Cleanup(unsub)
	changed := 0
	s := &Server{
		Store: st, PlatformDomain: "zorven.app", Events: broker,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnTunnelChange: func() { changed++ },
	}
	return &tunCreateEnv{s: s, st: st, evCh: evCh, changed: &changed}
}

func (e *tunCreateEnv) post(body string) *httptest.ResponseRecorder {
	return e.postCtx(context.Background(), body)
}

func (e *tunCreateEnv) postCtx(ctx context.Context, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/tunnels", strings.NewReader(body)).WithContext(withTenant(ctx, "ten_1"))
	w := httptest.NewRecorder()
	e.s.createTunnel(w, r)
	return w
}

// noEvent: tunnel.created gibi bir olay yayilmadiysa true.
func (e *tunCreateEnv) noEvent() bool {
	select {
	case <-e.evCh:
		return false
	case <-time.After(50 * time.Millisecond):
		return true
	}
}

const tunBody = `{"name":"api","client_id":"cli_1","target":"http://localhost:3000"}`

// Ad cakismasi: tunel olusmaz (olusup geri alinir), 409 hostname_taken doner,
// yonlendirme tazelenmez ve tunnel.created olayi yayilmaz.
func TestCreateTunnelNameTakenRollsBack(t *testing.T) {
	for name, body := range map[string]string{
		"kalici": tunBody,
		"gecici": `{"name":"api","client_id":"cli_1","target":"http://localhost:3000","ttl_sec":120}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newTunCreateEnv(t)
			e.st.taken["api--acme.zorven.app"] = true

			w := e.post(body)
			if w.Code != http.StatusConflict || errCode(t, w) != "hostname_taken" {
				t.Fatalf("409 hostname_taken bekleniyordu: %d %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "api--acme.zorven.app") {
				t.Fatalf("hata metni alinmis adi icermeli: %s", w.Body.String())
			}
			if e.st.tunnelCount() != 0 {
				t.Fatalf("adressiz tunel kalmamali: %d tunel var", e.st.tunnelCount())
			}
			if e.st.created != 1 || len(e.st.deleted) != 1 {
				t.Fatalf("olusan tunel geri alinmali: created=%d deleted=%v", e.st.created, e.st.deleted)
			}
			if *e.changed != 0 || !e.noEvent() {
				t.Fatalf("basarisiz olusturma yan etki yaymamali (changed=%d)", *e.changed)
			}
		})
	}
}

// Ad baska bir istekle ayni anda alinirsa (AddHostname sirasinda) istek iptal
// edilmis olsa bile geri alma tamamlanir.
func TestCreateTunnelRollbackSurvivesCanceledRequest(t *testing.T) {
	e := newTunCreateEnv(t)
	e.st.taken["api--acme.zorven.app"] = true
	ctx, cancel := context.WithCancel(context.Background())
	e.st.onAdd = cancel // istemci baglantiyi hostname eklenirken kapatti

	w := e.postCtx(ctx, tunBody)
	if w.Code != http.StatusConflict {
		t.Fatalf("409 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	if e.st.tunnelCount() != 0 {
		t.Fatal("iptal edilmis istekte de tunel geri alinmali")
	}
}

// Ad tunelle birlikte olusur; tunnel.created olayi yayilir.
func TestCreateTunnelAssignsScopedHostname(t *testing.T) {
	e := newTunCreateEnv(t)
	w := e.post(tunBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("201 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	var out store.Tunnel
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hostnames) != 1 || out.Hostnames[0].FQDN != "api--acme.zorven.app" || out.Hostnames[0].Type != store.HostTypeScoped {
		t.Fatalf("kapsamli ad atanmali: %+v", out.Hostnames)
	}
	if e.st.tunnelCount() != 1 || len(e.st.deleted) != 0 {
		t.Fatalf("tunel kalici olmali: %d tunel, silinen %v", e.st.tunnelCount(), e.st.deleted)
	}
	if *e.changed != 1 {
		t.Fatalf("yonlendirme tazelenmeli: %d", *e.changed)
	}
	if ev := nextEvent(t, e.evCh); ev.Type != events.TypeTunnelCreated {
		t.Fatalf("tunnel.created bekleniyordu: %+v", ev)
	}
}

// no_domain=true akisi etkilenmez: ad aranmaz, cakisan ad bile sorun degil, tunel adressiz acilir.
func TestCreateTunnelNoDomainUnaffected(t *testing.T) {
	e := newTunCreateEnv(t)
	e.st.taken["api--acme.zorven.app"] = true
	w := e.post(`{"name":"api","client_id":"cli_1","target":"http://localhost:3000","no_domain":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("no_domain 201 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	var out store.Tunnel
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Hostnames) != 0 {
		t.Fatalf("ad atanmamali: %+v", out.Hostnames)
	}
	if e.st.hostAdds != 0 || e.st.tunnelCount() != 1 {
		t.Fatalf("hostname eklenmemeli, tunel kalmali: adds=%d tunnels=%d", e.st.hostAdds, e.st.tunnelCount())
	}
}

// Cakisma disi bir depo hatasinda da tunel adressiz kalmaz: geri alinir, 500 doner.
func TestCreateTunnelHostnameStoreErrorRollsBack(t *testing.T) {
	e := newTunCreateEnv(t)
	e.st.addErr = errors.New("db down")
	w := e.post(tunBody)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("500 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	if e.st.tunnelCount() != 0 {
		t.Fatal("tunel geri alinmali")
	}
	if *e.changed != 0 || !e.noEvent() {
		t.Fatal("basarisiz olusturma yan etki yaymamali")
	}
}

// hostname_id: bulunamayan ad tunel acilmadan 422; bulunan ad tunele baglanir.
func TestCreateTunnelHostnameID(t *testing.T) {
	e := newTunCreateEnv(t)
	w := e.post(`{"name":"api","client_id":"cli_1","target":"http://localhost:3000","hostname_id":"hst_yok"}`)
	if w.Code != http.StatusUnprocessableEntity || errCode(t, w) != "hostname_not_found" {
		t.Fatalf("422 hostname_not_found bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	if e.st.created != 0 {
		t.Fatalf("bulunamayan adla tunel acilmamali: created=%d", e.st.created)
	}

	e.st.hosts["hst_1"] = store.Hostname{ID: "hst_1", TenantID: "ten_1", FQDN: "shop.example.com", Type: store.HostTypeCustom, Verified: true}
	w = e.post(`{"name":"api","client_id":"cli_1","target":"http://localhost:3000","hostname_id":"hst_1"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("201 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	var out store.Tunnel
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Hostnames) != 1 || out.Hostnames[0].ID != "hst_1" || out.Hostnames[0].TunnelID != out.ID {
		t.Fatalf("secilen ad tunele baglanmali: %+v", out)
	}
	if e.st.attached["hst_1"] != out.ID {
		t.Fatalf("ad depoda tunele baglanmali: %v", e.st.attached)
	}
	if e.st.hostAdds != 0 {
		t.Fatal("hostname_id verilince otomatik ad eklenmemeli")
	}

	// Baglama hatasi: tunel geri alinir.
	e2 := newTunCreateEnv(t)
	e2.st.hosts["hst_1"] = store.Hostname{ID: "hst_1", TenantID: "ten_1", FQDN: "shop.example.com"}
	e2.st.attachEr = errors.New("db down")
	w = e2.post(`{"name":"api","client_id":"cli_1","target":"http://localhost:3000","hostname_id":"hst_1"}`)
	if w.Code != http.StatusInternalServerError || e2.st.tunnelCount() != 0 {
		t.Fatalf("baglama hatasinda 500 ve geri alma bekleniyordu: %d tunnels=%d", w.Code, e2.st.tunnelCount())
	}
}

// Platform alani yoksa (PlatformDomain bos) eski davranis: otomatik ad denenmez,
// tunel adressiz acilir.
func TestCreateTunnelWithoutPlatformDomainSkipsHostname(t *testing.T) {
	e := newTunCreateEnv(t)
	e.s.PlatformDomain = ""
	w := e.post(tunBody)
	if w.Code != http.StatusCreated || e.st.hostAdds != 0 {
		t.Fatalf("201 ve ad denemesi yok bekleniyordu: %d adds=%d", w.Code, e.st.hostAdds)
	}
}
