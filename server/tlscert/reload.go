package tlscert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// currentDefault, yeniden yuklenmis varsayilan sertifikayi (varsa) ya da
// kurucuda verilen DefaultCert'i doner.
func (m *DualCertManager) currentDefault() *tls.Certificate {
	if c := m.reloaded.Load(); c != nil {
		return c
	}
	return m.DefaultCert
}

// SetDefaultCert, varsayilan (platform/joker) sertifikayi baglantilari
// kesmeden atomik olarak degistirir. Yeni el sikismalar yeni sertifikayi alir.
func (m *DualCertManager) SetDefaultCert(c *tls.Certificate) {
	if c != nil {
		m.reloaded.Store(c)
	}
}

// EnableCacheOnly, ACME'yi KAPATIR ve ozel alan adi sertifikalarini yalniz
// verilen onbellekten (birincilin yazdigi veritabani) okur. Edge rolunde
// kullanilir: edge sertifika talep etmez, challenge cevaplamaz. Faz 0C'de ACME
// kontrol duzleminde toplanana kadar gecici tasarimdir.
func (m *DualCertManager) EnableCacheOnly(cache autocert.Cache) {
	m.AutocertManager = nil
	m.cacheOnly = cache
}

const (
	cacheHitTTL  = 5 * time.Minute
	cacheMissTTL = 30 * time.Second
)

type cachedCert struct {
	cert *tls.Certificate // nil = onbellekte yok (negatif kayit)
	at   time.Time
}

// cachedCertFor, onbellekteki (autocert bicimli PEM: anahtar + zincir) sertifikayi
// bellekte kisa sureli tutarak doner. Suresi gecmis sertifika sunulmaz.
func (m *DualCertManager) cachedCertFor(ctx context.Context, host string) (*tls.Certificate, error) {
	now := time.Now()
	m.cacheMu.Lock()
	if m.cacheHit == nil {
		m.cacheHit = make(map[string]cachedCert)
	}
	if e, ok := m.cacheHit[host]; ok {
		ttl := cacheHitTTL
		if e.cert == nil {
			ttl = cacheMissTTL
		}
		if now.Sub(e.at) < ttl && (e.cert == nil || validNow(e.cert, now)) {
			m.cacheMu.Unlock()
			if e.cert == nil {
				return nil, fmt.Errorf("sertifika onbellekte yok: %s", host)
			}
			return e.cert, nil
		}
	}
	m.cacheMu.Unlock()

	var found *tls.Certificate
	for _, k := range []string{host, host + "+rsa"} {
		data, err := m.cacheOnly.Get(ctx, k)
		if err != nil {
			continue
		}
		c, err := tls.X509KeyPair(data, data)
		if err != nil {
			continue
		}
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
			c.Leaf = leaf
		}
		if validNow(&c, now) {
			found = &c
			break
		}
	}
	m.cacheMu.Lock()
	m.cacheHit[host] = cachedCert{cert: found, at: now}
	m.cacheMu.Unlock()
	if found == nil {
		return nil, fmt.Errorf("sertifika onbellekte yok ya da suresi dolmus: %s", host)
	}
	return found, nil
}

func validNow(c *tls.Certificate, now time.Time) bool {
	if c.Leaf == nil {
		return true
	}
	return now.After(c.Leaf.NotBefore) && now.Before(c.Leaf.NotAfter)
}

// FileReloader, sertifika/anahtar dosyalarini izler ve degisince Apply'a verir.
// Degisiklik algisi: mtime + boyut (os.Stat sembolik baglari izler); SIGHUP ile
// zorla yeniden yukleme de yapilir. Yeni cift okunamaz ya da uyumsuzsa (ornegin
// dosyalardan yalniz biri yazildiysa) ESKI sertifika korunur ve sonraki turda
// yeniden denenir; sunucu asla sertifikasiz kalmaz.
type FileReloader struct {
	CertFile string
	KeyFile  string
	Apply    func(*tls.Certificate)
	Log      *slog.Logger

	mu   sync.Mutex
	last string // son basarili yuklemenin dosya imzasi
}

func (r *FileReloader) signature() (string, error) {
	var sig string
	for _, p := range []string{r.CertFile, r.KeyFile} {
		fi, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		sig += fmt.Sprintf("%d:%d;", fi.ModTime().UnixNano(), fi.Size())
	}
	return sig, nil
}

// Prime, mevcut dosya imzasini "yuklenmis" olarak isaretler (acilis yuklemesinden sonra).
func (r *FileReloader) Prime() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, err := r.signature(); err == nil {
		r.last = s
	}
}

// Check, dosyalar degistiyse yeniden yukler. force true ise imzaya bakmaz.
// Yuklendiyse true doner.
func (r *FileReloader) Check(force bool) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sig, err := r.signature()
	if err != nil {
		return false, err
	}
	if !force && sig == r.last {
		return false, nil
	}
	c, err := tls.LoadX509KeyPair(r.CertFile, r.KeyFile)
	if err != nil {
		return false, err
	}
	if len(c.Certificate) == 0 {
		return false, errors.New("sertifika zinciri bos")
	}
	if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
		c.Leaf = leaf
		if time.Now().After(leaf.NotAfter) {
			return false, errors.New("yeni sertifikanin suresi dolmus; eskisi korunuyor")
		}
	}
	r.Apply(&c)
	r.last = sig
	return true, nil
}

// Run, interval'de bir ve SIGHUP gelince Check cagirir; ctx bitince doner.
func (r *FileReloader) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		force := false
		select {
		case <-ctx.Done():
			return
		case <-hup:
			force = true
		case <-t.C:
		}
		ok, err := r.Check(force)
		switch {
		case err != nil && r.Log != nil:
			r.Log.Warn("sertifika yeniden yuklenemedi, eskisi korunuyor", "hata", err)
		case ok && r.Log != nil:
			r.Log.Info("TLS sertifikasi yeniden yuklendi", "sertifika", r.CertFile)
		}
	}
}
