package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/selfupdate"
)

// Guncelleme uygulama yardimcilari: akisli indirme, aktif oturumlari bekleme,
// geri alma (rollback) isaretcisi.

const (
	// maxUpdateDeferral, aktif oturum (terminal/ekran/dosya) yuzunden guncellemenin
	// en fazla ne kadar ertelenecegi. Sonra yine uygulanir.
	maxUpdateDeferral = 30 * time.Minute
	updateDeferPoll   = 15 * time.Second

	// rollbackAttempts: yeni surum bu kadar kez ONAYLANMADAN (hello_ack) baslatilirsa eskisine donulur.
	rollbackAttempts = 3
	// rollbackConfirmWait: yeni surum bu sureden uzun hello_ack alamazsa VE sunucuya
	// HTTP ile ulasilabiliyorsa (ag degil, ikili sorunlu) eskisine donulur.
	rollbackConfirmWait = 2 * time.Minute
)

// updateMarker, <exe>.upd dosyasi: son guncellemenin durumu.
type updateMarker struct {
	From     string `json:"from"`
	To       string `json:"to"`
	OldPath  string `json:"old_path"`
	Attempts int    `json:"attempts"`
	At       string `json:"at"`
}

func markerPath(exe string) string { return exe + ".upd" }
func badPath(exe string) string    { return exe + ".upd-bad" }

func writeMarker(exe string, m updateMarker) error {
	b, _ := json.Marshal(m)
	return os.WriteFile(markerPath(exe), b, 0o600)
}

func readMarker(exe string) (updateMarker, bool) {
	var m updateMarker
	b, err := os.ReadFile(markerPath(exe))
	if err != nil || json.Unmarshal(b, &m) != nil || m.To == "" {
		return m, false
	}
	return m, true
}

// activeSessionCount, acik terminal + ekran + dosya aktarimi sayisi.
func (a *Agent) activeSessionCount() int {
	a.mu.Lock()
	tm, sm, fm := a.terminal, a.screen, a.curFM
	a.mu.Unlock()
	n := 0
	if tm != nil {
		n += tm.count()
	}
	if sm != nil {
		n += sm.count()
	}
	if fm != nil {
		n += fm.count()
	}
	return n
}

func (fm *fileManager) count() int {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return len(fm.xfers)
}

// waitIdleForUpdate, aktif oturum varken (en fazla maxDeferral) bekler. Elle
// guncellemede cagrilmaz. false: ctx bitti.
func (a *Agent) waitIdleForUpdate(ctx context.Context, maxDeferral, poll time.Duration) bool {
	deadline := time.Now().Add(maxDeferral)
	logged := false
	for a.activeSessionCount() > 0 && time.Now().Before(deadline) {
		if !logged {
			a.Log.Info("guncelleme ertelendi: aktif terminal/ekran/dosya oturumu var", "en_fazla", maxDeferral)
			logged = true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(poll):
		}
	}
	return ctx.Err() == nil
}

// downloadToFile, path'i temp dosyaya AKITIR (RAM'de tutmaz) ve sha256'sini hesaplar.
// Dosya dir icinde olusur (selfupdate ayni dosya sistemine yazacagi icin).
func (a *Agent) downloadToFile(ctx context.Context, path, dir string, max int64) (string, string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, a.httpBase()+path, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := a.updateHTTPClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("indirme http %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, ".zorven-upd-*")
	if err != nil {
		f, err = os.CreateTemp("", ".zorven-upd-*")
		if err != nil {
			return "", "", err
		}
	}
	name := f.Name()
	fail := func(e error) (string, string, error) {
		f.Close()
		os.Remove(name)
		return "", "", e
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, max+1))
	if err != nil {
		return fail(err)
	}
	if n > max {
		return fail(fmt.Errorf("indirme boyut siniri asildi (%d bayt)", max))
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", "", err
	}
	return name, hex.EncodeToString(h.Sum(nil)), nil
}

// applyUpdateFile, dogrulanmis ikiliyi uygular; eski ikiliyi <exe>.old olarak SAKLAR.
func applyUpdateFile(path, exe string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	old := exe + ".old"
	_ = os.Remove(old)
	if err := selfupdate.Apply(f, selfupdate.Options{TargetPath: exe, OldSavePath: old}); err != nil {
		if rerr := selfupdate.RollbackError(err); rerr != nil {
			return old, fmt.Errorf("%w (geri alma da basarisiz: %v)", err, rerr)
		}
		return old, err
	}
	return old, nil
}

