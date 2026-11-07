package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

const testSID = "S-1-5-21-111-222-333-1001"

func TestInstallServiceFlagsAndMarker(t *testing.T) {
	f := newFake()
	in := &installer{sys: f, paths: testPaths()}
	if err := in.installService(setupParams{Server: "s:443", OperatorSID: testSID}); err != nil {
		t.Fatal(err)
	}
	if !f.has("cli service install --allow-no-token --enable-terminal --enable-screen --server s:443") {
		t.Errorf("ilk kurulum terminal+ekran izinli olmali: %v", f.calls)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "--enable-deploy") {
			t.Errorf("enable_deploy kurulumda verilmemeli: %s", c)
		}
	}
	if !f.has("marker") || !f.has("operator "+testSID) {
		t.Errorf("isaret ve operator yazilmali: %v", f.calls)
	}
	if f.index("operator") > f.index("cli service start") {
		t.Error("operator servis baslamadan once eklenmeli")
	}

	// Ikinci kurulum/onarim: isaret var, izin bayragi YOK (bilincli kapatma korunur).
	f.calls = nil
	f.files[in.paths.CLIExe()] = true
	if err := in.Repair("s:443", "", testSID); err != nil {
		t.Fatal(err)
	}
	if f.has("cli service install --allow-no-token --enable") || strings.Contains(strings.Join(f.calls, "|"), "--enable-") {
		t.Errorf("onarimda izin bayragi verilmemeli: %v", f.calls)
	}
	if f.has("marker") {
		t.Error("isaret ikinci kez yazilmamali")
	}
	if !f.has("operator " + testSID) {
		t.Error("onarim operator eklemeli (0.3.5 gecisi)")
	}
}

func TestInstallServiceNoOperatorWhenEmpty(t *testing.T) {
	f := newFake()
	in := &installer{sys: f, paths: testPaths()}
	if err := in.installService(setupParams{Server: "s:443"}); err != nil {
		t.Fatal(err)
	}
	if f.has("operator") {
		t.Error("SID yoksa operator eklenmemeli")
	}
}

func TestValidOperatorSID(t *testing.T) {
	for sid, want := range map[string]bool{
		testSID:                true,
		"S-1-12-1-1-2-3-4":     true,
		"S-1-5-18":             false, // SYSTEM
		"S-1-5-32-544":         false, // Administrators
		"S-1-5-21-1":           false,
		"":                     false,
		testSID + "; calc.exe": false,
		"S-1-5-21-1-2-3\" --x": false,
	} {
		if got := validOperatorSID(sid) != ""; got != want {
			t.Errorf("%q: %v", sid, got)
		}
	}
}

// helperEnv: ProgramData'yi gecici dizine yonlendirir ve servis adimlarini sahteler.
func helperEnv(t *testing.T) (reloads, restarts *int) {
	t.Helper()
	t.Setenv("ProgramData", t.TempDir())
	reloads, restarts = new(int), new(int)
	oldR, oldS := svcReloadFn, svcRestartFn
	svcReloadFn = func() error { *reloads++; return nil }
	svcRestartFn = func() error { *restarts++; return nil }
	t.Cleanup(func() { svcReloadFn, svcRestartFn = oldR, oldS })
	return
}

