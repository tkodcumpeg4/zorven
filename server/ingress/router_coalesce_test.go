package ingress

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// slowStore: ListHostRoutes yavas (DB tam tablo okumasi gibi) ve yazmalarla eszamanli.
type slowStore struct {
	fakeStore
	mu      sync.Mutex
	hosts   []string
	delay   time.Duration
	entered chan struct{} // ilk yukleme basladiginda bir kez kapanir
	once    sync.Once
}

func (s *slowStore) add(h string) {
	s.mu.Lock()
	s.hosts = append(s.hosts, h)
	s.mu.Unlock()
}

func (s *slowStore) ListHostRoutes(context.Context) ([]store.HostRoute, error) {
	s.mu.Lock()
	snap := append([]string(nil), s.hosts...) // okuma ANI: sonraki yazmalari gormez
	s.mu.Unlock()
	s.once.Do(func() { close(s.entered) })
	time.Sleep(s.delay)
	out := make([]store.HostRoute, 0, len(snap))
	for _, h := range snap {
		out = append(out, store.HostRoute{FQDN: h, TenantID: store.DefaultTenantID, TunnelID: "tun_" + h,
			ClientID: "cli", Target: "http://localhost:1", Enabled: true})
	}
	return out, nil
}

// O4: N esanli Reload en cok 2 tam yuklemeye duser VE her cagiran dondugunde kendi
// degisikligi (Reload'dan once yazilan) yonlendirmede gorunur.
func TestReloadBirlestirirVeGarantiyiKorur(t *testing.T) {
	st := &slowStore{delay: 80 * time.Millisecond, entered: make(chan struct{})}
	r := NewRouter(st)

	// Ilk yukleme suruyor...
	firstDone := make(chan error, 1)
	st.add("ilk.example.com")
	go func() { firstDone <- r.Reload(context.Background()) }()
	<-st.entered

	// ...surerken 20 paralel degisiklik + Reload. Ilk yukleme bunlari GORMEDI
	// (okumasi yazmalardan once yapildi); her biri sonraki yuklemeyi beklemeli.
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := fmt.Sprintf("t%d.example.com", i)
			st.add(h)
			if err := r.Reload(context.Background()); err != nil {
				errs <- err
				return
			}
			if _, ok := r.Lookup(h); !ok {
				errs <- fmt.Errorf("%s Reload dondukten sonra yonlendirmede yok", h)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if n := r.reloadLoadCount.Load(); n != 2 {
		t.Fatalf("21 Reload cagrisi 2 tam yuklemeye dusmeliydi, yapilan %d", n)
	}
	// Ardisik (esanli olmayan) cagrilar her seferinde yukler.
	st.delay = 0
	st.add("son.example.com")
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("son.example.com"); !ok {
		t.Fatal("ardisik Reload degisikligi yansitmadi")
	}
	if n := r.reloadLoadCount.Load(); n != 3 {
		t.Fatalf("ardisik cagri yuklemeliydi: %d", n)
	}
}

// Bekleyen bir cagiranin baglami iptal edilince yalniz o vazgecer; ortak yukleme
// digerleri icin tamamlanir.
func TestReloadBeklemeIptali(t *testing.T) {
	st := &slowStore{delay: 100 * time.Millisecond, entered: make(chan struct{})}
	r := NewRouter(st)
	go func() { _ = r.Reload(context.Background()) }()
	<-st.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := r.Reload(ctx); err == nil {
		t.Fatal("iptal edilen bekleme hata donmeli")
	}
	st.add("x.example.com")
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("x.example.com"); !ok {
		t.Fatal("ortak yukleme iptal yuzunden bozuldu")
	}
}