// --- baslangicta rollback denetimi ---------------------------------------------

// startUpdateWatch, Run basinda cagrilir. Yeni surumle ilk baslatmaysa onay (hello_ack)
// bekler; cok kez onaylanmadan basladiysa ya da onay gelmiyorsa eski ikiliye doner.
func (a *Agent) startUpdateWatch(ctx context.Context) {
	exe, err := os.Executable()
	if err != nil || a.Version == "" || a.Version == "dev" {
		return
	}
	m, ok := readMarker(exe)
	if !ok {
		// Onceki calistirmada geri alma yapildiysa durumu (status last_error) bildir.
		if b, err := os.ReadFile(badPath(exe)); err == nil && strings.TrimSpace(string(b)) != "" {
			a.observ().recordRollback(strings.TrimSpace(string(b)), a.Version)
		}
		return
	}
	if m.To != a.Version {
		// Marker baska bir surume ait (eski kalinti ya da elle degisim): temizle.
		_ = os.Remove(markerPath(exe))
		return
	}
	m.Attempts++
	_ = writeMarker(exe, m)
	if m.Attempts >= rollbackAttempts {
		a.Log.Error("yeni surum onaylanmadan tekrar tekrar basladi, eski surume donuluyor", "yeni", m.To, "eski", m.From, "deneme", m.Attempts)
		a.rollbackUpdate(exe, m)
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(rollbackConfirmWait):
		}
		if _, still := readMarker(exe); !still {
			return // onaylandi
		}
		// Sunucuya HTTP ile ulasilabiliyor ama hello_ack yok: sorun ag degil, ikili.
		if _, err := a.fetchBody(ctx, "/bin/manifest.json"); err != nil {
			return
		}
		a.Log.Error("yeni surum sunucuya baglanamadi (sunucu erisilebilir), eski surume donuluyor", "yeni", m.To, "eski", m.From)
		a.rollbackUpdate(exe, m)
	}()
}

// confirmUpdate, hello_ack alininca cagrilir: yeni surum calisiyor, isaretci silinir.
func (a *Agent) confirmUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if m, ok := readMarker(exe); ok && m.To == a.Version {
		_ = os.Remove(markerPath(exe))
		_ = os.Remove(m.OldPath)
		a.Log.Info("guncelleme onaylandi", "surum", m.To)
	}
}

// rollbackUpdate, saklanan eski ikiliyi geri koyar ve yeniden baslar.
func (a *Agent) rollbackUpdate(exe string, m updateMarker) {
	old := m.OldPath
	if old == "" {
		old = exe + ".old"
	}
	if _, err := os.Stat(old); err != nil {
		a.Log.Error("geri alma: eski ikili yok", "yol", old, "hata", err)
		_ = os.Remove(markerPath(exe))
		return
	}
	if err := swapBack(exe, old); err != nil {
		a.Log.Error("geri alma basarisiz", "hata", err)
		return
	}
	_ = os.WriteFile(badPath(exe), []byte(m.To), 0o600) // bu surum tekrar denenmesin
	_ = os.Remove(markerPath(exe))
	a.observ().recordRollback(m.To, m.From)
	a.Log.Warn("guncelleme geri alindi", "bozuk", m.To, "donulen", m.From)
	a.restartSelf(exe)
}

// badVersion, rollback ile isaretlenmis surum mu.
func badVersion(exe, v string) bool {
	b, err := os.ReadFile(badPath(exe))
	return err == nil && strings.TrimSpace(string(b)) == v
}

func (o *agentObs) recordRollback(bad, to string) {
	o.recordDisconnect(errors.New("guncelleme geri alindi: " + bad + " -> " + to))
}

// dirOf, exe'nin klasoru (temp indirme icin).
func dirOf(exe string) string { return filepath.Dir(exe) }

// swapBack, saklanan eski ikiliyi exe yoluna geri koyar. Calisan exe Windows'ta
// silinemez ama yeniden adlandirilabilir; hata halinde yeni ikili yerine geri sarilir.
func swapBack(exe, old string) error {
	bad := exe + ".bad"
	_ = os.Remove(bad)
	if err := os.Rename(exe, bad); err != nil {
		return fmt.Errorf("calisan ikili ayrilamadi: %w", err)
	}
	if err := os.Rename(old, exe); err != nil {
		_ = os.Rename(bad, exe)
		return fmt.Errorf("eski ikili yerine konamadi: %w", err)
	}
	_ = os.Remove(bad)
	return nil
}
