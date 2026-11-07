package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/internal/tui"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

func isolateHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("NO_COLOR", "1")
}

func TestTruncatePathUTF8(t *testing.T) {
	p := "/api/" + strings.Repeat("日本語", 20)
	got := truncatePath(p, 32)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("%q", got)
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("bozuk UTF-8: %q", got)
		}
	}
	if truncatePath("/a", 32) != "/a" {
		t.Fatal("kisa yol degismemeli")
	}
}

func TestHTTPStatusTextCoverage(t *testing.T) {
	for _, c := range []int{200, 307, 409, 503, 413} {
		if httpStatusText(c) == "" {
			t.Errorf("%d bos", c)
		}
	}
	if httpStatusText(299) != "" {
		t.Error("bilinmeyen kod bos olmali")
	}
}

func TestPublicScheme(t *testing.T) {
	if publicScheme(true) != "http" || publicScheme(false) != "https" {
		t.Fatal("sema")
	}
}

func TestResolveServerAddr(t *testing.T) {
	t.Setenv("ZORVEN_SERVER", "")
	cfg := config.DefaultConfig()
	if got := resolveServerAddr("", cfg); got != "zorven.app:443" {
		t.Fatal(got)
	}
	cfg.ServerAddr = "cfg.example:443"
	if got := resolveServerAddr("", cfg); got != "cfg.example:443" {
		t.Fatal(got)
	}
	t.Setenv("ZORVEN_SERVER", "env.example:443")
	if got := resolveServerAddr("", cfg); got != "env.example:443" {
		t.Fatal(got)
	}
	if got := resolveServerAddr("flag.example:1", cfg); got != "flag.example:1" {
		t.Fatal(got)
	}
}

