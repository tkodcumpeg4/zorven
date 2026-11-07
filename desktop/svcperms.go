package main

// Kurulu (Program Files) Windows kipinde servis izinleri ve operator kimligi.
//
// Kurulu kipte gomulu ajan calismaz; terminal/ekran/Zorven Apps izinleri servisin
// SISTEM ayar dosyasindadir (yalniz yonetici yazabilir, kullanici OKUYAMAZ: token
// de orada). Bu yuzden:
//   - izin DEGISTIRME: yukseltilmis yardimci (--zv-svc-config, tek UAC onayi);
//   - izin OKUMA: servisin localctl "status" yaniti (Caps + Deploy), dosya degil.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// Servis izin anahtarlari (sistem ayarindaki enable_* alanlari).
const (
	permTerminal = "enable_terminal"
	permScreen   = "enable_screen"
	permDeploy   = "enable_deploy"
)

// operatorSIDRe: etkilesimli kullanici hesabi SID'i (yerel/alan S-1-5-21-..., Azure AD
// S-1-12-1-...). SYSTEM, Administrators gibi iyi bilinen SID'ler kabul edilmez.
var operatorSIDRe = regexp.MustCompile(`^S-1-(?:5-21|12-1)(?:-[0-9]{1,10}){2,6}$`)

// validOperatorSID, gecerli bicimdeyse SID'i, degilse "" doner.
func validOperatorSID(s string) string {
	s = strings.TrimSpace(s)
	if operatorSIDRe.MatchString(s) {
		return s
	}
	return ""
}

// operatorParam, yukseltilmis yardimciya gececek " --operator-sid S" parcasi.
// SID YUKSELTME ONCESI surecte okunur: over-the-shoulder UAC'ta (baska yonetici
// hesabi) yardimci kendi SID'ini yazmasin. Okunamazsa bos.
func operatorParam() string {
	if sid := validOperatorSID(currentUserSID()); sid != "" {
		return " " + operatorSIDFlag + " " + sid
	}
	return ""
}

// addSystemOperator, sistem ayarindaki operators listesine SID ekler (token ve
// diger ayarlara dokunmaz; listeden silme yoktur).
func addSystemOperator(sid string) error {
	if validOperatorSID(sid) == "" {
		return errors.New("gecersiz SID")
	}
	cfg, _ := clientcfg.LoadFile(clientcfg.SystemPath())
	if !cfg.AddOperator(sid) {
		return nil
	}
	return clientcfg.SaveSystem(cfg)
}

// validatePermPatch: yama bos olamaz ve yalniz bilinen izin anahtarlarini icerebilir.
func validatePermPatch(m map[string]bool) error {
	if len(m) == 0 {
		return errors.New("bos izin degisikligi")
	}
	for k := range m {
		switch k {
		case permTerminal, permScreen, permDeploy:
		default:
			return errors.New("bilinmeyen izin anahtari: " + k)
		}
	}
	return nil
}

// applyPermPatch, yamayi cfg'ye uygular. Yalniz izin alanlarina dokunur (token,
// sunucu, operatorler korunur). deployChanged: enable_deploy degeri degisti.
func applyPermPatch(cfg *clientcfg.Config, m map[string]bool) (changed, deployChanged bool) {
	set := func(dst *bool, key string) bool {
		if v, ok := m[key]; ok && *dst != v {
			*dst = v
			return true
		}
		return false
	}
	t := set(&cfg.EnableTerminal, permTerminal)
	s := set(&cfg.EnableScreen, permScreen)
	deployChanged = set(&cfg.EnableDeploy, permDeploy)
	return t || s || deployChanged, deployChanged
}

// Calisan servise degisikligi uygulama adimlari (testlerde degistirilir).
var (
	// svcReloadFn: canli uygulanabilen izinler icin servise "reload".
	svcReloadFn = func() error {
		_, err := localctl.Call(localctl.ServiceEndpoint(), localctl.Request{Op: "reload"})
		return err
	}
	// svcRestartFn: enable_deploy canli uygulanmadigindan servisi yeniden baslatir.
	svcRestartFn = func() error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		code, err := runHidden(filepath.Join(filepath.Dir(exe), cliExeName), "service", "restart")
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("service restart: kod %d", code)
		}
		return nil
	}
)

