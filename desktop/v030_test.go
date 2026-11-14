package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// fakeProtector, DPAPI yerine tersine cevrilebilir sahte koruyucu.
type fakeProtector struct{ fail bool }

func (f fakeProtector) Protect(p []byte) ([]byte, error) {
	out := make([]byte, len(p))
	for i, b := range p {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

func (f fakeProtector) Unprotect(e []byte) ([]byte, error) {
	if f.fail {
		return nil, errors.New("wrong user")
	}
	return f.Protect(e)
}

func withProtector(t *testing.T, p tokenProtect) {
	t.Helper()
	old := tokenProtector
	tokenProtector = p
	t.Cleanup(func() { tokenProtector = old })
}

func TestConfigTokenSealRoundTrip(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, fakeProtector{})
	cfg := defaultConfig()
	cfg.Token = "zrv_live_secret123"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	path, _ := configPath()
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("zrv_live_secret123")) {
		t.Fatal("token diskte acik metin olmamali")
	}
	if !bytes.Contains(raw, []byte(`"token_enc"`)) {
		t.Fatal("token_enc yazilmali")
	}
	got, notice, err := loadConfigNotice()
	if err != nil || notice != nil {
		t.Fatalf("yukleme: %v %v", err, notice)
	}
	if got.Token != "zrv_live_secret123" {
		t.Fatalf("token geri cozulemedi: %q", got.Token)
	}
}

func TestConfigPlaintextMigration(t *testing.T) {
	useTempConfigDir(t)
	// Eski surum acik metin yazmis; koruyucu simdi var.
	withProtector(t, nil)
	cfg := defaultConfig()
	cfg.Token = "zrv_live_old"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	withProtector(t, fakeProtector{})
	got, _, _ := loadConfigNotice()
	if got.Token != "zrv_live_old" {
		t.Fatalf("acik metin token okunamadi: %q", got.Token)
	}
	if err := saveConfig(got); err != nil {
		t.Fatal(err)
	}
	path, _ := configPath()
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("zrv_live_old")) {
		t.Fatal("kayit sonrasi token sifrelenmeli")
	}
}

func TestConfigCorruptIsBackedUp(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	path, _ := configPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	bad := []byte(`{"token": "zrv_live_keepme", `)
	_ = os.WriteFile(path, bad, 0o600)
	_, notice, err := loadConfigNotice()
	if err == nil || notice == nil || notice.Kind != "corrupt" {
		t.Fatalf("bozuk dosya bildirilmedi: %v %v", err, notice)
	}
	if notice.Backup == "" {
		t.Fatal("yedek yolu bos")
	}
	kept, rerr := os.ReadFile(notice.Backup)
	if rerr != nil || !bytes.Equal(kept, bad) {
		t.Fatalf("yedek icerigi bozuk: %v", rerr)
	}
	// Sonraki kayit yedegi etkilemez.
	if err := saveConfig(defaultConfig()); err != nil {
		t.Fatal(err)
	}
	kept2, _ := os.ReadFile(notice.Backup)
	if !bytes.Equal(kept2, bad) {
		t.Fatal("yedek degismemeli")
	}
}

func TestConfigDecryptFailureBackedUp(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, fakeProtector{})
	cfg := defaultConfig()
	cfg.Token = "zrv_live_abc"
	_ = saveConfig(cfg)
	withProtector(t, fakeProtector{fail: true})
	got, notice, _ := loadConfigNotice()
	if notice == nil || notice.Kind != "decrypt" || notice.Backup == "" {
		t.Fatalf("decrypt bildirimi yok: %v", notice)
	}
	if got.Token != "" {
		t.Fatal("token bos olmali")
	}
	if _, err := os.Stat(notice.Backup); err != nil {
		t.Fatalf("yedek yok: %v", err)
	}
}

func TestSaveConfigAtomicNoTempLeft(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	for i := 0; i < 5; i++ {
		if err := saveConfig(defaultConfig()); err != nil {
			t.Fatal(err)
		}
	}
	path, _ := configPath()
	ents, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("gecici dosya kaldi: %s", e.Name())
		}
	}
}

func TestRotatingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "desktop.log")
	rf, err := newRotatingFile(p, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	line := []byte(strings.Repeat("x", 60) + "\n")
	for i := 0; i < 12; i++ {
		if _, err := rf.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) > 3 {
		t.Fatalf("en fazla 3 dosya olmali, %d var", len(ents))
	}
	for _, e := range ents {
		fi, _ := e.Info()
		if fi.Size() > 100 {
			t.Fatalf("%s boyutu sinirdan buyuk: %d", e.Name(), fi.Size())
		}
	}
}

func TestRedactSecrets(t *testing.T) {
	got := redactSecrets("hata zrv_live_AbC-123_x oldu rpsh_live_zz")
	if strings.Contains(got, "AbC") || strings.Contains(got, "rpsh_live_zz") {
		t.Fatalf("token maskelenmedi: %s", got)
	}
}

func TestDiagnosticsNoToken(t *testing.T) {
	log, ring, _ := newDesktopLoggerAt(t.TempDir())
	a := &App{cfg: defaultConfig(), log: log, errs: ring}
	a.cfg.Token = "zrv_live_supersecret"
	log.Warn("baglanti", "token", "zrv_live_supersecret")
	txt := a.diagnosticsText()
	if strings.Contains(txt, "supersecret") {
		t.Fatalf("tanilama token iceriyor:\n%s", txt)
	}
	if !strings.Contains(txt, "Signed in: true") {
		t.Fatalf("beklenen alan yok:\n%s", txt)
	}
}

func TestValidateServerAddr(t *testing.T) {
	ok := map[string]string{"": "zorven.app:443", "zorven.app": "zorven.app", "https://a.example.com:8443/": "a.example.com:8443", "10.0.0.5:9000": "10.0.0.5:9000"}
	for in, want := range ok {
		got, err := validateServerAddr(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v (beklenen %q)", in, got, err, want)
		}
	}
	for _, in := range []string{"a b", "host:99999", "host:0", "ev;rm -rf", "a/b"} {
		if _, err := validateServerAddr(in); err == nil {
			t.Errorf("%q reddedilmeliydi", in)
		}
	}
}

func TestValidateCAPath(t *testing.T) {
	if err := validateCAPath(""); err != nil {
		t.Fatal(err)
	}
	if err := validateCAPath(filepath.Join(t.TempDir(), "yok.pem")); err == nil {
		t.Fatal("olmayan dosya reddedilmeli")
	}
	p := filepath.Join(t.TempDir(), "x.pem")
	_ = os.WriteFile(p, []byte("not a cert"), 0o600)
	if err := validateCAPath(p); err == nil {
		t.Fatal("gecersiz PEM reddedilmeli")
	}
}

// sharePortList'in testi tray_test.go'da (tepsi yalniz windows/linux'ta derlenir).
func TestHostURL(t *testing.T) {
	cases := map[string]string{
		"a.zorven.app":        "https://a.zorven.app",
		"tcp.zorven.app:5432": "tcp://tcp.zorven.app:5432",
		"udp://x:53":          "udp://x:53",
		"https://z.app":       "https://z.app",
	}
	for in, w := range cases {
		if g := hostURL(in); g != w {
			t.Errorf("hostURL(%q)=%q want %q", in, g, w)
		}
	}
}

func TestParseArgs(t *testing.T) {
	h, pid := parseArgs([]string{"--hidden", waitPIDFlag, "1234"})
	if !h || pid != 1234 {
		t.Fatalf("%v %d", h, pid)
	}
	h, pid = parseArgs([]string{waitPIDFlag, "abc"})
	if h || pid != 0 {
		t.Fatalf("%v %d", h, pid)
	}
}

func TestReleaseNotesURL(t *testing.T) {
	for lang, want := range map[string]string{
		"tr": "https://zorven.app/updates?utm_source=desktop", "en": "https://zorven.app/en/updates?utm_source=desktop",
		"ja": "https://zorven.app/ja/updates?utm_source=desktop", "zh": "https://zorven.app/zh/updates?utm_source=desktop",
	} {
		if got := releaseNotesURL(lang); got != want {
			t.Errorf("%s: %s", lang, got)
		}
	}
}

