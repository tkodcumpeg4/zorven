package main

// Windows kurulumu: Zorven.exe (Setup) kendi kurulumunu yapar (Program Files, Baslat
// menusu, PATH, Program Ekle/Kaldir, arka plan servisi). Bu dosya platformdan
// bagimsiz MANTIGI tasir (yollar, PATH duzenleme, adim sirasi, kaldirma, eski
// kurulum gecisi); isletim sistemi islemleri sysOps arayuzu arkasindadir ve
// birim testlerinde sahtesiyle denenir. Gercek uygulama: setup_windows.go.
//
// Mod ayirimi (tek ikili, uc rol):
//   - Kurulum modu : dosya adi "Setup" iceriyorsa (Zorven-Setup-windows-amd64.exe)
//     ya da --install bayragi verilmisse; kurulu degilse onay sorar.
//   - Normal mod   : Program Files\Zorven\ZorvenDesktop.exe (kurulu) ya da baska bir
//     yerden (tasinabilir). Tasinabilirde gomulu ajan eskisi gibi calisir ve
//     arayuz "Kur" onerir.
//   - Yardimci mod : yukseltilmis alt surec (--zv-install-run, --zv-uninstall-run,
//     --zv-svc-token, --zv-apply-update).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	appDirName = "Zorven"
	// Kurulu masaustu adi CLI (zorven.exe) ile buyuk/kucuk harf duyarsiz CAKISMAMALI:
	// Windows dosya sistemi ikisini ayni dosya sayar ve CLI masaustunun ustune yazilirdi.
	desktopExeName = "ZorvenDesktop.exe"
	cliExeName     = "zorven.exe"
	arpKeyName     = "Zorven"
	publisherName  = "Zorven"

	installFlag      = "--install"
	uninstallFlag    = "--uninstall"
	installRunFlag   = "--zv-install-run"
	uninstallRunFlag = "--zv-uninstall-run"
	svcTokenFlag     = "--zv-svc-token"
	svcConfigFlag    = "--zv-svc-config"
	operatorSIDFlag  = "--operator-sid"

	// permsMarkerName: servis izinleri (terminal/ekran) ilk kurulumda bir kez
	// "tam izinli" kuruldu isareti (ProgramData\zorven altinda). Varsa kurulum/onarim
	// izin bayraklarini TEKRAR vermez: kullanicinin sonradan kapattigi izin korunur.
	permsMarkerName = "desktop-perms-init"

	defaultServer = "zorven.app:443"
)

// Yardimci cikis kodlari (kurulum/kaldirma).
const (
	setupOK         = 0
	setupUsage      = 2
	setupErrDL      = 10 // CLI indirilemedi/dogrulanamadi
	setupErrFiles   = 11 // dosya kopyalama
	setupErrSystem  = 12 // PATH / kisayol / kayit defteri
	setupErrService = 13 // servis kurulamadi/baslatilamadi
	setupErrRemove  = 14 // kaldirma tamamlanamadi
)

// setupError, adimin cikis kodunu tasiyan hata.
type setupError struct {
	Code int
	Err  error
}

func (e *setupError) Error() string { return e.Err.Error() }
func (e *setupError) Unwrap() error { return e.Err }

func wrapSetup(code int, err error) error {
	if err == nil {
		return nil
	}
	return &setupError{Code: code, Err: err}
}

// setupExitCode, hatadan yardimci cikis kodunu cikarir.
func setupExitCode(err error) int {
	if err == nil {
		return setupOK
	}
	var se *setupError
	if errors.As(err, &se) {
		return se.Code
	}
	return 1
}

// setupErrText, yardimci cikis kodunun kullanici metni.
func setupErrText(code int) string {
	switch code {
	case setupErrDL:
		return tm("setup.errDL")
	case setupErrFiles:
		return tm("setup.errFiles")
	case setupErrSystem:
		return tm("setup.errSystem")
	case setupErrService:
		return tm("setup.errService")
	}
	return tm("setup.errGeneric", code)
}