// runSvcConfigHelper, yukseltilmis yardimci: "--zv-svc-config <json-dosyasi>
// [--operator-sid S]". Dosya {"enable_terminal":b,"enable_screen":b,
// "enable_deploy":b} bicimindedir (bilinmeyen anahtar reddedilir). Yalniz bu izinleri
// sistem ayarina birlestirir (token'a dokunmaz); degisiklik calisan servise
// uygulanir: deploy degistiyse servis yeniden baslatilir, degilse "reload".
func runSvcConfigHelper(args []string) int {
	if len(args) != 1 && len(args) != 3 {
		return setupUsage
	}
	op := ""
	if len(args) == 3 {
		if args[1] != operatorSIDFlag {
			return setupUsage
		}
		if op = validOperatorSID(args[2]); op == "" {
			return setupUsage
		}
	}
	raw, err := os.ReadFile(args[0])
	_ = os.Remove(args[0])
	if err != nil {
		return setupUsage
	}
	var patch map[string]bool
	if json.Unmarshal(raw, &patch) != nil || validatePermPatch(patch) != nil {
		return setupUsage
	}
	cfg, _ := clientcfg.LoadFile(clientcfg.SystemPath())
	changed, deployChanged := applyPermPatch(&cfg, patch)
	opAdded := cfg.AddOperator(op)
	if !changed && !opAdded {
		return setupOK
	}
	if err := clientcfg.SaveSystem(cfg); err != nil {
		return setupErrSystem
	}
	if deployChanged {
		if err := svcRestartFn(); err != nil {
			return setupErrService
		}
	} else if changed {
		// Servis calismiyorsa reload hatasi onemsenmez: acilista dosyayi okur.
		_ = svcReloadFn()
	}
	return setupOK
}

// applySvcPerms, izin yamasini yukseltilmis yardimciyla uygular (tek UAC onayi).
// Kullanici UAC'i reddederse hata doner. Testlerde degistirilir.
var applySvcPerms = realApplySvcPerms

func realApplySvcPerms(patch map[string]bool) error {
	if err := validatePermPatch(patch); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(patch)
	tf, err := writePermPatchFile(body)
	if err != nil {
		return err
	}
	defer os.Remove(tf)
	code, err := runElevatedWait(exe, svcConfigFlag+` "`+tf+`"`+operatorParam(), 3*time.Minute)
	if err != nil {
		return err
	}
	if code != setupOK {
		return errors.New(setupErrText(code))
	}
	return nil
}

// writePermPatchFile, yamayi kullanicinin onbellek dizinine gecici dosya olarak yazar.
func writePermPatchFile(body []byte) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	p := filepath.Join(dir, "zorven", fmt.Sprintf("svc-perms-%d-%d.json", os.Getpid(), time.Now().UnixNano()))
	if err := clientcfg.WriteSecretFile(p, body); err != nil {
		return "", err
	}
	return p, nil
}

// svcPerms, kurulu kipte servisin gecerli izinleri. Known=false: servis bulunamadi
// ya da durumu okunamadi (izinler bilinmiyor).
type svcPerms struct {
	Terminal, Screen, Deploy, Known bool
}

// installedPerms, kurulu kipte son servis durumundan izinleri verir.
func (a *App) installedPerms() svcPerms {
	if !installedMode() {
		return svcPerms{}
	}
	a.mu.Lock()
	s := a.svc
	a.mu.Unlock()
	if !s.Found || !s.Service {
		return svcPerms{}
	}
	return svcPerms{Terminal: s.TerminalCap, Screen: s.ScreenCap, Deploy: s.DeployOn, Known: true}
}

// permBaseline, degisiklik karsilastirmasi icin gecerli izinler: servis biliniyorsa
// onun durumu, degilse kullanici ayarindaki yansima (son basarili degisiklik).
func (a *App) permBaseline() svcPerms {
	if p := a.installedPerms(); p.Known {
		return p
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return svcPerms{Terminal: !a.cfg.NoTerminal, Screen: !a.cfg.NoScreen, Deploy: a.cfg.EnableDeploy}
}

// setServicePerms, kurulu kipte izin degisikligini yukseltilmis yardimciyla uygular.
// Zaten gecerli olan degerler yamadan cikarilir; kalan yoksa UAC sorulmaz. UAC
// reddi/hata halinde bellekteki hicbir sey degismez ve hata metni doner. Basarida
// kullanici ayarindaki yansima da guncellenir (servis okunamazken karsilastirma icin).
func (a *App) setServicePerms(want map[string]bool) string {
	cur := a.permBaseline()
	have := map[string]bool{permTerminal: cur.Terminal, permScreen: cur.Screen, permDeploy: cur.Deploy}
	patch := map[string]bool{}
	for k, v := range want {
		if have[k] != v {
			patch[k] = v
		}
	}
	if len(patch) == 0 {
		return ""
	}
	if err := applySvcPerms(patch); err != nil {
		a.log.Warn("servis izni degistirilemedi", "hata", err)
		return tm("perm.applyFail", err.Error())
	}
	a.mu.Lock()
	if v, ok := patch[permTerminal]; ok {
		a.cfg.NoTerminal = !v
	}
	if v, ok := patch[permScreen]; ok {
		a.cfg.NoScreen = !v
	}
	if v, ok := patch[permDeploy]; ok {
		a.cfg.EnableDeploy = v
	}
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		a.log.Warn("izin yansimasi kaydedilemedi", "hata", err)
	}
	a.serviceLoopTick()
	return ""
}