func writePatch(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSvcConfigHelperRejectsUnknownKey(t *testing.T) {
	reloads, restarts := helperEnv(t)
	for _, body := range []string{`{"token":true}`, `{"enable_terminal":true,"server_addr":false}`, `{}`, `[]`, `{"enable_terminal":"x"}`} {
		if got := runSvcConfigHelper([]string{writePatch(t, body)}); got != setupUsage {
			t.Errorf("%s: kod %d", body, got)
		}
	}
	if _, err := os.Stat(clientcfg.SystemPath()); err == nil {
		t.Error("reddedilen girdi sistem ayarina yazmamali")
	}
	if *reloads+*restarts != 0 {
		t.Error("servise dokunulmamali")
	}
	if runSvcConfigHelper([]string{"a", "--operator-sid", "gecersiz"}) != setupUsage || runSvcConfigHelper(nil) != setupUsage {
		t.Error("arguman dogrulamasi")
	}
}

func TestSvcConfigHelperKeepsTokenAndOtherPrefs(t *testing.T) {
	reloads, restarts := helperEnv(t)
	cfg := clientcfg.Config{Token: "zrv_live_abc", ServerAddr: "s:443", EnableTerminal: true, EnableScreen: true, Operators: []string{"S-1-5-21-9-9-9-1"}}
	if err := clientcfg.SaveSystem(cfg); err != nil {
		t.Fatal(err)
	}
	// Yalniz terminal kapatilir: ekran, token, sunucu, onceki operator korunur.
	if got := runSvcConfigHelper([]string{writePatch(t, `{"enable_terminal":false}`), "--operator-sid", testSID}); got != setupOK {
		t.Fatalf("kod %d", got)
	}
	got, _ := clientcfg.LoadFile(clientcfg.SystemPath())
	if got.EnableTerminal || !got.EnableScreen || got.Token != "zrv_live_abc" || got.ServerAddr != "s:443" {
		t.Errorf("birlesim hatali: %+v", got)
	}
	if len(got.Operators) != 2 || got.Operators[0] != "S-1-5-21-9-9-9-1" || got.Operators[1] != testSID {
		t.Errorf("operatorler eklenmeli, silinmemeli: %v", got.Operators)
	}
	if *reloads != 1 || *restarts != 0 {
		t.Errorf("terminal canli uygulanir (reload): reload=%d restart=%d", *reloads, *restarts)
	}
	// Deploy degisince servis yeniden baslatilir.
	if got := runSvcConfigHelper([]string{writePatch(t, `{"enable_deploy":true}`)}); got != setupOK {
		t.Fatal(got)
	}
	if g, _ := clientcfg.LoadFile(clientcfg.SystemPath()); !g.EnableDeploy || g.EnableTerminal {
		t.Errorf("deploy acilmali, terminal kapali kalmali: %+v", g)
	}
	if *restarts != 1 || *reloads != 1 {
		t.Errorf("deploy icin yeniden baslatma: reload=%d restart=%d", *reloads, *restarts)
	}
	// Degisiklik yok: dosya/servis dokunulmaz.
	if got := runSvcConfigHelper([]string{writePatch(t, `{"enable_deploy":true}`)}); got != setupOK || *restarts != 1 {
		t.Errorf("degisiklik yokken servis yeniden baslatilmamali: %d", *restarts)
	}
}

func TestSvcConfigHelperRestartFailure(t *testing.T) {
	_, _ = helperEnv(t)
	svcRestartFn = func() error { return errors.New("x") }
	if got := runSvcConfigHelper([]string{writePatch(t, `{"enable_deploy":true}`)}); got != setupErrService {
		t.Errorf("kod %d", got)
	}
}

func installedApp(t *testing.T) (*App, *[]map[string]bool) {
	t.Helper()
	useTempConfigDir(t)
	withProtector(t, nil)
	old := installedMode
	installedMode = func() bool { return true }
	t.Cleanup(func() { installedMode = old })
	var calls []map[string]bool
	oldApply := applySvcPerms
	applySvcPerms = func(p map[string]bool) error {
		cp := map[string]bool{}
		for k, v := range p {
			cp[k] = v
		}
		calls = append(calls, cp)
		return nil
	}
	t.Cleanup(func() { applySvcPerms = oldApply })
	a := &App{cfg: defaultConfig(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return a, &calls
}

func TestServiceFromResponseCaps(t *testing.T) {
	r := localctl.Response{OK: true, Service: true, Caps: localctl.Caps{TerminalCap: true, ScreenCap: false}, Deploy: true, Connected: true, State: "connected"}
	s := serviceFromResponse(r, nil)
	if !s.Service || !s.TerminalCap || s.ScreenCap || !s.DeployOn || !s.Connected {
		t.Errorf("izin alanlari yanittan gelmeli: %+v", s)
	}
}

func TestInstalledPermFlow(t *testing.T) {
	a, calls := installedApp(t)
	a.svc = ServiceState{Found: true, Service: true, TerminalCap: true, ScreenCap: true}

	// Arayuz durumu servisten okunur.
	if c := a.GetConfig(); c.NoTerminal || c.NoScreen || c.EnableDeploy {
		t.Errorf("GetConfig servis izinlerini yansitmali: %+v", c)
	}
	// Ayni deger: yardimci cagrilmaz (UAC cikmaz).
	if msg := a.UpdateBehavior(false, false, true); msg != "" || len(*calls) != 0 {
		t.Fatalf("ayni degerde UAC olmamali: %q %v", msg, *calls)
	}
	// Terminal kapat: yalniz degisen anahtar yamada.
	if msg := a.UpdateBehavior(true, false, true); msg != "" {
		t.Fatal(msg)
	}
	if len(*calls) != 1 || len((*calls)[0]) != 1 || (*calls)[0][permTerminal] != false {
		t.Errorf("yama yalniz terminal: %v", *calls)
	}
}

func TestInstalledPermUACDeniedRollsBack(t *testing.T) {
	a, calls := installedApp(t)
	a.svc = ServiceState{Found: true, Service: true, TerminalCap: true, ScreenCap: true}
	applySvcPerms = func(p map[string]bool) error { *calls = append(*calls, p); return errors.New("reddedildi") }

	msg := a.UpdateBehavior(true, true, true)
	if msg == "" || !strings.Contains(msg, "reddedildi") {
		t.Fatalf("acik hata beklenir: %q", msg)
	}
	if a.cfg.NoTerminal || a.cfg.NoScreen {
		t.Error("UAC reddinde bellekteki deger degismemeli")
	}
	// Acik surumde deploy izni yok: yalniz terminal istegi yardimciyi bir kez cagirdi.
	if len(*calls) != 1 {
		t.Errorf("cagri sayisi %d", len(*calls))
	}
}

func TestInstalledPermUnknownServiceUsesMirror(t *testing.T) {
	a, calls := installedApp(t)
	// Servis bulunamadi: karsilastirma kullanici ayarindaki yansimayla; tepsi
	// anahtarini degistirmek UAC sorulmasina yol acmaz.
	if msg := a.UpdateBehavior(false, false, false); msg != "" || len(*calls) != 0 {
		t.Fatalf("izin ayni, UAC olmamali: %q %v", msg, *calls)
	}
	if a.cfg.HideToTray {
		t.Error("tepsi ayari kaydedilmeli")
	}
	if msg := a.UpdateBehavior(true, false, false); msg != "" || len(*calls) != 1 {
		t.Fatal(msg, *calls)
	}
	if !a.cfg.NoTerminal {
		t.Error("basari sonrasi yansima guncellenmeli")
	}
}
