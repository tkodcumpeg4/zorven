package agent

import (
	"fmt"
	"image"
	"os"
	"testing"
	"time"
)

// Surekli ACCESS_LOST (UAC/masaustu degisimi) 30 sn'lik yedege dusurmemeli;
// kisa aralikla yeniden denenmeli ve donunce DXGI'ye gecilmeli.
func TestDXGICapturerSustainedLostRetriesFast(t *testing.T) {
	now := time.Unix(1000, 0)
	lost := fakeNext{err: fmt.Errorf("x: %w", errDXGILost)}
	seq := []fakeNext{frameRes(image.Rect(0, 0, 64, 64))}
	for i := 0; i < 8; i++ {
		seq = append(seq, lost)
	}
	seq = append(seq, frameRes(image.Rect(0, 0, 64, 64)))
	src := &fakeDXGISrc{nextRes: seq}
	c := newDXGICapturer(&fbCap{}, src)
	c.now = func() time.Time { return now }
	for i := 0; i < 9; i++ {
		c.CaptureHinted(0)
		now = now.Add(300 * time.Millisecond) // dxgiLostRetry'den uzun
	}
	if c.fbUntil.After(now.Add(2 * time.Second)) {
		t.Fatalf("surekli kayip uzun yedege dusurmemeli: fbUntil=%v now=%v", c.fbUntil, now)
	}
	if _, _, hinted, _ := c.CaptureHinted(0); !hinted {
		t.Fatal("kayip bitince DXGI'ye donulmeli")
	}
}

// Desteklenmeyen kaynak (surucu/RDP) ussel geri cekilmeyi korumali.
func TestDXGICapturerUnsupportedKeepsBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	src := &fakeDXGISrc{openErrs: []error{fmt.Errorf("x: %w", errDXGIUnsupported)}}
	c := newDXGICapturer(&fbCap{}, src)
	c.now = func() time.Time { return now }
	c.CaptureHinted(0)
	if got := c.fbUntil.Sub(now); got < dxgiRetryBase {
		t.Fatalf("desteklenmeyen icin uzun bekleme beklenir: %v", got)
	}
}

func TestDXGICapturerIdleTimeoutsCountHealthy(t *testing.T) {
	src := &fakeDXGISrc{nextRes: []fakeNext{frameRes(image.Rect(0, 0, 64, 64)), {}, {}, {}}}
	c := newDXGICapturer(&fbCap{}, src)
	g := &fakeGuard{}
	c.guard = g
	for i := 0; i < 4; i++ {
		c.CaptureHinted(0)
	}
	if g.oks != 4 {
		t.Fatalf("zaman asimi turlari da saglikli sayilmali: oks=%d", g.oks)
	}
}

func TestGuardRefCountedClear(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	g := testGuard(dir, 100, &now, func(int) bool { return false })
	_ = g.Begin()
	_ = g.Begin() // ikinci oturum
	g.Clear()
	if _, err := os.Stat(g.sentinelPath(100)); err != nil {
		t.Fatal("hala bir oturum acikken sentinel silinmemeli")
	}
	g.Clear()
	if _, err := os.Stat(g.sentinelPath(100)); !os.IsNotExist(err) {
		t.Fatal("son oturum kapaninca silinmeli")
	}
}
