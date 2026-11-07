package agent

import (
	"context"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// 60 fps (screen2_fps60): sunucu onaylarsa tavan 60'a cikar; onaylamazsa (eski
// sunucu) 30'da kalir. Uyarlama (yazma suresi, sunucu geri bildirimi) tavanin
// ALTINDA ayni kurallarla calisir.

func TestHelloAdvertisesFPS60(t *testing.T) {
	found := false
	for _, f := range (&Agent{}).helloFeatures() {
		found = found || f == protocol.FeatureScreen2FPS60
	}
	if !found {
		t.Fatal("ajan screen2_fps60 yetenegini ilan etmeli")
	}
}

// Ajan 30 ustune YALNIZ sunucu hello_ack'te screen2_fps60'i yansitirsa cikar;
// eski sunucu yansitmaz ve ajan 30'da kalir. Hello'da yetenek ilan edilir.
func TestHandshakeScreen2FPS60FromAck(t *testing.T) {
	for _, c := range []struct {
		name string
		ack  []string
		want bool
	}{
		{"sunucu onayladi", []string{protocol.FeatureScreen2, protocol.FeatureScreen2Layers, protocol.FeatureScreen2FPS60}, true},
		{"eski sunucu (onay yok)", []string{protocol.FeatureScreen2, protocol.FeatureScreen2Layers}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			hello := make(chan protocol.Hello, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				defer cancel()
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var h protocol.Hello
				_ = json.Unmarshal(data, &h)
				hello <- h
				ack, _ := protocol.Marshal(protocol.HelloAck{
					Type: protocol.TypeHelloAck, ClientID: "c1", SessionID: "s1", HeartbeatIntervalS: 30, Features: c.ack,
				}, protocol.TypeHelloAck)
				_ = conn.Write(ctx, websocket.MessageText, ack)
				time.Sleep(150 * time.Millisecond)
				conn.Close(websocket.StatusNormalClosure, "bye")
			}))
			defer srv.Close()
			a := testAgent(srv)
			a.NoAutoUpdate = true
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = a.connectOnce(ctx) // baglanti kapaninca doner; bayraklar el sikismada atandi
			select {
			case h := <-hello:
				found := false
				for _, f := range h.Features {
					found = found || f == protocol.FeatureScreen2FPS60
				}
				if !found {
					t.Fatalf("hello'da screen2_fps60 yok: %v", h.Features)
				}
			default:
				t.Fatal("hello alinamadi")
			}
			if a.screen2FPS60 != c.want {
				t.Fatalf("screen2FPS60=%v, beklenen %v", a.screen2FPS60, c.want)
			}
		})
	}
}

// screen_open -> oturum parametreleri: 30 ustu tavan yalniz pazarlandiysa gecer;
// katmanlar yalniz katman yetenegi pazarlandiysa.
func TestScreenOpenParamsWireFPS60(t *testing.T) {
	layers := []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 1600, Quality: 80}}
	msg := protocol.ScreenOpen{FPS: 60, Quality: 70, MaxWidth: 1280, Layers: layers}
	cases := []struct {
		name        string
		fps60, lyrs bool
	}{
		{"hicbiri", false, false}, {"yalniz fps60", true, false}, {"yalniz katman", false, true}, {"ikisi", true, true},
	}
	for _, c := range cases {
		sm := &screenManager{cs: &clientSession{screen2FPS60: c.fps60, screen2Layers: c.lyrs}}
		p := sm.screen2ParamsFor(msg, 2)
		if p.HighFPS != c.fps60 || (len(p.Layers) > 0) != c.lyrs || p.FPS != 60 || p.Display != 2 || p.MaxWidth != 1280 {
			t.Errorf("%s: %+v", c.name, p)
		}
		s := newScreen2Session("x", 1, &fakeCap{img: image.NewRGBA(image.Rect(0, 0, 64, 64))}, newGoTileEncoder(),
			func(context.Context, []byte) error { return nil }, p)
		want := 30
		if c.fps60 {
			want = 60
		}
		if s.ctl.FPS() != want || s.maxFPS() != want {
			t.Errorf("%s: oturum fps=%d tavan=%d, beklenen %d", c.name, s.ctl.FPS(), s.maxFPS(), want)
		}
	}
}

