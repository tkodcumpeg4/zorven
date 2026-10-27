package agent

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestHelperFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, hmCapReq, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	typ, p, err := readFrame(&buf)
	if err != nil || typ != hmCapReq || !bytes.Equal(p, []byte{1, 2, 3}) {
		t.Fatalf("%v %v %v", typ, p, err)
	}
	// bos yuk
	buf.Reset()
	_ = writeFrame(&buf, hmShutdown, nil)
	if typ, p, err = readFrame(&buf); err != nil || typ != hmShutdown || len(p) != 0 {
		t.Fatal("bos yuk")
	}
}

func TestHelperFrameRejectsBad(t *testing.T) {
	for _, in := range [][]byte{
		{0, 0, 0, 0},                // uzunluk 0
		{0xff, 0xff, 0xff, 0xff, 1}, // devasa
		{5, 0, 0, 0, 1},             // kesik
	} {
		if _, _, err := readFrame(bytes.NewReader(in)); err == nil {
			t.Fatalf("hata bekleniyordu: %v", in)
		}
	}
}

func TestHelperHandshake(t *testing.T) {
	tok := strings.Repeat("ab", 32)
	run := func(clientTok string) (error, error) {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		var wg sync.WaitGroup
		var serr, cerr error
		wg.Add(2)
		go func() { defer wg.Done(); _, _, serr = acceptHandshake(a, tok) }()
		go func() { defer wg.Done(); cerr = dialHandshake(b, clientTok, "1.2.3", 42) }()
		wg.Wait()
		return serr, cerr
	}
	if s, c := run(tok); s != nil || c != nil {
		t.Fatalf("gecerli token: %v %v", s, c)
	}
	if s, c := run(strings.Repeat("cd", 32)); s == nil || c == nil {
		t.Fatalf("yanlis token reddedilmeliydi: %v %v", s, c)
	}
}

func TestHelperHandshakeFirstMessageMustBeHello(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { _ = writeFrame(b, hmCapReq, []byte{0, 0, 0, 0}) }()
	if _, _, err := acceptHandshake(a, "x"); err == nil {
		t.Fatal("hello disi ilk mesaj reddedilmeliydi")
	}
}

func TestHelperPipeSDDL(t *testing.T) {
	got, err := helperPipeSDDL("S-1-5-21-1111-2222-3333-1001")
	if err != nil || got != "D:P(A;;GA;;;SY)(A;;GA;;;S-1-5-21-1111-2222-3333-1001)" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "S-1-5-21)(A;;GA;;;WD", "WD", "S-1-5-21-1 2"} {
		if _, err := helperPipeSDDL(bad); err == nil {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}

func TestValidHelperArgs(t *testing.T) {
	good := `\\.\pipe\zorven-screen-0123456789abcdef`
	tok := strings.Repeat("0f", 32)
	if err := validHelperArgs(good, tok); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{
		{`\\.\pipe\other`, tok}, {good, "short"}, {good, strings.ToUpper(tok)},
		{`\\.\pipe\zorven-screen-xyz`, tok}, {`\\server\pipe\zorven-screen-0123456789abcdef`, tok}, {"", ""},
	} {
		if validHelperArgs(c[0], c[1]) == nil {
			t.Errorf("reddedilmeliydi: %q %q", c[0], c[1])
		}
	}
}

func TestCapRespRoundTripAndBounds(t *testing.T) {
	c := capResp{Flags: capFlagHinted, Bounds: image.Rect(0, 0, 4, 4), Displays: 2, Method: "dxgi", W: 4, H: 4,
		Rects: []image.Rectangle{image.Rect(1, 1, 3, 2)}, Pix: [][]byte{bytes.Repeat([]byte{7}, 2*1*4)}}
	got, remote, err := decodeCapResp(encodeCapOK(c))
	if err != nil || remote != "" || got.Displays != 2 || got.Method != "dxgi" || len(got.Rects) != 1 || got.Rects[0] != c.Rects[0] {
		t.Fatalf("%+v %q %v", got, remote, err)
	}
	// dikdortgen goruntu disinda
	c.Rects = []image.Rectangle{image.Rect(0, 0, 9, 9)}
	c.Pix = [][]byte{make([]byte, 9*9*4)}
	if _, _, err := decodeCapResp(encodeCapOK(c)); err == nil {
		t.Fatal("goruntu disi dikdortgen reddedilmeliydi")
	}
	if _, remote, _ := decodeCapResp(encodeErr("boom")); remote != "boom" {
		t.Fatalf("uzak hata: %q", remote)
	}
	// kesik yuk panik/yanlis kabul olmamali
	enc := encodeCapOK(capResp{W: 2, H: 2, Rects: []image.Rectangle{image.Rect(0, 0, 2, 2)}, Pix: [][]byte{make([]byte, 16)}})
	if _, _, err := decodeCapResp(enc[:len(enc)-5]); err == nil {
		t.Fatal("kesik yanit reddedilmeliydi")
	}
}

// --- sahte yardimci ---

type fakeBackend struct {
	mu       sync.Mutex
	frames   []fakeFrame
	capCalls int
	inputs   []protocol.ScreenInput
	clip     string
}

type fakeFrame struct {
	img    *image.RGBA
	hint   []image.Rectangle
	hinted bool
	err    error
}

func solid(w, h int, v byte) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = v
	}
	return img
}

