package ingress

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// Ham TCP/UDP replika secimi (rawlb.go): HTTP ile AYNI algoritma ve tablolar.

func onlineSet(ids ...string) func(string) bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(c string) bool { return set[c] }
}

func newRawTestRouter(t *testing.T) *Router {
	t.Helper()
	return NewRouter(&fakeStore{})
}

// LB kaydi yokken: cevrimici uyeler arasinda sirali dagitim, cevrimdisi atlanir
// (HTTP'deki orderOnline ile ayni).
func TestOrderClientsWithoutLBRecordRotatesOnline(t *testing.T) {
	r := newRawTestRouter(t)
	cands := []string{"a", "b", "c"}
	if got := r.OrderClients("t1", cands, 1, onlineSet("a", "b", "c")); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("start=1 -> %v", got)
	}
	if got := r.OrderClients("t1", cands, 0, onlineSet("b", "c")); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("a cevrimdisi -> %v", got)
	}
	if got := r.OrderClients("t1", cands, 0, onlineSet()); got != nil {
		t.Fatalf("hic cevrimici yok -> %v; nil beklenirdi", got)
	}
}

// LB kaydi varken HTTP ile ayni orderBackends: agirlikli dagitim 3:1.
func TestOrderClientsUsesTunnelStrategy(t *testing.T) {
	r := newRawTestRouter(t)
	c := cfg(store.LBWeighted, false)
	c.TunnelID = "t1"
	c.Weights = map[string]int{"a": 3, "b": 1}
	r.lbEntries = map[string]*lbEntry{"t1": {cfg: c, clients: []string{"a", "b"}, proto: store.ProtoTCP}}

	count := map[string]int{}
	for s := uint64(0); s < 400; s++ {
		count[r.OrderClients("t1", []string{"a", "b"}, s, onlineSet("a", "b"))[0]]++
	}
	if count["a"] != 300 || count["b"] != 100 {
		t.Fatalf("dagilim = %v; a:300 b:100 beklenirdi", count)
	}
	// Baska bir tunelin kaydi bu tuneli etkilemez: kayitsiz tunel sirali dagitir.
	count = map[string]int{}
	for s := uint64(0); s < 400; s++ {
		count[r.OrderClients("t2", []string{"a", "b"}, s, onlineSet("a", "b"))[0]]++
	}
	if count["a"] != 200 || count["b"] != 200 {
		t.Fatalf("kayitsiz tunel esit dagitmali: %v", count)
	}
}

// Saglik tablosu HTTP ile ortaktir: sagliksiz replika secilmez; hepsi sagliksizsa panik modu.
func TestOrderClientsHonorsHealthAndPanicMode(t *testing.T) {
	r := newRawTestRouter(t)
	c := cfg(store.LBRoundRobin, true)
	c.TunnelID = "t1"
	c.UnhealthyThreshold = 1
	r.lbEntries = map[string]*lbEntry{"t1": {cfg: c, clients: []string{"a", "b"}, proto: store.ProtoTCP}}
	all := onlineSet("a", "b")

	r.lb.record("t1", "a", false, 0, "baglanti reddedildi", c) // esik 1: sagliksiz
	for s := uint64(0); s < 6; s++ {
		if got := r.OrderClients("t1", []string{"a", "b"}, s, all); got[0] != "b" || len(got) != 1 {
			t.Fatalf("sagliksiz a secilmemeli/yedeklenmemeli (start=%d): %v", s, got)
		}
	}
	r.lb.record("t1", "b", false, 0, "baglanti reddedildi", c)
	if got := r.OrderClients("t1", []string{"a", "b"}, 0, all); len(got) != 2 {
		t.Fatalf("panik modunda tum cevrimici adaylar kullanilmali: %v", got)
	}
	// Cevrimdisi olan panik modunda da elenir.
	if got := r.OrderClients("t1", []string{"a", "b"}, 0, onlineSet("b")); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("panik modunda cevrimdisi elenmeli: %v", got)
	}
}