func TestScreen2CapsMatchProtocol(t *testing.T) {
	if screen2MaxFPS != 30 || screen2HighFPS != 60 {
		t.Fatalf("tavanlar: %d / %d", screen2MaxFPS, screen2HighFPS)
	}
}

func TestLegacyFPS(t *testing.T) {
	cases := []struct {
		req  int
		high bool
		want int
	}{
		{0, false, defaultFPS}, {0, true, defaultFPS}, {-4, true, defaultFPS},
		{15, false, 15}, {30, false, 30}, {31, false, defaultFPS}, {45, false, defaultFPS}, {60, false, defaultFPS},
		{30, true, 30}, {45, true, 45}, {60, true, 60}, {61, true, defaultFPS}, {1000, true, defaultFPS},
	}
	for _, c := range cases {
		if got := legacyFPS(c.req, c.high); got != c.want {
			t.Errorf("legacyFPS(%d, high=%v)=%d, beklenen %d", c.req, c.high, got, c.want)
		}
	}
}

// Oturum tavani: onay yoksa 30, varsa 60; varsayilan (istek yok) 15; canli ayar da kirpilir.
func TestScreen2SessionFPSCap(t *testing.T) {
	mk := func(high bool, fps int) *screen2Session {
		return newScreen2Session("x", 1, &fakeCap{img: image.NewRGBA(image.Rect(0, 0, 64, 64))}, newGoTileEncoder(),
			func(context.Context, []byte) error { return nil }, screen2Params{FPS: fps, HighFPS: high})
	}
	cases := []struct {
		name      string
		high      bool
		fps       int
		wantStart int
		wantMax   int
	}{
		{"onay yok, 60 istenir", false, 60, 30, 30},
		{"onay yok, 30 istenir", false, 30, 30, 30},
		{"onay var, 60 istenir", true, 60, 60, 60},
		{"onay var, 45 istenir", true, 45, 45, 60},
		{"onay var, asiri istek", true, 500, 60, 60},
		{"onay var, istek yok", true, 0, 15, 60},
		{"onay yok, istek yok", false, 0, 15, 30},
	}
	for _, c := range cases {
		s := mk(c.high, c.fps)
		if s.ctl.FPS() != c.wantStart || s.maxFPS() != c.wantMax {
			t.Errorf("%s: baslangic fps=%d tavan=%d, beklenen %d / %d", c.name, s.ctl.FPS(), s.maxFPS(), c.wantStart, c.wantMax)
		}
		// Canli ayar: tavanin ustune cikmaz, altini aynen alir.
		s.applyParams(screen2Params{FPS: 90, Display: -1})
		if got := s.ctl.MaxFPS(); got != c.wantMax {
			t.Errorf("%s: ayar 90 -> tavan %d, beklenen %d", c.name, got, c.wantMax)
		}
		s.applyParams(screen2Params{FPS: 20, Display: -1})
		if got := s.ctl.MaxFPS(); got != 20 {
			t.Errorf("%s: ayar 20 -> tavan %d", c.name, got)
		}
		if c.high {
			s.applyParams(screen2Params{FPS: 45, Display: -1})
			if got := s.ctl.MaxFPS(); got != 45 {
				t.Errorf("%s: ayar 45 -> tavan %d", c.name, got)
			}
		}
	}
	// Elle kurulmus oturum (tavan atanmamis) eski davranisi korur.
	if (&screen2Session{}).maxFPS() != 30 {
		t.Fatal("tavansiz oturum 30 olmali")
	}
}

