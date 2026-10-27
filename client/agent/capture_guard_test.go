package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testGuard(dir string, pid int, now *time.Time, alive func(int) bool) *dxgiGuard {
	return newDXGIGuard(dir, "0.2.30", pid, func() time.Time { return *now }, alive)
}

func TestGuardCleanFlow(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	g := testGuard(dir, 100, &now, func(int) bool { return false })
	if ok, _ := g.Check(); !ok {
		t.Fatal("temiz dizinde izin verilmeliydi")
	}
	if err := g.Begin(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(g.sentinelPath(100)); err != nil {
		t.Fatal("sentinel yazilmadi")
	}
	for i := 0; i < guardGoodFrames-1; i++ {
		g.FrameOK()
	}
	if _, err := os.Stat(g.sentinelPath(100)); err != nil {
		t.Fatal("esikten once silinmemeli")
	}
	g.FrameOK()
	if _, err := os.Stat(g.sentinelPath(100)); !os.IsNotExist(err) {
		t.Fatal("esikte silinmeliydi")
	}
	g2 := testGuard(dir, 101, &now, func(int) bool { return false })
	if ok, _ := g2.Check(); !ok {
		t.Fatal("temiz kapanista atlama olmamali")
	}
}

func TestGuardCrashSkips24h(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	dead := testGuard(dir, 100, &now, func(int) bool { return false })
	_ = dead.Begin() // surec burada coker (Clear cagrilmaz)

	g := testGuard(dir, 200, &now, func(int) bool { return false })
	ok, reason := g.Check()
	if ok || reason == "" {
		t.Fatalf("atlama bekleniyordu: %v %q", ok, reason)
	}
	if _, err := os.Stat(dead.sentinelPath(100)); !os.IsNotExist(err) {
		t.Fatal("olu sentinel temizlenmeliydi")
	}
	now = now.Add(23 * time.Hour)
	if ok, _ := testGuard(dir, 201, &now, func(int) bool { return true }).Check(); ok {
		t.Fatal("23 saat sonra hala atlanmali")
	}
	now = now.Add(2 * time.Hour)
	if ok, _ := testGuard(dir, 202, &now, func(int) bool { return true }).Check(); !ok {
		t.Fatal("24 saat sonra izin verilmeli")
	}
	if _, err := os.Stat(filepath.Join(dir, guardSkipFile)); !os.IsNotExist(err) {
		t.Fatal("suresi dolan atlama kaydi silinmeliydi")
	}
}

func TestGuardLiveForeignSentinelIgnored(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	other := testGuard(dir, 100, &now, func(int) bool { return true })
	_ = other.Begin()
	g := testGuard(dir, 200, &now, func(pid int) bool { return pid == 100 })
	if ok, _ := g.Check(); !ok {
		t.Fatal("canli surecin sentinel'i atlama sebebi olmamali")
	}
	if _, err := os.Stat(other.sentinelPath(100)); err != nil {
		t.Fatal("canli sentinel silinmemeli")
	}
}

func TestGuardClearAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	g := testGuard(dir, 100, &now, func(int) bool { return false })
	_ = g.Begin()
	g.Clear()
	if _, err := os.Stat(g.sentinelPath(100)); !os.IsNotExist(err) {
		t.Fatal("Clear silmeliydi")
	}
	_ = os.WriteFile(filepath.Join(dir, "dxgi-7.sentinel"), []byte("{bozuk"), 0o600)
	if ok, _ := g.Check(); !ok {
		t.Fatal("bozuk kayit atlama sebebi olmamali")
	}
}

func TestChooseCaptureMode(t *testing.T) {
	cases := []struct {
		env     string
		allowed bool
		want    string
	}{
		{"", true, "dxgi"}, {"auto", true, "dxgi"}, {"", false, "kbinani"},
		{"DXGI", false, "dxgi"}, {"gdi", true, "gdi"}, {" KBinani ", true, "kbinani"},
	}
	for _, c := range cases {
		if got := chooseCaptureMode(c.env, c.allowed); got != c.want {
			t.Errorf("%q/%v: %s != %s", c.env, c.allowed, got, c.want)
		}
	}
}