func TestTrafficStats(t *testing.T) {
	var ts trafficStats
	for i := 1; i <= 100; i++ {
		st := 200
		if i%10 == 0 {
			st = 502
		}
		ts.add(st, time.Duration(i)*time.Millisecond)
	}
	s, changed := ts.snapshot()
	if !changed || s.Requests != 100 || s.Errors != 10 || s.P95Ms < 90 || s.P50Ms < 40 || s.P50Ms > 60 {
		t.Fatalf("beklenmeyen ozet: %+v", s)
	}
	if _, ch := ts.snapshot(); ch {
		t.Fatal("degisiklik yokken changed=false olmali")
	}
	ts.reset()
	if s, _ := ts.snapshot(); s.Requests != 0 {
		t.Fatal("reset sifirlamali")
	}
}

func TestWaitDone(t *testing.T) {
	if !waitDone(nil, time.Millisecond) {
		t.Fatal("nil kanal beklememeli")
	}
	ch := make(chan struct{})
	if waitDone(ch, 20*time.Millisecond) {
		t.Fatal("zaman asimi bekleniyordu")
	}
	go func() { time.Sleep(10 * time.Millisecond); close(ch) }()
	if !waitDone(ch, time.Second) {
		t.Fatal("kapanis beklenmeliydi")
	}
}

func TestSleepCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	start := time.Now()
	if sleepCtx(ctx, 5*time.Second) {
		t.Fatal("iptal edilmeliydi")
	}
	if time.Since(start) > time.Second {
		t.Fatal("iptal gecikti")
	}
}

func TestServiceFromResponse(t *testing.T) {
	r := localctl.Response{OK: true, Service: true, PID: 42, Version: "0.2.34",
		Tun: &localctl.TunInfo{Active: true, Connected: false, Reason: "bekleniyor"}}
	s := serviceFromResponse(r, nil)
	if !s.Found || !s.Service || s.PID != 42 || s.Connected || !s.TunActive || s.State != "reconnecting" {
		t.Fatalf("%+v", s)
	}
	// Tun yok: calisan ajan baglanmis varsayilir.
	s = serviceFromResponse(localctl.Response{OK: true}, nil)
	if !s.Connected || s.State != "connected" {
		t.Fatalf("%+v", s)
	}
}

func TestAlreadyConnectedMsg(t *testing.T) {
	if !isAlreadyConnectedMsg("baska bir istemci bagli (409); kapatin") {
		t.Fatal("409 taninmali")
	}
	if isAlreadyConnectedMsg("sunucuya ulasilamadi") {
		t.Fatal("yanlis pozitif")
	}
}

func TestSharePortSecureNotPersisted(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	a := &App{cfg: defaultConfig(), log: slog.Default(), errs: &errRing{max: 5}}
	a.cfg.Token = "" // baglanti kurulmasin; yalnizca kalicilik denetlenir
	_ = a.SharePort(3000, true)
	if a.cfg.NoTerminal || a.cfg.NoScreen {
		t.Fatal("guvenli mod ayara yazilmamali")
	}
	path, _ := configPath()
	raw, _ := os.ReadFile(path)
	var disk Config
	_ = json.Unmarshal(raw, &disk)
	if disk.NoTerminal || disk.NoScreen {
		t.Fatalf("diskte NoTerminal/NoScreen true: %s", raw)
	}
	if disk.LastPort != 3000 {
		t.Fatalf("son port kaydedilmeli: %d", disk.LastPort)
	}
}

func TestDownloadVerified(t *testing.T) {
	payload := append([]byte("MZ"), bytes.Repeat([]byte("a"), 300000)...)
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	var lastDone int64
	path, err := downloadVerified(context.Background(), srv.URL, hex.EncodeToString(sum[:]), int64(len(payload)), func(d, _ int64) { lastDone = d })
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if lastDone != int64(len(payload)) {
		t.Fatalf("ilerleme son baytta bitmeli: %d", lastDone)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, payload) {
		t.Fatal("dosya icerigi farkli")
	}

	// sha uyusmazligi: dosya silinir, hata doner.
	if p, err := downloadVerified(context.Background(), srv.URL, strings.Repeat("0", 64), 0, nil); err == nil {
		os.Remove(p)
		t.Fatal("sha uyusmazligi reddedilmeli")
	}
	// iptal
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := downloadVerified(ctx, srv.URL, hex.EncodeToString(sum[:]), 0, nil); err == nil {
		os.Remove(p)
		t.Fatal("iptal edilen indirme basarili olmamali")
	}
}