// Yazma suresi uyarlamasi 60 tavaninda da ayni kurallarla calisir.
func TestAdaptCtlSixtyFPS(t *testing.T) {
	a := newAdaptCtl(100, 60)
	if a.FPS() != 60 || a.Interval() != time.Second/60 {
		t.Fatalf("baslangic: fps=%d aralik=%v", a.FPS(), a.Interval())
	}
	iv := a.Interval()
	// tek yavas kare dusurmez; art arda iki yavas kare dusurur: fps*3/4 = 45, kalite -10
	if a.Observe(iv + iv/4) {
		t.Fatal("tek yavas kare dusurmemeli")
	}
	if !a.Observe(iv+iv/4) || a.FPS() != 45 || a.Quality() != 90 {
		t.Fatalf("iki yavas kare: fps=%d q=%d", a.FPS(), a.Quality())
	}
	// siddetli (>3 aralik) tek karede hemen: 45 -> 33 -> 24 ... tabana (3) iner
	want := []int{33, 24, 18, 13, 9, 6, 4, 3, 3}
	for i, w := range want {
		a.Observe(4 * a.Interval())
		if a.FPS() != w {
			t.Fatalf("adim %d: fps=%d, beklenen %d", i, a.FPS(), w)
		}
	}
	// rahat kareler: her screen2RaiseAfter karede +3, tavan 60'i asmaz
	for i := 0; i < screen2RaiseAfter; i++ {
		a.Observe(time.Microsecond)
	}
	if a.FPS() != 3+screen2RaiseFPS {
		t.Fatalf("ilk yukselis: fps=%d", a.FPS())
	}
	for i := 0; i < 5000; i++ {
		a.Observe(time.Microsecond)
	}
	if a.FPS() != 60 || a.Quality() != 100 {
		t.Fatalf("tavana donmedi: fps=%d q=%d", a.FPS(), a.Quality())
	}
	// Tavan 60'tan 40'a cekilirse kirpilir.
	a.SetMax(100, 40)
	if a.FPS() != 40 {
		t.Fatalf("kirpma: fps=%d", a.FPS())
	}
}

// Sunucu geri bildirimi 60 tavaninda: yavas izleyiciye gore hemen iner, tikanmasiz
// pencerelerde +2/sn ile yukselir ve tavana ulasinca kisit tamamen kalkar.
func TestAdaptFeedbackSixtyFPS(t *testing.T) {
	a := newAdaptCtl(100, 60)
	now := time.Now()
	if a.LayerFPS(0, now) != 60 {
		t.Fatal("geri bildirimsiz katman tavanda olmali")
	}
	a.Feedback(fbLayer(0, 20, 5, 0), 500, now) // 20*1.2+1 = 25
	if got := a.LayerFPS(0, now); got != 25 {
		t.Fatalf("yavas izleyici: fps=%d, beklenen 25", got)
	}
	if iv := a.LayerInterval(0, now); iv != time.Second/25 {
		t.Fatalf("aralik=%v", iv)
	}
	// Tikanmasiz her 1000 ms'de +2; kisit 60'a ulasinca kalkar (25 + 2*18 = 61).
	for i := 0; i < 17; i++ {
		now = now.Add(time.Second)
		a.Feedback(fbLayer(0, 60, 0, 0), 1000, now)
	}
	if got := a.LayerFPS(0, now); got != 59 {
		t.Fatalf("17 pencere sonra fps=%d, beklenen 59", got)
	}
	now = now.Add(time.Second)
	a.Feedback(fbLayer(0, 60, 0, 0), 1000, now)
	if got := a.LayerFPS(0, now); got != 60 {
		t.Fatalf("kisit kalkmadi: fps=%d", got)
	}
	// Tavanin ustunde bildirilen teslim hizi tavana kirpilir.
	b := newAdaptCtl(100, 60)
	b.Feedback(fbLayer(0, 200, 3, 0), 500, now)
	if got := b.LayerFPS(0, now); got != 60 {
		t.Fatalf("tavan: %d", got)
	}
}

// Katman yakalama araligi 60'ta ~16.7 ms; onay yoksa istek 60 olsa bile 33.3 ms.
func TestScreen2PaceAtSixtyFPS(t *testing.T) {
	layers := []*layerState{{spec: protocol.Screen2LayerSpec{ID: 0}}}
	for _, c := range []struct {
		high bool
		want time.Duration
	}{{true, time.Second / 60}, {false, time.Second / 30}} {
		s := newScreen2Session("x", 1, &fakeCap{img: image.NewRGBA(image.Rect(0, 0, 64, 64))}, newGoTileEncoder(),
			func(context.Context, []byte) error { return nil }, screen2Params{FPS: 60, HighFPS: c.high})
		if got := s.pace(layers, time.Now()); got != c.want {
			t.Errorf("high=%v pace=%v, beklenen %v", c.high, got, c.want)
		}
	}
}