// installPaths, kurulumun hedef yollari.
type installPaths struct {
	ProgramFiles  string // %ProgramFiles%
	ProgramData   string // %ProgramData%
	PublicDesktop string // %PUBLIC%\Desktop
}

func (p installPaths) Dir() string        { return filepath.Join(p.ProgramFiles, appDirName) }
func (p installPaths) DesktopExe() string { return filepath.Join(p.Dir(), desktopExeName) }
func (p installPaths) CLIExe() string     { return filepath.Join(p.Dir(), cliExeName) }
func (p installPaths) StartMenuLnk() string {
	return filepath.Join(p.ProgramData, "Microsoft", "Windows", "Start Menu", "Programs", appDirName+".lnk")
}
func (p installPaths) DesktopLnk() string {
	return filepath.Join(p.PublicDesktop, appDirName+".lnk")
}

// ConfigDir, servis yapilandirmasi (kaldirmada korunur).
func (p installPaths) ConfigDir() string { return filepath.Join(p.ProgramData, "zorven") }

// samePath, Windows yol karsilastirmasi (buyuk/kucuk harf duyarsiz).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// isInstalledExe: exe, kurulu dizindeki ZorvenDesktop.exe mi?
func (p installPaths) isInstalledExe(exe string) bool {
	return samePath(filepath.Dir(exe), p.Dir()) && strings.EqualFold(filepath.Base(exe), desktopExeName)
}

// isSetupName: dosya adi kurulum kipini secer (Zorven-Setup-windows-amd64.exe).
func isSetupName(exe string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(exe)), "setup")
}

// wantsSetup: bu calistirma kurulum akisini baslatmali mi? Kurulu kopya asla
// kurulum kipine girmez (kaldirma/onarim ayri bayrakla).
func wantsSetup(p installPaths, exe string, args []string) bool {
	if p.isInstalledExe(exe) {
		return false
	}
	// Guncelleme yardimcisi / yeniden baslatma / sessiz acilis kurulum acmaz.
	if hasArg(args, updateApplyFlag) || hasArg(args, waitPIDFlag) || hasArg(args, hiddenFlag) {
		return false
	}
	for _, a := range args {
		if a == installFlag {
			return true
		}
	}
	return isSetupName(exe)
}

// hasArg: bayrak var mi.
func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// argValue: "--bayrak deger" cifti.
func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// --- PATH duzenleme (saf fonksiyonlar) ----------------------------------

// pathEntries: noktalivirgulle ayrilmis PATH'i bos olmayan girdilere boler.
func pathEntries(p string) []string {
	var out []string
	for _, e := range strings.Split(p, ";") {
		if strings.TrimSpace(e) != "" {
			out = append(out, e)
		}
	}
	return out
}

func trimDir(s string) string { return strings.TrimRight(strings.TrimSpace(s), `\/`) }

// pathAdd, dir PATH'te yoksa sona ekler. changed=false: zaten vardi.
func pathAdd(path, dir string) (string, bool) {
	ents := pathEntries(path)
	for _, e := range ents {
		if strings.EqualFold(trimDir(e), trimDir(dir)) {
			return path, false
		}
	}
	return strings.Join(append(ents, dir), ";"), true
}