// Acik baglanti/flow sayaci HTTP ile ortaktir (least_connections).
func TestClientBeginEndFeedsLeastConnections(t *testing.T) {
	r := newRawTestRouter(t)
	c := cfg(store.LBLeastConnections, false)
	c.TunnelID = "t1"
	r.lbEntries = map[string]*lbEntry{"t1": {cfg: c, clients: []string{"a", "b"}, proto: store.ProtoTCP}}
	all := onlineSet("a", "b")

	for i := 0; i < 3; i++ {
		r.ClientBegin("a")
	}
	r.ClientBegin("b")
	if got := r.OrderClients("t1", []string{"a", "b"}, 0, all); got[0] != "b" {
		t.Fatalf("en az acik baglantili b secilmeli: %v", got)
	}
	// HTTP tarafinin begin/end'i ayni sayaci kullanir.
	r.lb.begin("b")
	r.lb.begin("b")
	r.lb.begin("b")
	if got := r.OrderClients("t1", []string{"a", "b"}, 0, all); got[0] != "a" {
		t.Fatalf("b artik daha yuklu: %v", got)
	}
	for i := 0; i < 3; i++ {
		r.ClientEnd("a")
	}
	if n := r.lb.inflightOf("a").Load(); n != 0 {
		t.Fatalf("a sayaci sifirlanmali: %d", n)
	}
}

// --- hostname'siz ham tunel LB kayitlari -------------------------------------------------

type rawLBStore struct {
	*fakeStore
	lbs       []store.TunnelLB
	tuns      []store.Tunnel
	reps      map[string][]string
	portErr   error
	portCalls int
	repCalls  int
}

func (f *rawLBStore) ListTunnelLBs(context.Context) ([]store.TunnelLB, error) { return f.lbs, nil }
func (f *rawLBStore) ListReservedPortTunnels(context.Context) ([]store.Tunnel, error) {
	f.portCalls++
	return f.tuns, f.portErr
}
func (f *rawLBStore) ListAllTunnelReplicas(context.Context) (map[string][]string, error) {
	f.repCalls++
	return f.reps, nil
}

func TestReloadBuildsLBEntriesForHostnamelessRawTunnels(t *testing.T) {
	weighted := cfg(store.LBWeighted, true)
	weighted.TunnelID = "tun_raw"
	weighted.Weights = map[string]int{"a": 1, "b": 2}
	httpCfg := cfg(store.LBLatency, true)
	httpCfg.TunnelID = "tun_http"
	ghost := cfg(store.LBRoundRobin, false)
	ghost.TunnelID = "tun_ghost" // ne host route'u ne rezerve portu var

	st := &rawLBStore{
		fakeStore: &fakeStore{routes: []store.HostRoute{{
			FQDN: "web.example.com", TenantID: store.DefaultTenantID, TunnelID: "tun_http",
			ClientID: "h1", ReplicaClientIDs: []string{"h2"}, Proto: store.ProtoHTTP, Enabled: true,
		}}},
		lbs: []store.TunnelLB{weighted, httpCfg, ghost},
		tuns: []store.Tunnel{{ID: "tun_raw", ClientID: "a", Proto: store.ProtoTCP,
			Exposure: store.ExposurePort, PublicPort: 10001, Enabled: true}},
		reps: map[string][]string{"tun_raw": {"b", "a"}}, // birincil tekrari ayiklanir
	}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	raw, ok := r.lbFor("tun_raw")
	if !ok {
		t.Fatal("hostname'siz ham tunel icin LB kaydi kurulmali")
	}
	if !reflect.DeepEqual(raw.clients, []string{"a", "b"}) || raw.proto != store.ProtoTCP || raw.cfg.Strategy != store.LBWeighted {
		t.Fatalf("ham tunel kaydi: %+v", raw)
	}
	web, ok := r.lbFor("tun_http")
	if !ok || !reflect.DeepEqual(web.clients, []string{"h1", "h2"}) {
		t.Fatalf("hostname'li tunel kaydi degismemeli: %+v ok=%v", web, ok)
	}
	if _, ok := r.lbFor("tun_ghost"); ok {
		t.Fatal("ham port tuneli olmayan kayit icin giris kurulmamali")
	}
	// Saglik denetleyicisinin gorunumu de ayni kayitlari icerir.
	if _, ok := r.lbSnapshot()["tun_raw"]; !ok {
		t.Fatal("saglik denetleyicisi hostname'siz ham tuneli de gormeli")
	}
	// Strateji secimi bu kayittan gelir (agirlik 1:2).
	count := map[string]int{}
	for s := uint64(0); s < 300; s++ {
		count[r.OrderClients("tun_raw", []string{"a", "b"}, s, onlineSet("a", "b"))[0]]++
	}
	if count["a"] != 100 || count["b"] != 200 {
		t.Fatalf("agirlikli dagilim: %v", count)
	}
}