// Yavas yazma (tikanan ag) 60 tavanli oturumu da asagi ceker: hedef fps, yazmanin
// kaldirabildigi hizin civarina iner (60'ta takili kalmaz).
func TestScreen2SlowWriteAdaptsDownFromSixty(t *testing.T) {
	if testing.Short() {
		t.Skip("zamanlamali test")
	}
	src := image.NewRGBA(image.Rect(0, 0, 320, 192))
	fill(src, src.Rect, 40, 40, 40)
	fc := &fakeCap{img: src}
	var sent int
	var smu sync.Mutex
	slow := func(ctx context.Context, b []byte) error {
		select {
		case <-time.After(40 * time.Millisecond): // ~25 kare/sn kaldiran ag
		case <-ctx.Done():
			return ctx.Err()
		}
		smu.Lock()
		sent++
		smu.Unlock()
		return nil
	}
	s := newScreen2Session("scr_s", 4, fc, newGoTileEncoder(), slow,
		screen2Params{FPS: 60, Quality: 70, MaxWidth: 4000, HighFPS: true})
	if s.ctl.FPS() != 60 {
		t.Fatalf("baslangic fps=%d", s.ctl.FPS())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	stop := make(chan struct{})
	go func() {
		c := uint8(0)
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			c += 5
			fc.mu.Lock()
			fill(fc.img, image.Rect(0, 0, 50, 50), c, 0, 0)
			fc.mu.Unlock()
		}
	}()
	time.Sleep(2500 * time.Millisecond)
	close(stop)
	got := s.ctl.FPS()
	cancel()
	<-done
	if got >= 45 || got < screen2MinFPS {
		t.Fatalf("yavas yazmada hedef fps %d, 60'tan belirgin sekilde inmeliydi", got)
	}
	if s.ctl.MaxFPS() != 60 {
		t.Fatalf("kullanici tavani degismemeli: %d", s.ctl.MaxFPS())
	}
}

// Uctan uca dongu: surekli degisen ekranda 60 tavanli oturum, 30 tavanli oturumdan
// belirgin sekilde fazla kare uretir (yakalama dongusunde 30'luk gizli kesme yok).
func TestScreen2LoopReachesAboveThirtyFPS(t *testing.T) {
	if testing.Short() {
		t.Skip("zamanlamali test")
	}
	run := func(high bool) int {
		src := image.NewRGBA(image.Rect(0, 0, 320, 192))
		fill(src, src.Rect, 40, 40, 40)
		fc := &fakeCap{img: src}
		col := &collector{}
		s := newScreen2Session("scr_h", 3, fc, newGoTileEncoder(), col.send,
			screen2Params{FPS: 60, Quality: 70, MaxWidth: 4000, HighFPS: high})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.run(ctx) }()
		stop := make(chan struct{})
		go func() {
			c := uint8(0)
			for {
				select {
				case <-stop:
					return
				case <-time.After(2 * time.Millisecond):
				}
				c += 5
				fc.mu.Lock()
				fill(fc.img, image.Rect(0, 0, 50, 50), c, 0, 0)
				fc.mu.Unlock()
			}
		}()
		waitFor(t, func() bool { return len(col.snapshot()) >= 1 })
		start := time.Now()
		n0 := len(col.snapshot())
		time.Sleep(1500 * time.Millisecond)
		n := len(col.snapshot()) - n0
		el := time.Since(start)
		close(stop)
		cancel()
		<-done
		return int(float64(n) / el.Seconds())
	}
	// Zamanlamali test: mesgul makinede tek denemeyi bozan gurultuye karsi en fazla
	// 3 deneme; gercek bir 30'luk kesme her denemede basarisiz olur.
	var low, high int
	for try := 1; try <= 3; try++ {
		low = run(false)
		high = run(true)
		t.Logf("deneme %d: 30 tavan %d kare/sn, 60 tavan %d kare/sn", try, low, high)
		if low > 36 {
			t.Fatalf("30 tavanli oturum %d kare/sn uretti", low)
		}
		if low >= 18 && high >= low+low/4 {
			return
		}
	}
	if low < 18 {
		t.Skipf("makine cok yavas/mesgul (%d kare/sn); 60 karsilastirmasi anlamsiz", low)
	}
	t.Fatalf("60 tavanli oturum yeterince hizlanmadi: 30 tavan=%d, 60 tavan=%d kare/sn", low, high)
}
