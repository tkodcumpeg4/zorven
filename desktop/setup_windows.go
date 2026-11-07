//go:build windows

package main

// Windows kurulumunun gercek uygulamasi: sysOps (dosya, HKLM PATH, kisayol,
// Program Ekle/Kaldir), UAC akislari ve MessageBox onaylari.
//
// Kisayol (.lnk) yontemi: gizli PowerShell + WScript.Shell. IShellLinkW COM
// dogrudan Go'dan cagrilabilir ama go-ole benzeri bir bagimlilik ya da elle
// vtable kodu gerektirir; PowerShell tum desteklenen Windows surumlerinde var,
// bagimlilik eklemez. Yollar komut satirina degil ORTAM DEGISKENLERINE konur
// (alinti/enjeksiyon sorunu yok) ve konsol penceresi acilmaz (HideWindow).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/desktop/autostart"
)

const (
	arpPath     = `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + arpKeyName
	machineEnv  = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	mbYesNo     = 0x4
	mbIconQ     = 0x20
	mbIconErr   = 0x10
	mbIconInfo  = 0x40
	idYes       = 6
	createNoWin = 0x08000000
)

// realPaths, bu makinenin Program Files / ProgramData / Public Desktop yollari.
func realPaths() installPaths {
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	return installPaths{
		ProgramFiles:  env("ProgramFiles", `C:\Program Files`),
		ProgramData:   env("ProgramData", `C:\ProgramData`),
		PublicDesktop: filepath.Join(env("PUBLIC", `C:\Users\Public`), "Desktop"),
	}
}

func realLegacyPaths() legacyPaths {
	desk := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	return legacyPaths{
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		AppData:      os.Getenv("APPDATA"),
		UserDesktop:  desk,
	}
}

func init() {
	installedMode = func() bool {
		exe, err := os.Executable()
		return err == nil && realPaths().isInstalledExe(exe)
	}
	legacyFound = func() bool { return legacyInstalled(&winSys{}, realLegacyPaths()) }
}

// winSys, sysOps'un gercek uygulamasi.
type winSys struct{}

func (winSys) Log(format string, a ...any) {
	dir := realPaths().ConfigDir()
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "setup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

func setupLogPath() string { return filepath.Join(realPaths().ConfigDir(), "setup.log") }

func (winSys) MkdirAll(dir string) error  { return os.MkdirAll(dir, 0o755) }
func (winSys) RemoveFile(p string) error  { return os.Remove(p) }
func (winSys) RemoveAll(dir string) error { return removeAllRetry(dir) }
func (winSys) Exists(p string) bool       { _, err := os.Stat(p); return err == nil }

func (winSys) WriteMarker(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o644)
}
func (winSys) AddOperator(sid string) error { return addSystemOperator(sid) }

// removeAllRetry: yeni kapanan sureclerin dosya kilitleri birkac saniye surebilir.
func removeAllRetry(dir string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

// CopyFile: gecici dosyaya yazip yeniden adlandirir; hedef calisiyorsa eskiyi
// .old'a tasir (Windows calisan dosyayi yeniden adlandirmaya izin verir).
func (winSys) CopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return replaceFile(dst, data)
}

func (winSys) AddMachinePath(dir string) error {
	return editMachinePath(func(p string) (string, bool) { return pathAdd(p, dir) })
}
func (winSys) RemoveMachinePath(dir string) error {
	return editMachinePath(func(p string) (string, bool) { return pathRemove(p, dir) })
}

func editMachinePath(edit func(string) (string, bool)) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, machineEnv, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, typ, err := k.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	next, changed := edit(cur)
	if !changed {
		return nil
	}
	if typ == registry.SZ {
		err = k.SetStringValue("Path", next)
	} else {
		err = k.SetExpandStringValue("Path", next) // varsayilan tur: %SystemRoot% genislemesi korunur
	}
	if err != nil {
		return err
	}
	broadcastEnvChange()
	return nil
}

// broadcastEnvChange: acik pencerelere ortam degisti bilgisini yollar.
func broadcastEnvChange() {
	env, _ := windows.UTF16PtrFromString("Environment")
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var res uintptr
	// HWND_BROADCAST=0xffff, WM_SETTINGCHANGE=0x1A, SMTO_ABORTIFHUNG=2
	_, _, _ = proc.Call(0xffff, 0x1A, 0, uintptr(unsafe.Pointer(env)), 2, 3000, uintptr(unsafe.Pointer(&res)))
}

const lnkScript = `$ws = New-Object -ComObject WScript.Shell
$d = Split-Path -Parent $env:ZV_LNK
New-Item -ItemType Directory -Force -Path $d | Out-Null
$s = $ws.CreateShortcut($env:ZV_LNK)
$s.TargetPath = $env:ZV_TARGET
$s.Arguments = $env:ZV_ARGS
$s.WorkingDirectory = $env:ZV_WORKDIR
$s.IconLocation = $env:ZV_ICON
$s.Description = $env:ZV_DESC
$s.Save()`

func (w winSys) CreateShortcut(sc shortcutSpec) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", lnkScript)
	cmd.Env = append(os.Environ(),
		"ZV_LNK="+sc.Path, "ZV_TARGET="+sc.Target, "ZV_ARGS="+sc.Args,
		"ZV_WORKDIR="+sc.WorkDir, "ZV_ICON="+sc.Icon, "ZV_DESC="+sc.Desc)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWin}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kisayol: %v: %s", err, out)
	}
	if !w.Exists(sc.Path) {
		return errors.New("kisayol olusmadi: " + sc.Path)
	}
	return nil
}

func (winSys) SetUninstallEntry(e uninstallEntry) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, arpPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for name, v := range map[string]string{
		"DisplayName": e.DisplayName, "DisplayVersion": e.DisplayVersion, "Publisher": e.Publisher,
		"InstallLocation": e.InstallLocation, "UninstallString": e.UninstallString, "DisplayIcon": e.DisplayIcon,
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return nil
}

func (winSys) DeleteUninstallEntry() error {
	err := registry.DeleteKey(registry.LOCAL_MACHINE, arpPath)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func (winSys) DeleteUserUninstallEntry() error {
	err := registry.DeleteKey(registry.CURRENT_USER, arpPath)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func (winSys) StopProcesses(images ...string) error {
	for _, img := range images {
		cmd := exec.Command("taskkill", "/F", "/IM", img, "/FI", "PID ne "+strconv.Itoa(os.Getpid()))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWin}
		_ = cmd.Run() // surec yoksa 128 doner; hata sayilmaz
	}
	time.Sleep(700 * time.Millisecond)
	return nil
}

func (winSys) RunCLI(exe string, args ...string) (int, error) { return runHidden(exe, args...) }

// runHidden, konsol penceresi acmadan komutu calistirip cikis kodunu doner.
func runHidden(exe string, args ...string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWin}
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func msgBox(text, caption string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(caption)
	r, _ := windows.MessageBox(0, t, c, flags)
	return int(r)
}

func setupLang() {
	cfg, _ := loadConfig()
	i18n.Set(i18n.Detect(cfg.Language))
}

// handleSetupCommands, kurulum/kaldirma/yardimci bayraklarini isler. handled=true
// ise surec code ile cikmali (Wails baslatilmaz).
func handleSetupCommands(args []string) (handled bool, code int) {
	if len(args) > 0 {
		switch args[0] {
		case installRunFlag:
			return true, runInstallHelper(args[1:])
		case uninstallRunFlag:
			return true, runUninstallHelper(args[1:])
		case svcTokenFlag:
			return true, runSvcTokenHelper(args[1:])
		case svcConfigFlag:
			return true, runSvcConfigHelper(args[1:])
		}
	}
	self, err := os.Executable()
	if err != nil {
		return false, 0
	}
	if hasArg(args, uninstallFlag) {
		return true, runUninstallLauncher(self)
	}
	if wantsSetup(realPaths(), self, args) {
		return true, runSetupLauncher(self)
	}
	return false, 0
}

// runSetupLauncher: yukseltilmemis on yuz. Onay sorar, UAC ile yardimciyi calistirir,
// eski kurulumu tasir, kurulu kopyayi normal kullanici olarak baslatir.
func runSetupLauncher(self string) int {
	setupLang()
	p := realPaths()
	if msgBox(tm("setup.confirm", p.Dir()), tm("setup.title"), mbYesNo|mbIconQ) != idYes {
		return 0
	}
	desk := msgBox(tm("setup.desktopShortcut"), tm("setup.title"), mbYesNo|mbIconQ) == idYes

	cfg, _ := loadConfig()
	server := cfg.ServerAddr
	if !svcServerRe.MatchString(server) {
		server = defaultServer
	}
	params := installRunFlag + " --server " + server
	params += operatorParam()
	if desk {
		params += " --desktop-shortcut"
	}
	tf := ""
	if svcTokenRe.MatchString(cfg.Token) {
		if f, err := writeInstallTokenFile(cfg.Token); err == nil {
			tf = f
			params += ` --token-file "` + f + `"`
		}
	}
	code, err := runElevatedWait(self, params, 15*time.Minute)
	if tf != "" {
		_ = os.Remove(tf)
	}
	if err != nil {
		msgBox(tm("setup.fail", err.Error(), setupLogPath()), tm("setup.title"), mbIconErr)
		return 1
	}
	if code != setupOK && code != setupErrService {
		msgBox(tm("setup.fail", setupErrText(code), setupLogPath()), tm("setup.title"), mbIconErr)
		return code
	}
	if code == setupErrService {
		msgBox(tm("setup.fail", setupErrText(code), setupLogPath()), tm("setup.title"), mbIconErr)
	}
	// Eski (kullanici duzeyi) kurulumu temizle, sonra kurulu kopyayi baslat.
	migrateLegacy(&winSys{}, realLegacyPaths(), self)
	cmd := exec.Command(p.DesktopExe())
	cmd.Dir = p.Dir()
	if err := cmd.Start(); err != nil {
		return 1
	}
	return 0
}

// runInstallHelper: yukseltilmis kurulum. Bayraklar: --server S [--desktop-shortcut]
// [--token-file F] [--repair].
func runInstallHelper(args []string) int {
	setupLang()
	w := &winSys{}
	p := realPaths()
	in := &installer{sys: w, paths: p}
	server := argValue(args, "--server")
	if !svcServerRe.MatchString(server) {
		server = defaultServer
	}
	tf := argValue(args, "--token-file")
	op := validOperatorSID(argValue(args, operatorSIDFlag))
	if hasArg(args, "--repair") {
		err := in.Repair(server, tf, op)
		w.Log("onarim: %v", err)
		return setupExitCode(err)
	}
	self, err := os.Executable()
	if err != nil {
		return setupErrFiles
	}
	// CLI'yi ONCE indir: cevrimdisiysa hicbir sey degismeden acik hata.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cliPath, cliVer, err := provisionCLI(ctx, server)
	if err != nil {
		w.Log("CLI indirilemedi: %v", err)
		return setupErrDL
	}
	defer os.Remove(cliPath)
	w.Log("kurulum basladi: surum %s, cli %s, sunucu %s", version, cliVer, server)
	err = in.Install(setupParams{
		Self: self, CLISrc: cliPath, Version: version, Server: server,
		TokenFile: tf, OperatorSID: op, DesktopShortcut: hasArg(args, "--desktop-shortcut"),
	})
	w.Log("kurulum bitti: %v", err)
	return setupExitCode(err)
}

// runUninstallLauncher: yukseltilmemis on yuz. Calisan kopya kurulu dizindeyse
// kendini temp'e kopyalayip oradan yukseltir (calisan exe silinemez).
func runUninstallLauncher(self string) int {
	setupLang()
	p := realPaths()
	if msgBox(tm("un.confirm", userConfigDirHint(), p.ConfigDir()), tm("setup.title"), mbYesNo|mbIconQ) != idYes {
		return 0
	}
	// Bu kullanicinin otomatik baslatma kaydi (HKCU) yukseltilmis surecte gorunmez.
	if m, err := autostart.New(); err == nil {
		_ = m.Disable()
	}
	helper := self
	if samePath(filepath.Dir(self), p.Dir()) {
		tmp, err := copyTemp(self)
		if err != nil {
			msgBox(tm("un.fail", err.Error()), tm("setup.title"), mbIconErr)
			return 1
		}
		helper = tmp
	}
	_, err := runElevatedWait(helper, uninstallRunFlag+` "`+helper+`"`, 10*time.Minute)
	if err != nil {
		msgBox(tm("un.fail", err.Error()), tm("setup.title"), mbIconErr)
		return 1
	}
	return 0
}

// runUninstallHelper: yukseltilmis kaldirma; sonucu kendisi bildirir.
func runUninstallHelper(args []string) int {
	setupLang()
	w := &winSys{}
	p := realPaths()
	err := (&installer{sys: w, paths: p}).Uninstall()
	w.Log("kaldirma: %v", err)
	if err != nil {
		text := err.Error()
		switch setupExitCode(err) {
		case setupErrService:
			text = tm("un.errService")
		case setupErrRemove:
			text = tm("un.errRemove")
		}
		msgBox(tm("un.fail", text), tm("setup.title"), mbIconErr)
		return setupExitCode(err)
	}
	// Temp kopyasi ise kendini gecikmeyle sil.
	if self, e := os.Executable(); e == nil && len(args) > 0 && samePath(self, args[0]) && !samePath(filepath.Dir(self), p.Dir()) {
		c := exec.Command("cmd", "/c", `ping -n 4 127.0.0.1 >nul & del /f /q "`+self+`"`)
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWin}
		_ = c.Start()
	}
	msgBox(tm("un.done", userConfigDirHint(), p.ConfigDir()), tm("setup.title"), mbIconInfo)
	return 0
}

// userConfigDirHint, korunan kullanici ayar dizininin gosterim yolu.
func userConfigDirHint() string { return filepath.Join(os.Getenv("APPDATA"), "zorven") }