func TestAPITokenPrecedence(t *testing.T) {
	isolateHome(t)
	t.Setenv("ZORVEN_API_TOKEN", "")
	if tok, _ := resolveAPIToken("", ""); tok != "" {
		t.Fatal("bos olmali")
	}
	if err := config.SaveAPIToken("zrv_api_saved"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := resolveAPIToken("", ""); tok != "zrv_api_saved" {
		t.Fatal("kayitli")
	}
	t.Setenv("ZORVEN_API_TOKEN", "zrv_api_env")
	if tok, _ := resolveAPIToken("", ""); tok != "zrv_api_env" {
		t.Fatal("env kayitliyi ezmeli")
	}
	f := filepath.Join(t.TempDir(), "tk")
	_ = os.WriteFile(f, []byte("zrv_api_file\n"), 0o600)
	if tok, _ := resolveAPIToken("", f); tok != "zrv_api_file" {
		t.Fatal("dosya env'i ezmeli")
	}
	if _, err := os.Stat(f); err != nil {
		t.Fatal("api token dosyasi silinmemeli")
	}
	if tok, _ := resolveAPIToken("zrv_api_flag", f); tok != "zrv_api_flag" {
		t.Fatal("bayrak")
	}
	// access: kayitli token explicit sayilmaz
	t.Setenv("ZORVEN_API_TOKEN", "")
	if tok, _ := explicitAPIToken("", ""); tok != "" {
		t.Fatal("kayitli token explicit olmamali")
	}
}

func TestLoginLogoutRoundTrip(t *testing.T) {
	isolateHome(t)
	t.Setenv("ZORVEN_API_TOKEN", "")
	f := filepath.Join(t.TempDir(), "tk")
	_ = os.WriteFile(f, []byte("zrv_api_abc\n"), 0o600)
	login := newLoginCmd()
	login.SetArgs([]string{"--api-token-file", f})
	if err := login.Execute(); err != nil {
		t.Fatal(err)
	}
	if config.LoadAPIToken() != "zrv_api_abc" {
		t.Fatal("kaydedilmedi")
	}
	// agent token reddedilir
	_ = os.WriteFile(f, []byte("zrv_live_abc\n"), 0o600)
	login = newLoginCmd()
	login.SetArgs([]string{"--api-token-file", f})
	if err := login.Execute(); err == nil {
		t.Fatal("istemci token'i reddedilmeli")
	}
	if err := config.SetToken("zrv_live_dev"); err != nil {
		t.Fatal(err)
	}
	logout := newLogoutCmd()
	logout.SetArgs([]string{"--yes"})
	if err := logout.Execute(); err != nil {
		t.Fatal(err)
	}
	p, _ := config.Path()
	if c, _ := config.LoadFile(p); c.Token != "" {
		t.Fatal("cihaz token'i silinmedi")
	}
	if config.LoadAPIToken() != "" {
		t.Fatal("api token silinmedi")
	}
}

func TestTailLines(t *testing.T) {
	data := "a\nb\nc\nd\ne\n"
	r := strings.NewReader(data)
	got, _, err := tailLines(r, int64(len(data)), 3)
	if err != nil || strings.Join(got, ",") != "c,d,e" {
		t.Fatalf("%v %v", got, err)
	}
	got, _, _ = tailLines(strings.NewReader(data), int64(len(data)), 50)
	if len(got) != 5 {
		t.Fatalf("%v", got)
	}
	if got, _, _ := tailLines(strings.NewReader(""), 0, 5); len(got) != 0 {
		t.Fatal("bos")
	}
}

func TestDoctorHelpers(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	skew, err := clockSkew("Wed, 28 Oct 2026 11:59:00 GMT", now)
	if err != nil || skew != time.Minute {
		t.Fatalf("%v %v", skew, err)
	}
	if _, err := clockSkew("bozuk", now); err == nil {
		t.Fatal("bozuk tarih hata vermeli")
	}
	if has, ok := parseCapEff("Name:\tx\nCapEff:\t0000000000001000\n"); !ok || !has {
		t.Fatal("CAP_NET_ADMIN bit 12")
	}
	if has, ok := parseCapEff("CapEff:\t0000000000000000\n"); !ok || has {
		t.Fatal("yetkisiz")
	}
}

func TestDoctorReportNoNetwork(t *testing.T) {
	isolateHome(t)
	t.Setenv("ZORVEN_TOKEN", "")
	t.Setenv("ZORVEN_SERVER", "nonexistent.invalid:443")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rep := runDoctor(ctx)
	if rep.OK {
		t.Fatal("token yok / DNS yok: basarisiz olmali")
	}
	ids := map[string]string{}
	for _, c := range rep.Checks {
		ids[c.ID] = c.Status
	}
	if ids["token"] != chFail || ids["dns"] != chFail || ids["tls"] != chSkip {
		t.Fatalf("%v", ids)
	}
}

func TestBridgeHalfClose(t *testing.T) {
	// echo sunucusu: istemci yazmayi kapatinca (EOF) yanitin tamami yine de gelmeli.
	srv, _ := net.Listen("tcp", "127.0.0.1:0")
	defer srv.Close()
	go func() {
		c, err := srv.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c) // istemci CloseWrite edene kadar okur
		_, _ = c.Write(append([]byte("echo:"), b...))
	}()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		local, err := ln.Accept()
		if err != nil {
			return
		}
		remote, err := net.Dial("tcp", srv.Addr().String())
		if err != nil {
			return
		}
		bridge(local, remote)
		local.Close()
		remote.Close()
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("hello"))
	_ = c.(*net.TCPConn).CloseWrite()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(c)
	if err != nil || string(b) != "echo:hello" {
		t.Fatalf("%q %v", b, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge bitmedi")
	}
}

func TestForwardOneReturnsDialError(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	tc, _ := forwardTLSConfig("x.example", "", true)
	err := forwardOne(context.Background(), a, "127.0.0.1:1", tc) // kapali port
	if err == nil {
		t.Fatal("dial hatasi donmeli (sessizce yutulmamali)")
	}
}

func TestServiceRunOnceNoToken(t *testing.T) {
	isolateHome(t)
	if _, err := os.Stat(config.SystemPath()); err == nil {
		t.Skip("sistem ayar dosyasi var")
	}
	p := &serviceProgram{}
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	if code := p.runOnce(context.Background(), lg); code != exitConfig {
		t.Fatalf("kod %d, beklenen %d", code, exitConfig)
	}
}

func TestServiceRunExitsNonZero(t *testing.T) {
	isolateHome(t)
	if _, err := os.Stat(config.SystemPath()); err == nil {
		t.Skip("sistem ayar dosyasi var")
	}
	var got int
	old := exitProcess
	exitProcess = func(c int) { got = c }
	defer func() { exitProcess = old }()
	(&serviceProgram{}).run(context.Background())
	if got != exitConfig {
		t.Fatalf("surec %d koduyla cikmali, cikti %d", exitConfig, got)
	}
}

func TestSaveServiceOptsRejectsBadToken(t *testing.T) {
	isolateHome(t)
	if err := saveServiceOpts(installOpts{token: "kotu"}); err == nil {
		t.Fatal("gecersiz token reddedilmeli")
	}
}