// Her LB kaydinin bir host route'u varsa ek sorgu yapilmaz (sicak olmayan yol ama gereksiz is yok).
func TestReloadSkipsRawLookupWhenAllTunnelsHaveRoutes(t *testing.T) {
	c := cfg(store.LBRoundRobin, false)
	c.TunnelID = "tun_http"
	st := &rawLBStore{
		fakeStore: &fakeStore{routes: []store.HostRoute{{
			FQDN: "web.example.com", TenantID: store.DefaultTenantID, TunnelID: "tun_http", ClientID: "h1", Enabled: true,
		}}},
		lbs: []store.TunnelLB{c},
	}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.portCalls != 0 || st.repCalls != 0 {
		t.Fatalf("gereksiz sorgu: port=%d replika=%d", st.portCalls, st.repCalls)
	}
}

// Ham tunel verisi okunamazsa ESKI LB gorunumu korunur (fail-stale).
func TestReloadKeepsLBEntriesWhenRawLookupFails(t *testing.T) {
	c := cfg(store.LBRoundRobin, false)
	c.TunnelID = "tun_raw"
	st := &rawLBStore{
		fakeStore: &fakeStore{},
		lbs:       []store.TunnelLB{c},
		tuns:      []store.Tunnel{{ID: "tun_raw", ClientID: "a", Proto: store.ProtoUDP, Exposure: store.ExposurePort, PublicPort: 10002}},
	}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.lbFor("tun_raw"); !ok {
		t.Fatal("ilk yukleme kaydi kurmali")
	}
	st.portErr = errors.New("gecici veritabani hatasi")
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("LB okuma hatasi tum yeniden yuklemeyi dusurmemeli: %v", err)
	}
	if _, ok := r.lbFor("tun_raw"); !ok {
		t.Fatal("hata aninda eski LB gorunumu korunmali")
	}
}

// --- saglik denetleyicisi ----------------------------------------------------------------

// UDP tuneller yoklanmaz (HTTP yoklamasi anlamsiz); TCP tuneller yoklanir.
func TestHealthCheckerSkipsUDPTunnels(t *testing.T) {
	hc := cfg(store.LBRoundRobin, true)
	hc.IntervalSec = 2
	r := newRawTestRouter(t)
	r.lbEntries = map[string]*lbEntry{
		"t_udp": {cfg: hc, clients: []string{"a", "b"}, proto: store.ProtoUDP},
		"t_tcp": {cfg: hc, clients: []string{"a", "b"}, proto: store.ProtoTCP},
	}
	// Hub bos: yoklama baslatilmaz; yalniz zamanlama kaydi tutulur.
	c := &HealthChecker{Router: r, Hub: tunnel.NewHub(),
		lastRun: map[string]time.Time{}, inFlight: map[string]bool{}}
	c.tick(context.Background(), time.Now())
	if _, ok := c.lastRun["t_udp"]; ok {
		t.Fatal("UDP tunel yoklama zamanlamasina alinmamali")
	}
	if _, ok := c.lastRun["t_tcp"]; !ok {
		t.Fatal("TCP tunel yoklama zamanlamasina alinmali")
	}
}