func (f *fakeBackend) NumDisplays() int           { return 1 }
func (f *fakeBackend) Bounds(int) image.Rectangle { return image.Rect(0, 0, 8, 6) }
func (f *fakeBackend) Method() string             { return "fake" }
func (f *fakeBackend) Attach() bool               { return true }
func (f *fakeBackend) ClipSeq() uint32            { return 7 }
func (f *fakeBackend) ClipGet() (string, error)   { return f.clip, nil }
func (f *fakeBackend) ClipSet(s string) error     { f.clip = s; return nil }
func (f *fakeBackend) Input(m protocol.ScreenInput, r displayRect) inputResult {
	f.mu.Lock()
	f.inputs = append(f.inputs, m)
	f.mu.Unlock()
	return inputResult{State: protocol.InputStateOK}
}
func (f *fakeBackend) CaptureHinted(int) (*image.RGBA, []image.Rectangle, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.capCalls
	f.capCalls++
	if i >= len(f.frames) {
		i = len(f.frames) - 1
	}
	fr := f.frames[i]
	return fr.img, fr.hint, fr.hinted, fr.err
}

// fakeConnector her Connect'te net.Pipe uzerinde serveHelper baslatir.
type fakeConnector struct {
	be       helperBackend
	connects int
	stale    bool
	shut     int
	failWith error
	mu       sync.Mutex
}

func (f *fakeConnector) Connect() (io.ReadWriteCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects++
	if f.failWith != nil {
		return nil, f.failWith
	}
	a, b := net.Pipe()
	go func() { _ = serveHelper(b, f.be, nil); b.Close() }()
	return a, nil
}
func (f *fakeConnector) Stale() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.stale }
func (f *fakeConnector) Shutdown()   { f.mu.Lock(); f.shut++; f.mu.Unlock() }

func TestHelperCapturerDelegation(t *testing.T) {
	full := solid(8, 6, 10)
	changed := solid(8, 6, 10)
	for y := 2; y < 4; y++ {
		for x := 3; x < 5; x++ {
			copy(changed.Pix[y*changed.Stride+x*4:], []byte{200, 200, 200, 255})
		}
	}
	be := &fakeBackend{frames: []fakeFrame{
		{img: full}, // tam
		{img: changed, hint: []image.Rectangle{image.Rect(3, 2, 5, 4)}, hinted: true}, // kismi
		{hinted: true},                      // degisim yok
		{err: errors.New("secure desktop")}, // guvenli masaustu
	}}
	fc := &fakeConnector{be: be}
	cl := newHelperClient(fc)
	defer cl.Close()
	cp := helperCapturer{cl}

	if n := cp.NumDisplays(); n != 1 {
		t.Fatalf("NumDisplays=%d", n)
	}
	if b := cp.Bounds(0); b != image.Rect(0, 0, 8, 6) {
		t.Fatalf("Bounds=%v", b)
	}
	img, hint, hinted, err := cp.CaptureHinted(0)
	if err != nil || hinted || hint != nil || !bytes.Equal(img.Pix, full.Pix) {
		t.Fatalf("tam kare: %v %v %v", err, hinted, hint)
	}
	img, hint, hinted, err = cp.CaptureHinted(0)
	if err != nil || !hinted || len(hint) != 1 || !bytes.Equal(img.Pix, changed.Pix) {
		t.Fatalf("kismi kare: %v %v %v", err, hinted, hint)
	}
	img, hint, hinted, err = cp.CaptureHinted(0)
	if err != nil || !hinted || len(hint) != 0 || img != nil {
		t.Fatalf("degisim yok: %v %v %v", err, hinted, hint)
	}
	// Guvenli masaustu: hata degil, son kare tekrar (degraded)
	if _, _, hinted, err = cp.CaptureHinted(0); err != nil || !hinted || !cl.Degraded() {
		t.Fatalf("degraded: %v %v %v", err, hinted, cl.Degraded())
	}
	// Capture her zaman tam, sahipli kopya
	img2, err := cp.Capture(0)
	if err != nil || !bytes.Equal(img2.Pix, changed.Pix) {
		t.Fatalf("Capture: %v", err)
	}
	img2.Pix[0] ^= 0xff // tuvali bozmamali
	img3, _ := cp.Capture(0)
	if img3.Pix[0] == img2.Pix[0] {
		t.Fatal("Capture tuvalin kopyasini vermeli")
	}
	if m := cp.Method(); m != "helper/fake" {
		t.Fatalf("Method=%q", m)
	}
	if fc.connects != 1 {
		t.Fatalf("tek baglanti bekleniyordu: %d", fc.connects)
	}
}

