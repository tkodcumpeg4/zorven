package ingress

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestFetchIPListLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tor.txt")
	if err := os.WriteFile(path, []byte("185.220.101.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := fetchIPList(context.Background(), path)
	if err != nil {
		t.Fatalf("yerel dosya okunamadi: %v", err)
	}
	if body != "185.220.101.5\n" {
		t.Fatalf("icerik = %q", body)
	}
	if _, err := fetchIPList(context.Background(), filepath.Join(dir, "yok.txt")); err == nil {
		t.Error("olmayan dosya icin hata beklenirdi")
	}
	if _, err := fetchIPList(context.Background(), "  "); err == nil {
		t.Error("bos kaynak icin hata beklenirdi")
	}
}

func TestFetchIPListHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("10.0.0.0/8\n"))
	}))
	defer srv.Close()

	body, err := fetchIPList(context.Background(), srv.URL+"/ok")
	if err != nil {
		t.Fatalf("indirme basarisiz: %v", err)
	}
	if body != "10.0.0.0/8\n" {
		t.Fatalf("icerik = %q", body)
	}
	if _, err := fetchIPList(context.Background(), srv.URL+"/bad"); err == nil {
		t.Error("HTTP 500 icin hata beklenirdi")
	}
}

// Kaynaklar birlestirilir ve snapshot atomik olarak degisir.
func TestProviderRefreshMergesSources(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	os.WriteFile(a, []byte("10.0.0.0/8\n"), 0o644)
	os.WriteFile(b, []byte("203.0.113.0/24\n"), 0o644)

	p := newIPListProvider("tor", []string{a, b}, "", quietLog())
	if err := p.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if p.size() != 2 {
		t.Fatalf("prefix sayisi = %d, beklenen 2", p.size())
	}
	if !p.contains(mustAddr(t, "10.1.1.1")) || !p.contains(mustAddr(t, "203.0.113.9")) {
		t.Error("her iki kaynaktan gelen adresler bulunmali")
	}
}

// FAIL-STALE: yenileme basarisiz olursa eski snapshot KORUNUR.
func TestProviderRefreshFailKeepsOldSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "list.txt")
	os.WriteFile(src, []byte("10.0.0.0/8\n"), 0o644)

	p := newIPListProvider("hosting", []string{src}, "", quietLog())
	if err := p.refresh(context.Background()); err != nil {
		t.Fatalf("ilk refresh: %v", err)
	}

	// Kaynagi kaldir; yenileme hata vermeli ama kume DEGISMEMELI.
	os.Remove(src)
	if err := p.refresh(context.Background()); err == nil {
		t.Fatal("kaynak kaybolunca hata beklenirdi")
	}
	if !p.contains(mustAddr(t, "10.1.1.1")) {
		t.Error("eski snapshot korunmali (fail-stale)")
	}
	if p.size() != 1 {
		t.Errorf("prefix sayisi = %d, eski snapshot 1 olmali", p.size())
	}
}

// Kaynak okunuyor ama hicbir prefix cozulemiyorsa da eski snapshot korunur:
// bozuk/bos bir yayin listeyi sifirlamamali.
func TestProviderRefreshEmptyParseKeepsOldSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "list.txt")
	os.WriteFile(src, []byte("10.0.0.0/8\n"), 0o644)

	p := newIPListProvider("rep", []string{src}, "", quietLog())
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(src, []byte("# bakim modu\n"), 0o644)
	if err := p.refresh(context.Background()); err == nil {
		t.Fatal("bos ayristirma icin hata beklenirdi")
	}
	if !p.contains(mustAddr(t, "10.1.1.1")) {
		t.Error("bos yayin eski snapshot'i silmemeli")
	}
}

// Disk onbellegi: refresh yazar, loadFromCache ag olmadan geri yukler.
func TestProviderDiskCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	src := filepath.Join(dir, "list.txt")
	os.WriteFile(src, []byte("198.51.100.0/24\n"), 0o644)

	p := newIPListProvider("tor", []string{src}, cache, quietLog())
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Yeni bir saglayici, ULASILAMAZ kaynakla ama ayni onbellekle.
	p2 := newIPListProvider("tor", []string{filepath.Join(dir, "yok.txt")}, cache, quietLog())
	if !p2.loadFromCache() {
		t.Fatal("onbellekten yukleme basarisiz")
	}
	if !p2.contains(mustAddr(t, "198.51.100.5")) {
		t.Error("onbellekten yuklenen kume adresi icermeli")
	}
}

func TestProviderNilSafe(t *testing.T) {
	var p *ipListProvider
	if p.contains(mustAddr(t, "1.2.3.4")) {
		t.Error("nil saglayici hicbir seyi icermemeli")
	}
	if p.size() != 0 {
		t.Error("nil saglayici boyutu 0")
	}
}

