package main

// Masaustu uygulamasinin kendi surum bilgisi ve guncellemesi.
//
// Gomulu ajanin oto-guncellemesi (client/agent/update.go) KULLANILMAZ: o, CLI
// ikilisini indirip calisan dosyanin yerine koyar; masaustunde bu Zorven.exe'yi
// CLI ile ezerdi. Bu yuzden masaustu ayri bir manifest (/bin/desktop.json)
// okur ve kendi ikilisini (Zorven-desktop-<os>-<arch>) uygular.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/selfupdate"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/shared/updatesig"
)

// version, derleme sirasinda -ldflags "-X main.version=..." ile yazilir.
var version = "dev"

const (
	desktopDownloadPage = "https://zorven.app/download?utm_source=desktop"
	updatesPageBase     = "https://zorven.app"
	maxUpdateBytes      = 200 << 20
)

type desktopManifest struct {
	Version string `json:"version"`
	Files   map[string]struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"files"`
}

// UpdateInfo, arayuze donen guncelleme durumu.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	CanApply  bool   `json:"can_apply"` // bu platform icin ikili var mi
	NotesURL  string `json:"notes_url,omitempty"`
	Error     string `json:"error,omitempty"`
}

// updateState, arka plan guncelleme durumu.
type updateState struct {
	mu       sync.Mutex
	info     UpdateInfo
	notified string // en son bildirimi yapilan surum
	busy     bool   // indirme/uygulama suruyor
	cancel   func() // suren indirmeyi iptal eder
}

// GetVersion, calisan uygulamanin surumu.
func (a *App) GetVersion() string { return version }

// releaseNotesURL, guncellemeler sayfasinin dile gore yolu: tr kokte,
// diger diller /en/ /ja/ /zh/ altinda.
func releaseNotesURL(lang string) string {
	switch lang {
	case i18n.EN:
		return updatesPageBase + "/en/updates?utm_source=desktop"
	case i18n.JA:
		return updatesPageBase + "/ja/updates?utm_source=desktop"
	case i18n.ZH:
		return updatesPageBase + "/zh/updates?utm_source=desktop"
	}
	return updatesPageBase + "/updates?utm_source=desktop"
}

// OpenReleaseNotes, surum notlari sayfasini tarayicida acar.
func (a *App) OpenReleaseNotes() {
	a.OpenBrowser(releaseNotesURL(i18n.Lang()))
}

func (a *App) updateBase() string {
	a.mu.Lock()
	addr := a.cfg.ServerAddr
	a.mu.Unlock()
	if addr == "" || addr == "localhost:8443" {
		addr = "zorven.app:443"
	}
	return "https://" + strings.TrimSuffix(addr, ":443")
}

var updateHTTP = &http.Client{Timeout: 60 * time.Second}

// downloadHTTP: buyuk dosya icin toplam sure sinirsiz; ilerleme/iptal ctx ile.
var downloadHTTP = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

func (a *App) fetchDesktopManifest(ctx context.Context) (desktopManifest, error) {
	body, err := fetchUpdateFile(ctx, a.updateBase()+"/bin/desktop.json")
	if err != nil {
		return desktopManifest{}, err
	}
	sig, err := fetchUpdateFile(ctx, a.updateBase()+"/bin/desktop.json.sig")
	if err != nil {
		return desktopManifest{}, errors.New(tm("upd.sigFail"))
	}
	return parseSignedManifest(body, sig, nil)
}

// parseSignedManifest, desktop.json baytlarini imza dogrulandiktan SONRA
// ayristirir (keys nil ise gomulu acik anahtarlar).
func parseSignedManifest(body, sig []byte, keys []string) (desktopManifest, error) {
	var m desktopManifest
	if err := updatesig.Verify(body, sig, keys); err != nil {
		return m, errors.New(tm("upd.notApplied", updatesig.Describe(err)))
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, errors.New(tm("upd.readFail"))
	}
	return m, nil
}

func fetchUpdateFile(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, errors.New(tm("upd.unreachable"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(tm("upd.httpFail", resp.StatusCode))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// CheckForUpdate, sunucudaki en son masaustu surumunu sorgular.
func (a *App) CheckForUpdate() UpdateInfo {
	info := UpdateInfo{Current: version, NotesURL: releaseNotesURL(i18n.Lang())}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m, err := a.fetchDesktopManifest(ctx)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Latest = m.Version
	info.Available = version != "dev" && newerVersion(m.Version, version)
	_, info.CanApply = m.Files[runtime.GOOS+"/"+runtime.GOARCH]
	a.upd.mu.Lock()
	a.upd.info = info
	a.upd.mu.Unlock()
	return info
}

// GetUpdateInfo, son bilinen guncelleme durumunu (arka plan denetimi dahil) doner.
func (a *App) GetUpdateInfo() UpdateInfo {
	a.upd.mu.Lock()
	defer a.upd.mu.Unlock()
	info := a.upd.info
	info.Current = version
	if info.Latest != "" {
		info.NotesURL = releaseNotesURL(i18n.Lang())
	}
	return info
}

// emitProgress, indirme ilerlemesini arayuze yollar.
func (a *App) emitProgress(phase string, done, total int64) {
	emit(a.ctx, "update:progress", map[string]any{"phase": phase, "done": done, "total": total})
}

// progressReader, okunan bayt sayisini sinirli sikilikta bildirir.
type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	last  time.Time
	fn    func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.fn != nil && (time.Since(p.last) > 150*time.Millisecond || err != nil) {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return n, err
}

// downloadVerified, url'yi gecici dosyaya AKAR (bellege tamamen okumaz), sha256
// ve boyutu dogrular, yurutulebilir (MZ) basligini denetler. Basarida dosya
// yolunu; hata durumunda kullaniciya gosterilecek metni doner.
func downloadVerified(ctx context.Context, url, wantSHA string, wantSize int64, progress func(done, total int64)) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := downloadHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New(tm("upd.cancelled"))
		}
		return "", errors.New(tm("upd.dlFail"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(tm("upd.dlFailHTTP", resp.StatusCode))
	}
	total := resp.ContentLength
	if total <= 0 {
		total = wantSize
	}
	tmp, err := os.CreateTemp("", "zorven-update-*.exe")
	if err != nil {
		return "", errors.New(tm("upd.tmpFail"))
	}
	name := tmp.Name()
	fail := func(e error) (string, error) {
		tmp.Close()
		_ = os.Remove(name)
		return "", e
	}
	h := sha256.New()
	pr := &progressReader{r: io.LimitReader(resp.Body, maxUpdateBytes+1), total: total, fn: progress}
	n, err := io.Copy(io.MultiWriter(tmp, h), pr)
	if err != nil {
		if ctx.Err() != nil {
			return fail(errors.New(tm("upd.cancelled")))
		}
		return fail(errors.New(tm("upd.dlFail")))
	}
	if n > maxUpdateBytes {
		return fail(errors.New(tm("upd.sizeMismatch")))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", errors.New(tm("upd.tmpFail"))
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantSHA) {
		_ = os.Remove(name)
		return "", errors.New(tm("upd.shaMismatch"))
	}
	if wantSize > 0 && n != wantSize {
		_ = os.Remove(name)
		return "", errors.New(tm("upd.sizeMismatch"))
	}
	if runtime.GOOS == "windows" {
		f, err := os.Open(name)
		if err == nil {
			head := make([]byte, 2)
			_, _ = io.ReadFull(f, head)
			f.Close()
			if !bytes.Equal(head, []byte("MZ")) {
				_ = os.Remove(name)
				return "", errors.New(tm("upd.notExe"))
			}
		}
	}
	return name, nil
}

// CancelUpdate, suren indirmeyi iptal eder.
func (a *App) CancelUpdate() {
	a.upd.mu.Lock()
	c := a.upd.cancel
	a.upd.mu.Unlock()
	if c != nil {
		c()
	}
}

// InstallUpdate, yeni surumu indirir (ilerleme olaylariyla), sha256'yi dogrular,
// calisan ikilinin yerine koyar ve uygulamayi yeniden baslatir. Bos donerse
// yeniden baslatma basladi demektir; hata metni donerse calisan surum
// degismemistir ve baglanti korunur (tekrar denenebilir).
func (a *App) InstallUpdate() string {
	a.upd.mu.Lock()
	if a.upd.busy {
		a.upd.mu.Unlock()
		return tm("upd.busy")
	}
	a.upd.busy = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	a.upd.cancel = cancel
	a.upd.mu.Unlock()
	defer func() {
		cancel()
		a.upd.mu.Lock()
		a.upd.busy = false
		a.upd.cancel = nil
		a.upd.mu.Unlock()
	}()

	msg := a.installUpdate(ctx)
	if msg != "" {
		a.log.Warn("guncelleme basarisiz", "hata", msg)
		a.emitProgress("error", 0, 0)
	}
	return msg
}

func (a *App) installUpdate(ctx context.Context) string {
	m, err := a.fetchDesktopManifest(ctx)
	if err != nil {
		return err.Error()
	}
	// Downgrade / ayni surum engeli (imzali olsa bile eski manifest tekrar sunulabilir).
	if !newerVersion(m.Version, version) {
		return tm("upd.latest")
	}
	f, ok := m.Files[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok || f.Name == "" || f.SHA256 == "" {
		a.OpenBrowser(desktopDownloadPage)
		return tm("upd.noPlatform")
	}
	// Dosya adi yol ayirici icermemeli (manifest imzali olsa da savunma).
	if f.Name != filepath.Base(f.Name) || strings.ContainsAny(f.Name, `/\`) {
		return tm("upd.readFail")
	}
	a.emitProgress("download", 0, f.Size)
	path, err := downloadVerified(ctx, a.updateBase()+"/bin/"+f.Name, f.SHA256, f.Size, func(done, total int64) {
		a.emitProgress("download", done, total)
	})
	if err != nil {
		return err.Error()
	}
	defer os.Remove(path)
	a.emitProgress("apply", 0, 0)

	exe, err := os.Executable()
	if err != nil {
		return tm("upd.noExe")
	}
	// Kurulu Windows kipi: masaustu + CLI tek yukseltilmis yardimciyla birlikte
	// guncellenir, servis yeniden baslatilir (surum kilidi).
	if installedMode() {
		return a.installUpdateInstalled(ctx, m, path, f.SHA256, exe)
	}
	sum, _ := hex.DecodeString(f.SHA256)
	if err := applyFromFile(path, selfupdate.Options{Checksum: sum, Hash: crypto.SHA256}); err != nil {
		// Genellikle kurulum klasorune yazma izni yoktur (or. Program Files).
		// Indirme sayfasina atmak yerine yonetici onayiyla ayni ikiliye
		// uygulatmayi dene; o da olmazsa nedeniyle birlikte sayfayi ac.
		if runtime.GOOS == "windows" && !processElevated() {
			return a.applyUpdateElevated(exe, path, f.SHA256, "", "")
		}
		a.OpenBrowser(desktopDownloadPage)
		return tm("upd.applyFail", err.Error())
	}
	return a.restartAfterUpdate(exe)
}

func applyFromFile(path string, opts selfupdate.Options) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	return selfupdate.Apply(fh, opts)
}

// restartAfterUpdate, yeni surumu BU surec kapanana kadar bekleyecek sekilde
// baslatir (tek ornek kilidi cakismasin), sonra baglantiyi temiz kapatip cikar.
// Baslatma basarisizsa baglanti ve uygulama oldugu gibi surer.
func (a *App) restartAfterUpdate(exe string) string {
	if err := startSelf(exe); err != nil {
		a.log.Warn("yeni surum baslatilamadi", "hata", err)
		return tm("upd.restartHand")
	}
	a.emitProgress("restart", 0, 0)
	go func() {
		a.disconnectAndWait(3 * time.Second)
		time.Sleep(200 * time.Millisecond)
		a.QuitApp()
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	}()
	return ""
}

// waitPIDFlag: yeni surec, eski surecin (pid) kapanmasini bekleyip sonra acilir.
const waitPIDFlag = "--zv-wait-pid"

// startSelf, exe'yi eski sureci bekleyecek sekilde baslatir.
func startSelf(exe string) error {
	cmd := exec.Command(exe, waitPIDFlag, strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// cleanupOldBinary, selfupdate'in biraktigi ".<ad>.old" dosyasini siler
// (Windows'ta calisan ikili yeniden adlandirilir; eski dosya sonraki acilista silinebilir).
func cleanupOldBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir, base := filepath.Split(exe)
	for _, n := range []string{"." + base + ".old", base + ".old"} {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// updateApplyFlag, yukseltilmis yardimci kipini secer:
//
//	Zorven.exe --zv-apply-update <indirilen-dosya> <sha256>
//
// Calisan (kurulu, yalniz yoneticinin yazabildigi) ikili UAC ile bu kipte
// acilir; dosyayi bellege okuyup sha256'yi YENIDEN dogrular ve kendi yerine
// koyar. Yeni surumu BASLATMAZ: o, yukseltilmemis kullanici sureci tarafindan
// yapilir (bu sure cikis kodunu bekler). Gecici dosya kullanici yazilabilir
// oldugu icin dogrulama yukseltilmis surecte yapilir.
const updateApplyFlag = "--zv-apply-update"

// Yardimci cikis kodlari.
const (
	helperOK       = 0
	helperUsage    = 2
	helperVerify   = 3
	helperNoExe    = 4
	helperApplyErr = 5
)

// applyUpdateElevated, dogrulanmis ikiliyi yonetici olarak yardimci kipte
// uygulatir ve BEKLER. Yardimci basarisizsa bu uygulama calismaya devam eder.
//
// cliPath/cliSHA doluysa (kurulu kip) ayni yardimci kurulu dizindeki zorven.exe'yi
// de degistirir ve servisi durdurup yeniden baslatir: tek UAC onayi.
func (a *App) applyUpdateElevated(exe, path, sha, cliPath, cliSHA string) string {
	// Yardimci dosyayi isi bitince siler; kopya uzerinde calissin ki
	// installUpdate'in defer'i da guvenle silebilsin.
	cp, err := copyTemp(path)
	if err != nil {
		return tm("upd.tmpFail")
	}
	params := fmt.Sprintf("%s \"%s\" %s", updateApplyFlag, cp, sha)
	cleanup := []string{cp}
	if cliPath != "" {
		ccp, err := copyTemp(cliPath)
		if err != nil {
			_ = os.Remove(cp)
			return tm("upd.tmpFail")
		}
		cleanup = append(cleanup, ccp)
		params += fmt.Sprintf(" \"%s\" %s", ccp, cliSHA)
	}
	code, err := runElevatedWait(exe, params, 3*time.Minute)
	if err != nil {
		for _, c := range cleanup {
			_ = os.Remove(c)
		}
		return tm("upd.needAdmin", err.Error())
	}
	if code != helperOK {
		for _, c := range cleanup {
			_ = os.Remove(c)
		}
		return tm("upd.helperFail", code)
	}
	return a.restartAfterUpdate(exe)
}

// installUpdateInstalled, kurulu kipte masaustu ve CLI'yi birlikte gunceller.
// CLI manifest surumu masaustu hedefiyle ayni degilse (kilit) HICBIR sey
// uygulanmaz.
func (a *App) installUpdateInstalled(ctx context.Context, m desktopManifest, deskPath, deskSHA, exe string) string {
	cm, err := fetchSignedManifest(ctx, a.updateBase(), "manifest.json")
	if err != nil {
		return err.Error()
	}
	if !cliVersionOK(m.Version, cm.Version) {
		return tm("upd.cliLock", cm.Version, m.Version)
	}
	cf, ok := cm.Files[cliManifestKey(runtime.GOOS, runtime.GOARCH)]
	if !ok || !validBinName(cf.Name) || cf.SHA256 == "" {
		return tm("setup.errNoCLI")
	}
	a.emitProgress("download", 0, cf.Size)
	cliPath, err := downloadVerified(ctx, a.updateBase()+"/bin/"+cf.Name, cf.SHA256, cf.Size, func(done, total int64) {
		a.emitProgress("download", done, total)
	})
	if err != nil {
		return err.Error()
	}
	defer os.Remove(cliPath)
	a.emitProgress("apply", 0, 0)
	return a.applyUpdateElevated(exe, deskPath, deskSHA, cliPath, cf.SHA256)
}

func copyTemp(src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp("", "zorven-update-*.exe")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(out.Name())
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// runApplyUpdate, yardimci kip: args = [dosya, sha256]. Cikis kodu dondurur.
//
// args 4 elemanliysa [dosya, sha256, cli-dosyasi, cli-sha256]: kurulu dizindeki
// zorven.exe de degistirilir ve servis durdurulup yeniden baslatilir.
func runApplyUpdate(args []string) int {
	if len(args) != 2 && len(args) != 4 {
		return helperUsage
	}
	path, sha := args[0], args[1]
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil || verifyDownload(data, sha, 0) != "" {
		return helperVerify
	}
	var cliData []byte
	if len(args) == 4 {
		defer os.Remove(args[2])
		cliData, err = os.ReadFile(args[2])
		if err != nil || verifyDownload(cliData, args[3], 0) != "" {
			return helperVerify
		}
	}
	sum, _ := hex.DecodeString(sha)
	exe, err := os.Executable()
	if err != nil {
		return helperNoExe
	}
	if cliData != nil {
		return applyDesktopAndCLI(exe, data, sum, cliData)
	}
	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{
		TargetPath: exe, Checksum: sum, Hash: crypto.SHA256,
	}); err != nil {
		return helperApplyErr
	}
	return helperOK
}

// svcControl, CLI ile servisi durdurur/baslatir (testlerde degistirilir).
var svcControl = func(cliExe, verb string) { _, _ = runHidden(cliExe, "service", verb) }

// applyDesktopAndCLI: servisi durdur, CLI ve masaustu ikilisini degistir, servisi
// baslat. Servis her durumda yeniden baslatilir.
func applyDesktopAndCLI(exe string, desktop, desktopSum, cli []byte) int {
	cliExe := filepath.Join(filepath.Dir(exe), cliExeName)
	svcControl(cliExe, "stop")
	defer svcControl(cliExe, "start")
	if err := replaceFile(cliExe, cli); err != nil {
		return helperApplyErr
	}
	if err := selfupdate.Apply(bytes.NewReader(desktop), selfupdate.Options{
		TargetPath: exe, Checksum: desktopSum, Hash: crypto.SHA256,
	}); err != nil {
		return helperApplyErr
	}
	return helperOK
}

// verifyDownload, indirilen verinin sha256'sini, (verildiyse) boyutunu ve
// Windows'ta calistirilabilir (MZ) olup olmadigini denetler. Bos donerse
// dosya uygulanabilir; aksi halde kullaniciya gosterilecek hata metni.
func verifyDownload(data []byte, wantSHA string, wantSize int64) string {
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), wantSHA) {
		return tm("upd.shaMismatch")
	}
	if wantSize > 0 && int64(len(data)) != wantSize {
		return tm("upd.sizeMismatch")
	}
	if runtime.GOOS == "windows" && !bytes.HasPrefix(data, []byte("MZ")) {
		return tm("upd.notExe")
	}
	return ""
}

// newerVersion, a > b ise true (ortak updatesig.Newer).
func newerVersion(a, b string) bool { return updatesig.Newer(a, b) }