func TestHelperInputAndClipboard(t *testing.T) {
	be := &fakeBackend{frames: []fakeFrame{{img: solid(8, 6, 1)}}}
	cl := newHelperClient(&fakeConnector{be: be})
	defer cl.Close()
	res := cl.Input(protocol.ScreenInput{Kind: "keydown", Key: "a", Code: "KeyA"}, displayRect{w: 8, h: 6})
	if res.State != protocol.InputStateOK || len(be.inputs) != 1 || be.inputs[0].Key != "a" {
		t.Fatalf("%+v %+v", res, be.inputs)
	}
	cb := helperClipboard{cl}
	if err := cb.Set("merhaba"); err != nil {
		t.Fatal(err)
	}
	if got, err := cb.Get(); err != nil || got != "merhaba" {
		t.Fatalf("%q %v", got, err)
	}
	if cb.Seq() != 7 {
		t.Fatal("seq")
	}
}

func TestHelperReconnectsAfterPipeFailure(t *testing.T) {
	be := &fakeBackend{frames: []fakeFrame{{img: solid(8, 6, 3)}}}
	fc := &fakeConnector{be: be}
	cl := newHelperClient(fc)
	defer cl.Close()
	if _, err := cl.Capture(0); err != nil {
		t.Fatal(err)
	}
	cl.mu.Lock()
	cl.link.Close() // yardimci bosta cikti / boru koptu
	cl.mu.Unlock()
	if _, err := cl.Capture(0); err != nil {
		t.Fatalf("yeniden baglanmaliydi: %v", err)
	}
	if fc.connects != 2 {
		t.Fatalf("connects=%d", fc.connects)
	}
}

func TestHelperRestartsOnSessionChange(t *testing.T) {
	be := &fakeBackend{frames: []fakeFrame{{img: solid(8, 6, 3)}}}
	fc := &fakeConnector{be: be}
	cl := newHelperClient(fc)
	defer cl.Close()
	_, _ = cl.Capture(0)
	fc.mu.Lock()
	fc.stale = true
	fc.mu.Unlock()
	_, _ = cl.Capture(0)
	fc.mu.Lock()
	fc.stale = false
	fc.mu.Unlock()
	if fc.connects != 2 {
		t.Fatalf("oturum degisince yeniden baslatilmaliydi: %d", fc.connects)
	}
}

func TestHelperNoUserErrorAndBackoff(t *testing.T) {
	fc := &fakeConnector{be: &fakeBackend{frames: []fakeFrame{{img: solid(2, 2, 1)}}}, failWith: errNoInteractiveUser}
	cl := newHelperClient(fc)
	now := time.Unix(1000, 0)
	cl.now = func() time.Time { return now }
	if cl.NumDisplays() != 0 {
		t.Fatal("kullanici yokken 0 monitor")
	}
	if err := cl.Err(); err == nil || strings.Contains(err.Error(), "scr.err") {
		t.Fatalf("cevrilmis hata bekleniyordu: %v", err)
	}
	cl.NumDisplays() // backoff icinde yeniden Connect denenmemeli
	if fc.connects != 1 {
		t.Fatalf("backoff: connects=%d", fc.connects)
	}
	now = now.Add(3 * time.Second)
	fc.mu.Lock()
	fc.failWith = nil
	fc.mu.Unlock()
	if cl.NumDisplays() != 1 {
		t.Fatal("kullanici gelince calismali")
	}
	if cl.Err() != nil {
		t.Fatal("hata temizlenmeliydi")
	}
}

func TestHelperOutOfSyncRestarts(t *testing.T) {
	// Taban kare yokken "degisim yok" gelirse yardimci yeniden baslatilir.
	be := &fakeBackend{frames: []fakeFrame{{hinted: true}}}
	cl := newHelperClient(&fakeConnector{be: be})
	defer cl.Close()
	if _, _, _, err := cl.CaptureHinted(0); err == nil {
		t.Fatal("hata bekleniyordu")
	}
}