func TestIPIntelLookup(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	tor := newIPListProvider("tor", []string{write("tor.txt", "185.220.101.0/24\n")}, "", quietLog())
	host := newIPListProvider("hosting", []string{write("h.txt", "203.0.113.0/24\n")}, "", quietLog())
	rep := newIPListProvider("spamhaus", []string{write("r.txt", "198.51.100.0/24\n")}, "", quietLog())
	for _, p := range []*ipListProvider{tor, host, rep} {
		if err := p.refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	x := &ipIntel{tor: tor, hosting: host, reputation: []*ipListProvider{rep}}

	if got := x.lookup("185.220.101.5"); !got.torExit || got.hosting || len(got.reputation) != 0 {
		t.Errorf("tor adresi = %+v", got)
	}
	if got := x.lookup("203.0.113.5"); got.torExit || !got.hosting {
		t.Errorf("hosting adresi = %+v", got)
	}
	got := x.lookup("198.51.100.5")
	if len(got.reputation) != 1 || got.reputation[0] != "spamhaus" {
		t.Errorf("reputation = %v, beklenen [spamhaus]", got.reputation)
	}
	if got := x.lookup("8.8.8.8"); got.torExit || got.hosting || len(got.reputation) != 0 {
		t.Errorf("temiz adres = %+v", got)
	}
	// Cozulemeyen IP bos sonuc dondurur — kosul eslesmez, deny tetiklenmez.
	if got := x.lookup("bozuk-ip"); got.torExit || got.hosting || len(got.reputation) != 0 {
		t.Errorf("bozuk ip = %+v", got)
	}
	var nilIntel *ipIntel
	if got := nilIntel.lookup("1.2.3.4"); got.torExit || got.hosting {
		t.Error("nil ipIntel bos sonuc dondurmeli")
	}
	if nilIntel.enabled() {
		t.Error("nil ipIntel etkin olmamali")
	}
	if !x.enabled() {
		t.Error("saglayicili ipIntel etkin olmali")
	}
}

func TestSplitSources(t *testing.T) {
	got := SplitSources(" a.txt , , https://x/y ")
	if len(got) != 2 || got[0] != "a.txt" || got[1] != "https://x/y" {
		t.Fatalf("SplitSources = %v", got)
	}
	if SplitSources("") != nil {
		t.Error("bos girdi nil donmeli")
	}
}

func TestParseReputationSources(t *testing.T) {
	got := ParseReputationSources("Spamhaus=https://a/1,abuse=/tmp/x,spamhaus=https://a/2,bozuk,=https://y,ad=")
	if len(got) != 2 {
		t.Fatalf("liste sayisi = %d (%v), beklenen 2", len(got), got)
	}
	if len(got["spamhaus"]) != 2 {
		t.Errorf("spamhaus kaynaklari = %v, beklenen 2 adet", got["spamhaus"])
	}
	if len(got["abuse"]) != 1 {
		t.Errorf("abuse kaynaklari = %v", got["abuse"])
	}
	if ParseReputationSources("") != nil {
		t.Error("bos girdi nil donmeli")
	}
}

func TestIPIntelConfigEmpty(t *testing.T) {
	if !(IPIntelConfig{}).Empty() {
		t.Error("bos yapilandirma Empty olmali")
	}
	if (IPIntelConfig{Tor: []string{"x"}}).Empty() {
		t.Error("Tor tanimliyken Empty olmamali")
	}
	if (IPIntelConfig{Reputation: map[string][]string{"a": {"x"}}}).Empty() {
		t.Error("Reputation tanimliyken Empty olmamali")
	}
}

// SetupIPIntel kaynak yoksa router'a hicbir sey baglamamali.
func TestSetupIPIntelNoSources(t *testing.T) {
	r := &Router{}
	if n := r.SetupIPIntel(context.Background(), IPIntelConfig{}, quietLog()); n != 0 {
		t.Fatalf("saglayici sayisi = %d, beklenen 0", n)
	}
	if r.intel != nil {
		t.Error("kaynak yokken intel nil kalmali")
	}
}

func TestSetupIPIntelWiresProviders(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tor.txt")
	os.WriteFile(src, []byte("185.220.101.0/24\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &Router{}
	cfg := IPIntelConfig{
		Tor:        []string{src},
		Reputation: map[string][]string{"abuse": {src}},
		Refresh:    time.Hour,
	}
	if n := r.SetupIPIntel(ctx, cfg, quietLog()); n != 2 {
		t.Fatalf("saglayici sayisi = %d, beklenen 2", n)
	}
	if r.intel == nil || !r.intel.enabled() {
		t.Fatal("intel router'a baglanmali")
	}
}