// pathRemove, dir girdisini (varsa) PATH'ten cikarir.
func pathRemove(path, dir string) (string, bool) {
	changed := false
	var kept []string
	for _, e := range pathEntries(path) {
		if strings.EqualFold(trimDir(e), trimDir(dir)) {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	return strings.Join(kept, ";"), changed
}

// --- isletim sistemi arayuzu ---------------------------------------------

type shortcutSpec struct {
	Path, Target, Args, WorkDir, Icon, Desc string
}

type uninstallEntry struct {
	DisplayName, DisplayVersion, Publisher, InstallLocation string
	UninstallString, DisplayIcon                            string
}

// sysOps, kurulumun dokundugu isletim sistemi islemleri.
type sysOps interface {
	MkdirAll(dir string) error
	CopyFile(src, dst string) error
	RemoveFile(path string) error
	RemoveAll(dir string) error
	Exists(path string) bool
	AddMachinePath(dir string) error
	RemoveMachinePath(dir string) error
	CreateShortcut(sc shortcutSpec) error
	SetUninstallEntry(e uninstallEntry) error
	DeleteUninstallEntry() error
	DeleteUserUninstallEntry() error
	// WriteMarker: bos bir isaret dosyasi yazar (dizin yoksa olusturur).
	WriteMarker(path string) error
	// AddOperator: sistem ayarindaki operators listesine SID ekler (silmez).
	AddOperator(sid string) error
	// StopProcesses: gorsel adi verilen surecleri (kendisi haric) sonlandirir.
	StopProcesses(images ...string) error
	// RunCLI: exe'yi gizli konsolla calistirir; cikis kodunu doner (err yalniz
	// baslatma hatasinda).
	RunCLI(exe string, args ...string) (int, error)
	Log(format string, a ...any)
}

// setupParams, kurulum adimlarinin girdisi.
type setupParams struct {
	Self            string // calisan (kaynak) Zorven.exe
	CLISrc          string // indirilmis ve dogrulanmis zorven.exe
	Version         string
	Server          string
	TokenFile       string // bos olabilir (servis kimlik bekler)
	OperatorSID     string // kurulumu baslatan etkilesimli kullanicinin SID'i (bos olabilir)
	DesktopShortcut bool
}

type installer struct {
	sys   sysOps
	paths installPaths
}

// Install, kurulum adimlarini sirayla uygular. CLI zaten indirilmis olmali
// (cevrimdisi hata ONCE, hicbir sey degismeden verilir).
func (in *installer) Install(o setupParams) error {
	dir := in.paths.Dir()
	s := in.sys
	// 0. Ad cakismasi: hicbir sey degismeden once, sessiz ustune yazma yok.
	if err := checkNameClash(in.paths.DesktopExe(), in.paths.CLIExe()); err != nil {
		return wrapSetup(setupErrFiles, err)
	}
	// 1. Eski surumun dosyalarini serbest birak.
	if s.Exists(in.paths.CLIExe()) {
		_, _ = s.RunCLI(in.paths.CLIExe(), "service", "stop")
	}
	_ = s.StopProcesses(desktopExeName, cliExeName)
	// 2. Dosyalar.
	if err := s.MkdirAll(dir); err != nil {
		return wrapSetup(setupErrFiles, err)
	}
	if !samePath(o.Self, in.paths.DesktopExe()) {
		if err := s.CopyFile(o.Self, in.paths.DesktopExe()); err != nil {
			return wrapSetup(setupErrFiles, err)
		}
	}
	if err := s.CopyFile(o.CLISrc, in.paths.CLIExe()); err != nil {
		return wrapSetup(setupErrFiles, err)
	}
	// 3. PATH, kisayollar, kayit.
	if err := s.AddMachinePath(dir); err != nil {
		return wrapSetup(setupErrSystem, err)
	}
	exe := in.paths.DesktopExe()
	// Bozuk eski kurulum (0.3.4: kisayol/ARP CLI'ye isaret ediyordu): eski kayitlari
	// silip yeniden yaz. Yeni kisayol/kayit her zaman ZorvenDesktop.exe'yi gosterir.
	for _, p := range []string{in.paths.StartMenuLnk(), in.paths.DesktopLnk()} {
		if s.Exists(p) {
			_ = s.RemoveFile(p)
		}
	}
	_ = s.DeleteUninstallEntry()
	lnk := shortcutSpec{Path: in.paths.StartMenuLnk(), Target: exe, WorkDir: dir, Icon: exe, Desc: "Zorven"}
	if err := s.CreateShortcut(lnk); err != nil {
		return wrapSetup(setupErrSystem, err)
	}
	if o.DesktopShortcut {
		lnk.Path = in.paths.DesktopLnk()
		if err := s.CreateShortcut(lnk); err != nil {
			s.Log("masaustu kisayolu olusturulamadi: %v", err)
		}
	}
	if err := s.SetUninstallEntry(uninstallEntry{
		DisplayName: appDirName, DisplayVersion: o.Version, Publisher: publisherName,
		InstallLocation: dir, UninstallString: `"` + exe + `" ` + uninstallFlag, DisplayIcon: exe,
	}); err != nil {
		return wrapSetup(setupErrSystem, err)
	}
	// 4. Servis (tam izinli kip; token yoksa kimlik bekler).
	return in.installService(o)
}

// checkNameClash: masaustu ve CLI ikilisi ayni dizinde buyuk/kucuk harf duyarsiz
// ayni ada sahipse acik hata doner (biri digerinin ustune yazilirdi).
func checkNameClash(desktopExe, cliExe string) error {
	if strings.EqualFold(filepath.Base(desktopExe), filepath.Base(cliExe)) &&
		strings.EqualFold(filepath.Dir(desktopExe), filepath.Dir(cliExe)) {
		return fmt.Errorf("masaustu ve CLI dosya adlari cakisiyor: %s / %s",
			filepath.Base(desktopExe), filepath.Base(cliExe))
	}
	return nil
}

// installService: servisi kurar (idempotent) ve baslatir. Ilk kurulumda (isaret
// dosyasi yok) servis terminal+ekran izinleriyle ("tam izinli") kurulur; sonraki
// kurulum/onarimda bayrak verilmez, boylece kullanicinin kapattigi izin ezilmez
// (`service install` izinleri yalniz acar, hic kapatmaz). Zorven Apps (enable_deploy)
// kurulumda KAPALI kalir; Ayarlar'daki anahtara baglidir (Docker gerektirir).
func (in *installer) installService(o setupParams) error {
	args := []string{"service", "install", "--allow-no-token"}
	marker := filepath.Join(in.paths.ConfigDir(), permsMarkerName)
	first := !in.sys.Exists(marker)
	if first {
		args = append(args, "--enable-terminal", "--enable-screen")
	}
	if o.Server != "" {
		args = append(args, "--server", o.Server)
	}
	if o.TokenFile != "" {
		args = append(args, "--token-file", o.TokenFile)
	}
	cli := in.paths.CLIExe()
	if code, err := in.sys.RunCLI(cli, args...); err != nil || code != 0 {
		return wrapSetup(setupErrService, fmt.Errorf("service install: kod %d err %v", code, err))
	}
	if first {
		if err := in.sys.WriteMarker(marker); err != nil {
			in.sys.Log("izin isareti yazilamadi: %v", err)
		}
	}
	// Masaustu kontrol kanalini tam yetkiyle kullanacak kullanici (silinmez, eklenir).
	if o.OperatorSID != "" {
		if err := in.sys.AddOperator(o.OperatorSID); err != nil {
			return wrapSetup(setupErrService, fmt.Errorf("operator eklenemedi: %w", err))
		}
	}
	if code, err := in.sys.RunCLI(cli, "service", "start"); err != nil || code != 0 {
		return wrapSetup(setupErrService, fmt.Errorf("service start: kod %d err %v", code, err))
	}
	return nil
}

// Repair: servisi yeniden kurar/baslatir (kurulu dizinden; dosyalara dokunmaz).
func (in *installer) Repair(server, tokenFile, operatorSID string) error {
	if !in.sys.Exists(in.paths.CLIExe()) {
		return wrapSetup(setupErrFiles, errors.New("zorven.exe bulunamadi"))
	}
	return in.installService(setupParams{Server: server, TokenFile: tokenFile, OperatorSID: operatorSID})
}

// Uninstall, kurulumu geri alir. Kullanici ayarlari (%AppData%\zorven) ve
// ProgramData yapilandirmasi KORUNUR.
func (in *installer) Uninstall() error {
	s := in.sys
	cli := in.paths.CLIExe()
	if s.Exists(cli) {
		// 4 = servis kayitli degil (zorven service status kodlari).
		if code, _ := s.RunCLI(cli, "service", "status"); code != 4 {
			_, _ = s.RunCLI(cli, "service", "stop")
			if code, err := s.RunCLI(cli, "service", "uninstall"); err != nil || code != 0 {
				return wrapSetup(setupErrService, fmt.Errorf("service uninstall: kod %d err %v", code, err))
			}
		}
	}
	_ = s.StopProcesses(desktopExeName, cliExeName)
	for _, p := range []string{in.paths.StartMenuLnk(), in.paths.DesktopLnk()} {
		if s.Exists(p) {
			if err := s.RemoveFile(p); err != nil {
				s.Log("kisayol silinemedi %s: %v", p, err)
			}
		}
	}
	if err := s.RemoveMachinePath(in.paths.Dir()); err != nil {
		s.Log("PATH girdisi silinemedi: %v", err)
	}
	if err := s.DeleteUninstallEntry(); err != nil {
		s.Log("kayit silinemedi: %v", err)
	}
	if s.Exists(in.paths.Dir()) {
		if err := s.RemoveAll(in.paths.Dir()); err != nil {
			return wrapSetup(setupErrRemove, err)
		}
	}
	return nil
}

// --- eski (kullanici duzeyi) kurulumdan gecis ----------------------------

// legacyPaths: installer/install.ps1'in LOCALAPPDATA kurulumu.
type legacyPaths struct {
	LocalAppData string
	AppData      string
	UserDesktop  string
}

func (l legacyPaths) Dir() string { return filepath.Join(l.LocalAppData, "Programs", appDirName) }
func (l legacyPaths) StartMenuLnk() string {
	return filepath.Join(l.AppData, "Microsoft", "Windows", "Start Menu", "Programs", appDirName+".lnk")
}
func (l legacyPaths) DesktopLnk() string { return filepath.Join(l.UserDesktop, appDirName+".lnk") }

// legacyInstalled: eski kurulumun izi var mi (dizin ya da kisayol).
func legacyInstalled(s sysOps, l legacyPaths) bool {
	return s.Exists(filepath.Join(l.Dir(), desktopExeName)) || s.Exists(l.StartMenuLnk()) || s.Exists(l.DesktopLnk())
}

// migrateLegacy, eski kullanici duzeyi kurulumu temizler: kisayollar, Program
// Ekle/Kaldir kaydi, dizin. self eski dizindeyse (kendi uzerinden calisiyor)
// dizin silinmez. Kullanici ayarlari (%AppData%\zorven) dokunulmaz.
func migrateLegacy(s sysOps, l legacyPaths, self string) (removedDir bool) {
	for _, p := range []string{l.StartMenuLnk(), l.DesktopLnk()} {
		if s.Exists(p) {
			_ = s.RemoveFile(p)
		}
	}
	_ = s.DeleteUserUninstallEntry()
	if !s.Exists(l.Dir()) {
		return false
	}
	if samePath(filepath.Dir(self), l.Dir()) {
		s.Log("eski kurulum dizini calisan kopya oldugu icin silinmedi: %s", l.Dir())
		return false
	}
	if err := s.RemoveAll(l.Dir()); err != nil {
		s.Log("eski dizin silinemedi: %v", err)
		return false
	}
	return true
}

// --- surum kilidi ---------------------------------------------------------

// cliVersionOK: guncelleme sirasinda CLI manifest surumu masaustu hedefiyle
// ayni olmali (CLI+masaustu birlikte yayinlanir, ayri surumde kalmaz).
func cliVersionOK(desktopTarget, cliManifest string) bool {
	return desktopTarget != "" && desktopTarget == cliManifest
}

// installedMode: calisan kopya Program Files kurulumu mu (yalniz Windows'ta
// true olabilir). Testlerde degistirilebilir.
var installedMode = func() bool { return false }

// legacyFound: eski kullanici duzeyi (LOCALAPPDATA) kurulum izi var mi.
var legacyFound = func() bool { return false }

// replaceFile, dst'yi data ile degistirir: gecici dosya + yeniden adlandirma.
// dst calisiyorsa eski dosya ".old"a tasinir (Windows calisan exe'yi yeniden
// adlandirabilir) ve basari halinde silinmeye calisilir.
func replaceFile(dst string, data []byte) error {
	tmp, old := dst+".new", dst+".old"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	_ = os.Remove(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst)
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(old)
	return nil
}