func TestSettingsLiveApplyAndInsecureConfirm(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	up := filepath.Join(dir, "u.json")
	m := &settingsModel{
		userPath: func() (string, error) { return up, nil }, systemPath: func() string { return filepath.Join(dir, "s.json") },
		probe: func() string { return "running" },
	}
	var capsCalled, reloadCalled int
	m.setCaps = func(tr, sc *bool, system bool) (liveResult, error) { capsCalled++; return liveApplied, nil }
	m.reload = func(system bool) (liveResult, error) { reloadCalled++; return liveNeedRestart, nil }
	m.load()
	m.running = "running"

	m.cursor = siScreen
	m.activate(1)
	if capsCalled != 1 || m.msgErr || !strings.Contains(m.msg, i18nOK()) {
		t.Fatalf("ekran anahtari canli uygulanmali (%d) %q", capsCalled, m.msg)
	}
	// insecure: onay ister; baska tus iptal eder
	m.cursor = siInsecure
	m.activate(1)
	if !m.confirming || m.cfg.Insecure {
		t.Fatal("onay beklenmeli")
	}
	m.handle(tui.Key{Kind: tui.KeyRune, Text: "n"})
	if m.cfg.Insecure || m.confirming {
		t.Fatal("iptal edilmeli")
	}
	m.activate(1)
	m.handle(tui.Key{Kind: tui.KeyRune, Text: "y"})
	// insecure ajanin "reload" islemiyle canli degismez (configLive): reload cagrilmaz,
	// "yeniden baslatilinca uygulanir" notu gorunur; "canli uygulandi" denmez.
	if !m.cfg.Insecure || reloadCalled != 0 {
		t.Fatalf("onayla acilmali ve reload cagrilmamali: %v %d", m.cfg.Insecure, reloadCalled)
	}
	if !strings.Contains(m.msg, strings.TrimSpace(i18n.T("set.saved.suffix"))) || strings.Contains(m.msg, i18nOK()) {
		t.Fatalf("insecure icin yeniden baslatma notu bekleniyordu: %q", m.msg)
	}
	// sunucu adresi ise canli uygulanir: reload cagrilir.
	m.cursor = siServer
	m.activate(1)
	m.buf = []rune("alt.example:443")
	m.handle(tui.Key{Kind: tui.KeyEnter})
	if reloadCalled != 1 || m.cfg.ServerAddr != "alt.example:443" {
		t.Fatalf("sunucu adresi icin reload cagrilmali: %d %q", reloadCalled, m.cfg.ServerAddr)
	}
}

func i18nOK() string { return strings.TrimSpace(i18n.T("v3.set.live.ok")) }

func TestEffectiveConfigSources(t *testing.T) {
	isolateHome(t)
	t.Setenv("ZORVEN_SERVER", "env.example:443")
	t.Setenv("ZORVEN_TOKEN", "")
	t.Setenv("ZORVEN_LANG", "")
	if err := config.SetToken("zrv_live_abcdefghijklmnopqrst"); err != nil {
		t.Fatal(err)
	}
	v := effectiveConfig()
	srv, _ := v.lookup("server")
	tok, _ := v.lookup("token")
	if srv.Source != "env" || srv.Value != "env.example:443" {
		t.Fatalf("%+v", srv)
	}
	if tok.Source != "user" || strings.Contains(tok.Value, "abcdefghijklmnopqrst") {
		t.Fatalf("token maskeli ve kullanici kaynakli olmali: %+v", tok)
	}
}

func TestStatusExitCodeNothingRunning(t *testing.T) {
	isolateHome(t)
	of := findAgent
	findAgent = func() (*localctl.Endpoint, *localctl.Response, error) { return nil, nil, nil }
	defer func() { findAgent = of }()
	d := collectStatus()
	if d.Exit != exitNothing && d.Service != "running" {
		t.Fatalf("exit %d service %s", d.Exit, d.Service)
	}
}

func TestLiveStatusKnown(t *testing.T) {
	var l liveStatus
	if l.known() {
		t.Fatal("bos bilinmiyor")
	}
	l.State = "connected"
	if !l.known() {
		t.Fatal("state varsa bilinir")
	}
	l.StartedAt = time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	if up, ok := l.uptime(); !ok || up < time.Minute {
		t.Fatalf("%v %v", up, ok)
	}
	if got := decodeLive(nil); got.known() {
		t.Fatal("nil")
	}
}
