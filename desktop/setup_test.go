package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSys, sysOps'un kayit tutan sahtesi.
type fakeSys struct {
	calls   []string
	files   map[string]bool
	failOn  map[string]error // "islem" ya da "islem:arg" -> hata
	cliCode map[string]int   // "service status" -> cikis kodu
	path    string           // makine PATH'i
	entry   *uninstallEntry
}

func newFake() *fakeSys {
	return &fakeSys{files: map[string]bool{}, failOn: map[string]error{}, cliCode: map[string]int{}}
}

func (f *fakeSys) rec(op string, args ...string) error {
	f.calls = append(f.calls, strings.TrimSpace(op+" "+strings.Join(args, " ")))
	if e, ok := f.failOn[op]; ok {
		return e
	}
	return nil
}
func (f *fakeSys) MkdirAll(d string) error { return f.rec("mkdir", d) }
func (f *fakeSys) CopyFile(s, d string) error {
	if err := f.rec("copy", s, d); err != nil {
		return err
	}
	f.files[d] = true
	return nil
}
func (f *fakeSys) RemoveFile(p string) error { delete(f.files, p); return f.rec("rmfile", p) }
func (f *fakeSys) RemoveAll(d string) error {
	for p := range f.files {
		if strings.HasPrefix(p, d) {
			delete(f.files, p)
		}
	}
	return f.rec("rmall", d)
}
func (f *fakeSys) Exists(p string) bool {
	if f.files[p] {
		return true
	}
	for q := range f.files {
		if strings.HasPrefix(q, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
func (f *fakeSys) AddMachinePath(d string) error {
	f.path, _ = pathAdd(f.path, d)
	return f.rec("pathadd", d)
}
func (f *fakeSys) RemoveMachinePath(d string) error {
	f.path, _ = pathRemove(f.path, d)
	return f.rec("pathrm", d)
}
func (f *fakeSys) CreateShortcut(sc shortcutSpec) error {
	if err := f.rec("lnk", sc.Path, sc.Target); err != nil {
		return err
	}
	f.files[sc.Path] = true
	return nil
}
func (f *fakeSys) SetUninstallEntry(e uninstallEntry) error {
	f.entry = &e
	return f.rec("arp+")
}
func (f *fakeSys) DeleteUninstallEntry() error     { f.entry = nil; return f.rec("arp-") }
func (f *fakeSys) DeleteUserUninstallEntry() error { return f.rec("userarp-") }
func (f *fakeSys) StopProcesses(i ...string) error { return f.rec("kill", i...) }
func (f *fakeSys) RunCLI(exe string, args ...string) (int, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, "cli "+key)
	if code, ok := f.cliCode[key]; ok {
		return code, nil
	}
	for k, code := range f.cliCode { // onek eslesmesi (service install ...)
		if strings.HasPrefix(key, k) {
			return code, nil
		}
	}
	return 0, nil
}
func (f *fakeSys) Log(string, ...any) {}
func (f *fakeSys) WriteMarker(p string) error {
	if err := f.rec("marker", p); err != nil {
		return err
	}
	f.files[p] = true
	return nil
}
func (f *fakeSys) AddOperator(sid string) error { return f.rec("operator", sid) }

func (f *fakeSys) has(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}
func (f *fakeSys) index(prefix string) int {
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func testPaths() installPaths {
	return installPaths{
		ProgramFiles:  filepath.Join("C:", "Program Files"),
		ProgramData:   filepath.Join("C:", "ProgramData"),
		PublicDesktop: filepath.Join("C:", "Users", "Public", "Desktop"),
	}
}

func TestInstallPaths(t *testing.T) {
	p := testPaths()
	if p.Dir() != filepath.Join("C:", "Program Files", "Zorven") {
		t.Fatalf("dizin: %s", p.Dir())
	}
	if !strings.HasSuffix(p.StartMenuLnk(), filepath.Join("Start Menu", "Programs", "Zorven.lnk")) {
		t.Fatalf("baslat menusu: %s", p.StartMenuLnk())
	}
	if !p.isInstalledExe(strings.ToUpper(p.DesktopExe())) {
		t.Error("kurulu exe buyuk/kucuk harf duyarsiz taninmali")
	}
	if p.isInstalledExe(filepath.Join(os.TempDir(), "Zorven.exe")) {
		t.Error("baska dizin kurulu sayilmamali")
	}
}

func TestWantsSetup(t *testing.T) {
	p := testPaths()
	dl := filepath.Join("D:", "dl")
	cases := []struct {
		exe  string
		args []string
		want bool
	}{
		{filepath.Join(dl, "Zorven-Setup-windows-amd64.exe"), nil, true},
		{filepath.Join(dl, "Zorven.exe"), []string{"--install"}, true},
		{filepath.Join(dl, "Zorven.exe"), nil, false},  // tasinabilir: uygulama acilir
		{p.DesktopExe(), []string{"--install"}, false}, // kurulu kopya kurulum kipine girmez
		{filepath.Join(dl, "Zorven-Setup.exe"), []string{"--hidden"}, false},
		{filepath.Join(dl, "Zorven-Setup.exe"), []string{waitPIDFlag, "5"}, false},
		{filepath.Join(dl, "Zorven-Setup.exe"), []string{updateApplyFlag, "x", "y"}, false},
	}
	for _, c := range cases {
		if got := wantsSetup(p, c.exe, c.args); got != c.want {
			t.Errorf("wantsSetup(%s,%v)=%v, beklenen %v", c.exe, c.args, got, c.want)
		}
	}
}

func TestPathAddRemove(t *testing.T) {
	dir := `C:\Program Files\Zorven`
	got, ch := pathAdd(`C:\Windows;C:\bin`, dir)
	if !ch || got != `C:\Windows;C:\bin;C:\Program Files\Zorven` {
		t.Fatalf("ekleme: %q %v", got, ch)
	}
	// Tekrar ekleme (buyuk/kucuk harf ve sondaki ters bolu farki) degistirmez.
	if again, ch := pathAdd(got, `c:\program files\zorven\`); ch || again != got {
		t.Fatalf("yinelenen ekleme: %q %v", again, ch)
	}
	if b, ch := pathAdd("", dir); !ch || b != dir {
		t.Fatalf("bos PATH: %q", b)
	}
	rm, ch := pathRemove(got+";", dir)
	if !ch || rm != `C:\Windows;C:\bin` {
		t.Fatalf("cikarma: %q %v", rm, ch)
	}
	if _, ch := pathRemove(rm, dir); ch {
		t.Error("olmayan girdi cikarilirken degisti denmemeli")
	}
}

func TestInstallSteps(t *testing.T) {
	f := newFake()
	p := testPaths()
	in := &installer{sys: f, paths: p}
	self := filepath.Join("D:", "dl", "Zorven-Setup.exe")
	err := in.Install(setupParams{Self: self, CLISrc: filepath.Join("T", "cli.tmp"), Version: "0.3.3",
		Server: "zorven.app:443", TokenFile: "", DesktopShortcut: true})
	if err != nil {
		t.Fatal(err)
	}
	if !f.files[p.DesktopExe()] || !f.files[p.CLIExe()] {
		t.Errorf("dosyalar kopyalanmadi: %v", f.files)
	}
	if !f.files[p.StartMenuLnk()] || !f.files[p.DesktopLnk()] {
		t.Error("kisayollar olusmadi")
	}
	if !strings.Contains(f.path, p.Dir()) {
		t.Errorf("PATH: %q", f.path)
	}
	e := f.entry
	if e == nil || e.DisplayName != "Zorven" || e.DisplayVersion != "0.3.3" || e.Publisher != "Zorven" ||
		e.InstallLocation != p.Dir() || e.DisplayIcon != p.DesktopExe() ||
		e.UninstallString != `"`+p.DesktopExe()+`" --uninstall` {
		t.Errorf("kayit: %+v", e)
	}
	// Sira: dosyalar, sonra servis kurulumu, sonra baslatma.
	ci, ii, si := f.index("copy"), f.index("cli service install --allow-no-token --enable-terminal --enable-screen --server zorven.app:443"), f.index("cli service start")
	if ci < 0 || ii < ci || si < ii {
		t.Errorf("sira yanlis: %v", f.calls)
	}
	if f.has("cli service install --allow-no-token --enable-terminal --enable-screen --server zorven.app:443 --token-file") {
		t.Error("token yokken --token-file verilmemeli")
	}
}

func TestInstallWithTokenFileAndNoDesktopShortcut(t *testing.T) {
	f := newFake()
	p := testPaths()
	err := (&installer{sys: f, paths: p}).Install(setupParams{Self: "x.exe", CLISrc: "c", Server: "s:443", TokenFile: "tok", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.has("cli service install --allow-no-token --enable-terminal --enable-screen --server s:443 --token-file tok") {
		t.Errorf("token dosyasi aktarilmadi: %v", f.calls)
	}
	if f.files[p.DesktopLnk()] {
		t.Error("istenmeyen masaustu kisayolu")
	}
}

func TestInstallSelfAlreadyInPlaceSkipsCopy(t *testing.T) {
	f := newFake()
	p := testPaths()
	if err := (&installer{sys: f, paths: p}).Install(setupParams{Self: p.DesktopExe(), CLISrc: "c"}); err != nil {
		t.Fatal(err)
	}
	if f.has("copy " + p.DesktopExe() + " " + p.DesktopExe()) {
		t.Error("kendini kendine kopyalamamali")
	}
}

func TestInstallErrorCodes(t *testing.T) {
	p := testPaths()
	cases := []struct {
		op   string
		code int
	}{{"mkdir", setupErrFiles}, {"copy", setupErrFiles}, {"pathadd", setupErrSystem}, {"lnk", setupErrSystem}, {"arp+", setupErrSystem}}
	for _, c := range cases {
		f := newFake()
		f.failOn[c.op] = errors.New("boom")
		err := (&installer{sys: f, paths: p}).Install(setupParams{Self: "x.exe", CLISrc: "c"})
		if got := setupExitCode(err); got != c.code {
			t.Errorf("%s: kod %d, beklenen %d", c.op, got, c.code)
		}
	}
	f := newFake()
	f.cliCode["service install"] = 1
	err := (&installer{sys: f, paths: p}).Install(setupParams{Self: "x.exe", CLISrc: "c"})
	if setupExitCode(err) != setupErrService {
		t.Errorf("servis hatasi kodu: %v", err)
	}
	if f.has("cli service start") {
		t.Error("kurulamayan servis baslatilmamali")
	}
}

func TestInstallStopsOldServiceFirst(t *testing.T) {
	f := newFake()
	p := testPaths()
	f.files[p.CLIExe()] = true // onceki kurulum
	if err := (&installer{sys: f, paths: p}).Install(setupParams{Self: "x.exe", CLISrc: "c"}); err != nil {
		t.Fatal(err)
	}
	if s, c := f.index("cli service stop"), f.index("copy"); s < 0 || s > c {
		t.Errorf("eski servis kopyadan once durdurulmali: %v", f.calls)
	}
}

func TestUninstall(t *testing.T) {
	f := newFake()
	p := testPaths()
	in := &installer{sys: f, paths: p}
	if err := in.Install(setupParams{Self: "x.exe", CLISrc: "c", DesktopShortcut: true}); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	f.cliCode["service status"] = 0 // kayitli
	if err := in.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if !f.has("cli service stop") || !f.has("cli service uninstall") {
		t.Errorf("servis kaldirilmadi: %v", f.calls)
	}
	if f.index("cli service uninstall") > f.index("rmall") {
		t.Error("servis dosyalardan once kaldirilmali")
	}
	if f.files[p.StartMenuLnk()] || f.files[p.DesktopLnk()] || f.Exists(p.Dir()) {
		t.Errorf("kalintilar: %v", f.files)
	}
	if strings.Contains(f.path, p.Dir()) {
		t.Errorf("PATH temizlenmedi: %q", f.path)
	}
	if f.entry != nil {
		t.Error("kayit silinmedi")
	}
	// Kullanici ayarlari ve ProgramData yapilandirmasi silinmemeli.
	for _, c := range f.calls {
		if strings.Contains(c, p.ConfigDir()) || strings.Contains(strings.ToLower(c), "appdata") {
			t.Errorf("ayarlara dokunuldu: %s", c)
		}
	}
}

func TestUninstallNoServiceRegistered(t *testing.T) {
	f := newFake()
	p := testPaths()
	f.files[p.CLIExe()] = true
	f.cliCode["service status"] = 4
	if err := (&installer{sys: f, paths: p}).Uninstall(); err != nil {
		t.Fatal(err)
	}
	if f.has("cli service uninstall") {
		t.Error("kayitli olmayan servis kaldirilmaya calisilmamali")
	}
}

func TestUninstallServiceFailureKeepsFiles(t *testing.T) {
	f := newFake()
	p := testPaths()
	f.files[p.CLIExe()] = true
	f.cliCode["service uninstall"] = 1
	err := (&installer{sys: f, paths: p}).Uninstall()
	if setupExitCode(err) != setupErrService {
		t.Fatalf("hata: %v", err)
	}
	if f.has("rmall") {
		t.Error("servis kaldirilamadiysa dosyalar silinmemeli")
	}
}

func TestRepair(t *testing.T) {
	f := newFake()
	p := testPaths()
	in := &installer{sys: f, paths: p}
	if setupExitCode(in.Repair("s:443", "", "")) != setupErrFiles {
		t.Error("CLI yokken onarim hata vermeli")
	}
	f.files[p.CLIExe()] = true
	if err := in.Repair("s:443", "tok", ""); err != nil {
		t.Fatal(err)
	}
	if !f.has("cli service install --allow-no-token --enable-terminal --enable-screen --server s:443 --token-file tok") || !f.has("cli service start") {
		t.Errorf("onarim: %v", f.calls)
	}
	if f.has("copy") {
		t.Error("onarim dosyalara dokunmamali")
	}
}

func TestMigrateLegacy(t *testing.T) {
	f := newFake()
	l := legacyPaths{LocalAppData: filepath.Join("C:", "Users", "u", "AppData", "Local"),
		AppData: filepath.Join("C:", "Users", "u", "AppData", "Roaming"), UserDesktop: filepath.Join("C:", "Users", "u", "Desktop")}
	if legacyInstalled(f, l) {
		t.Fatal("iz yokken eski kurulum bulunmamali")
	}
	f.files[filepath.Join(l.Dir(), "Zorven.exe")] = true
	f.files[l.StartMenuLnk()] = true
	f.files[l.DesktopLnk()] = true
	if !legacyInstalled(f, l) {
		t.Fatal("eski kurulum bulunamadi")
	}
	if !migrateLegacy(f, l, filepath.Join("D:", "x", "Zorven.exe")) {
		t.Fatal("dizin silinmedi")
	}
	if legacyInstalled(f, l) || !f.has("userarp-") {
		t.Errorf("temizlik eksik: %v %v", f.files, f.calls)
	}
}

func TestMigrateLegacyRunningFromOldDirKeepsDir(t *testing.T) {
	f := newFake()
	l := legacyPaths{LocalAppData: filepath.Join("C:", "L"), AppData: filepath.Join("C:", "R"), UserDesktop: filepath.Join("C:", "D")}
	f.files[filepath.Join(l.Dir(), "Zorven.exe")] = true
	if migrateLegacy(f, l, filepath.Join(l.Dir(), "Zorven.exe")) {
		t.Error("calisan kopyanin dizini silinmemeli")
	}
}

func TestCLIVersionLock(t *testing.T) {
	if !cliVersionOK("0.3.4", "0.3.4") {
		t.Error("ayni surum kabul edilmeli")
	}
	if cliVersionOK("0.3.4", "0.3.3") || cliVersionOK("0.3.4", "") || cliVersionOK("", "") {
		t.Error("surum farki reddedilmeli")
	}
}

func TestCLIManifestKey(t *testing.T) {
	if cliManifestKey("windows", "amd64") != "windows/amd64+cli" || cliManifestKey("windows", "arm64") != "windows/arm64" {
		t.Error("manifest anahtari")
	}
	if validBinName("../x") || validBinName(`a\b`) || validBinName("") || !validBinName("zorven-windows-amd64.exe") {
		t.Error("dosya adi dogrulama")
	}
}

func TestReplaceFile(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "zorven.exe")
	if err := replaceFile(p, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(p, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "v2" {
		t.Fatalf("icerik %q", b)
	}
	ents, _ := os.ReadDir(d)
	if len(ents) != 1 {
		t.Errorf("artik dosya kaldi: %v", ents)
	}
}

// Kurulu kipte guncelleme yardimcisi: CLI ve masaustu degisir, servis durdurulup
// yeniden baslatilir (hata olsa da baslatma yapilir). Surum kilidi ayri test edildi.
func TestApplyDesktopAndCLI(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, "Zorven.exe")
	_ = os.WriteFile(exe, []byte("MZold"), 0o755)
	cliExe := filepath.Join(d, cliExeName)
	_ = os.WriteFile(cliExe, []byte("MZoldcli"), 0o755)
	var verbs []string
	old := svcControl
	svcControl = func(_, verb string) { verbs = append(verbs, verb) }
	t.Cleanup(func() { svcControl = old })

	newDesk, newCLI := []byte("MZnewdesktop"), []byte("MZnewcli")
	sum := sha256.Sum256(newDesk)
	if code := applyDesktopAndCLI(exe, newDesk, sum[:], newCLI); code != helperOK {
		t.Fatalf("kod %d", code)
	}
	if b, _ := os.ReadFile(cliExe); string(b) != "MZnewcli" {
		t.Errorf("CLI guncellenmedi: %q", b)
	}
	if len(verbs) != 2 || verbs[0] != "stop" || verbs[1] != "start" {
		t.Errorf("servis sirasi: %v", verbs)
	}
}

func TestRunApplyUpdateCLIArgsVerified(t *testing.T) {
	d := t.TempDir()
	desk := filepath.Join(d, "d.exe")
	cli := filepath.Join(d, "c.exe")
	_ = os.WriteFile(desk, []byte("MZdesk"), 0o600)
	_ = os.WriteFile(cli, []byte("MZcli"), 0o600)
	ds := sha256.Sum256([]byte("MZdesk"))
	if got := runApplyUpdate([]string{desk, hex.EncodeToString(ds[:]), cli, "00"}); got != helperVerify {
		t.Fatalf("yanlis CLI sha: %d", got)
	}
	if got := runApplyUpdate([]string{"a", "b", "c"}); got != helperUsage {
		t.Fatalf("3 arguman: %d", got)
	}
}

func TestTokenSig(t *testing.T) {
	a, b := tokenSig("zrv_live_a", "s:443"), tokenSig("zrv_live_b", "s:443")
	if a == b || a != tokenSig("zrv_live_a", "s:443") || tokenSig("zrv_live_a", "t:443") == a {
		t.Error("imza ayirt etmeli ve kararli olmali")
	}
	if strings.Contains(a, "zrv_live") {
		t.Error("imza token icermemeli")
	}
}

func TestSvcTokenHelperValidation(t *testing.T) {
	d := t.TempDir()
	tf := filepath.Join(d, "tok")
	_ = os.WriteFile(tf, []byte("gecersiz"), 0o600)
	if got := runSvcTokenHelper([]string{tf, "zorven.app:443"}); got != setupUsage {
		t.Errorf("gecersiz token: %d", got)
	}
	if _, err := os.Stat(tf); !os.IsNotExist(err) {
		t.Error("token dosyasi silinmeli")
	}
	if runSvcTokenHelper([]string{"x"}) != setupUsage {
		t.Error("arguman sayisi")
	}
}

func TestConnectInstalledNoTakeoverAndShare(t *testing.T) {
	old := installedMode
	installedMode = func() bool { return true }
	t.Cleanup(func() { installedMode = old })
	a := &App{cfg: defaultConfig()}
	if msg := a.TakeOver(); msg == "" {
		t.Error("kurulu kipte devralma reddedilmeli")
	}
	if msg := a.connectInstalled("3000"); msg == "" {
		t.Error("kurulu kipte port paylasimi gomulu ajan acmamali")
	}
}

// Masaustu ve CLI adi buyuk/kucuk harf duyarsiz cakismamali (0.3.4 hatasi).
func TestNameClash(t *testing.T) {
	if strings.EqualFold(desktopExeName, cliExeName) {
		t.Fatalf("sabit adlar cakisiyor: %s / %s", desktopExeName, cliExeName)
	}
	p := testPaths()
	if err := checkNameClash(p.DesktopExe(), p.CLIExe()); err != nil {
		t.Fatalf("gercek adlar cakismamali: %v", err)
	}
	if err := checkNameClash(filepath.Join(p.Dir(), "Zorven.exe"), filepath.Join(p.Dir(), "zorven.exe")); err == nil {
		t.Fatal("Zorven.exe / zorven.exe cakismasi yakalanmali")
	}
	if err := checkNameClash(filepath.Join("A", "Zorven.exe"), filepath.Join("B", "zorven.exe")); err != nil {
		t.Fatalf("farkli dizin cakisma degil: %v", err)
	}
}

// Bozuk 0.3.4 kurulumu: dizinde yalniz CLI (zorven.exe), kisayol ve ARP CLI'ye
// isaret ediyor. Yeni kurulum masaustunu ZorvenDesktop.exe olarak koyar, CLI'yi
// gunceller, kisayol/ARP'yi yeniden yazar, servisi korur (kurar/baslatir).
func TestInstallRecoversBrokenInstall(t *testing.T) {
	f := newFake()
	p := testPaths()
	old := p.Dir() + string(filepath.Separator) + "Zorven.exe"
	f.files[p.CLIExe()] = true
	f.files[p.StartMenuLnk()] = true
	f.entry = &uninstallEntry{DisplayIcon: old, UninstallString: `"` + old + `" --uninstall`}
	in := &installer{sys: f, paths: p}
	if err := in.Install(setupParams{Self: filepath.Join("D:", "Zorven-Setup-windows-amd64.exe"), CLISrc: "cli.exe", Version: "0.3.5"}); err != nil {
		t.Fatal(err)
	}
	if !f.files[p.DesktopExe()] || !strings.HasSuffix(p.DesktopExe(), "ZorvenDesktop.exe") {
		t.Fatalf("masaustu kopyalanmadi: %v", f.files)
	}
	if !f.has("copy cli.exe " + p.CLIExe()) {
		t.Error("CLI guncellenmedi")
	}
	if f.index("rmfile "+p.StartMenuLnk()) < 0 || f.index("arp-") < 0 {
		t.Errorf("eski kisayol/ARP silinmedi: %v", f.calls)
	}
	if f.index("arp-") > f.index("arp+") {
		t.Error("ARP silme yazmadan once olmali")
	}
	if f.entry == nil || !strings.Contains(f.entry.DisplayIcon, "ZorvenDesktop.exe") || !strings.Contains(f.entry.UninstallString, "ZorvenDesktop.exe") {
		t.Errorf("ARP yeni adi gostermeli: %+v", f.entry)
	}
	if !f.has("lnk " + p.StartMenuLnk() + " " + p.DesktopExe()) {
		t.Errorf("kisayol masaustune isaret etmeli: %v", f.calls)
	}
	if !f.has("cli service install") || !f.has("cli service start") {
		t.Error("servis kurulmadi/baslatilmadi")
	}
}

// Bozuk kurulumdan --uninstall calisir (yalniz CLI var).
func TestUninstallBrokenInstall(t *testing.T) {
	f := newFake()
	p := testPaths()
	f.files[p.CLIExe()] = true
	f.files[p.StartMenuLnk()] = true
	in := &installer{sys: f, paths: p}
	if err := in.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if f.Exists(p.Dir()) || f.Exists(p.StartMenuLnk()) || f.entry != nil {
		t.Errorf("kalinti var: %v", f.files)
	}
	if !f.has("cli service uninstall") {
		t.Error("servis kaldirilmadi")
	}
}
