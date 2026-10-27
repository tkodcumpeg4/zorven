package botguard

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// TLSFingerprint, ClientHello'dan cikarilan JA4 BENZERI parmak izi. Go'nun
// ClientHelloInfo'su ham uzanti listesini vermedigi icin gercek JA4 uretilemez;
// surum, sifre takimi (GREASE ayiklanmis, sirali), ALPN, egri/imza algoritmalari
// ozeti kullanilir. Amac eslesme degil, "tarayici gibi mi" sinyalidir.
type TLSFingerprint struct {
	Summary     string // or. t13d1516h2_<12hex>_<12hex>
	CipherCount int    // GREASE haric
	HasGREASE   bool
	HasTLS13    bool
	ALPN        []string
	At          time.Time
}

func isGREASE(v uint16) bool { return v&0x0f0f == 0x0a0a && byte(v>>8) == byte(v) }

func short(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// FingerprintOf, ClientHelloInfo'dan parmak izi cikarir.
func FingerprintOf(h *tls.ClientHelloInfo) *TLSFingerprint {
	fp := &TLSFingerprint{ALPN: append([]string(nil), h.SupportedProtos...)}
	var ciphers []uint16
	for _, c := range h.CipherSuites {
		if isGREASE(c) {
			fp.HasGREASE = true
			continue
		}
		ciphers = append(ciphers, c)
	}
	fp.CipherCount = len(ciphers)
	for _, v := range h.SupportedVersions {
		if isGREASE(v) {
			fp.HasGREASE = true
			continue
		}
		if v == tls.VersionTLS13 {
			fp.HasTLS13 = true
		}
	}
	for _, c := range h.SupportedCurves {
		if isGREASE(uint16(c)) {
			fp.HasGREASE = true
		}
	}
	sort.Slice(ciphers, func(i, j int) bool { return ciphers[i] < ciphers[j] })
	cs := fmt.Sprint(ciphers)
	var ext []string
	for _, c := range h.SupportedCurves {
		if !isGREASE(uint16(c)) {
			ext = append(ext, fmt.Sprintf("c%04x", uint16(c)))
		}
	}
	for _, s := range h.SignatureSchemes {
		if !isGREASE(uint16(s)) {
			ext = append(ext, fmt.Sprintf("s%04x", uint16(s)))
		}
	}
	alpn := "00"
	if n := len(fp.ALPN); n > 0 {
		a := fp.ALPN[0]
		alpn = string(a[0]) + string(a[len(a)-1])
	}
	ver := "12"
	if fp.HasTLS13 {
		ver = "13"
	}
	fp.Summary = fmt.Sprintf("t%sd%02d%02d%s_%s_%s", ver, fp.CipherCount, len(fp.ALPN), alpn, short(cs), short(ext...))
	return fp
}

// TLSFPStore, baglanti (uzak adres "ip:port") -> parmak izi. Bellek sinirlidir
// (cap) ve girdiler TTL sonunda atilir; kayip girdi = sinyal yok (guvenli taraf).
type TLSFPStore struct {
	mu  sync.Mutex
	m   map[string]*TLSFingerprint
	cap int
	ttl time.Duration
	now func() time.Time
}

// NewTLSFPStore, varsayilan 100k girdi / 30 dk.
func NewTLSFPStore() *TLSFPStore {
	return &TLSFPStore{m: make(map[string]*TLSFingerprint), cap: 100_000, ttl: 30 * time.Minute, now: time.Now}
}

// Record, el sikismada cagrilir (GetConfigForClient).
func (s *TLSFPStore) Record(h *tls.ClientHelloInfo) {
	if s == nil || h == nil || h.Conn == nil {
		return
	}
	fp := FingerprintOf(h)
	key := h.Conn.RemoteAddr().String()
	now := s.now()
	fp.At = now
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) >= s.cap {
		for k, v := range s.m {
			if now.Sub(v.At) > s.ttl {
				delete(s.m, k)
			}
		}
		if len(s.m) >= s.cap {
			return // dolu: yeni baglanti icin sinyal yok
		}
	}
	s.m[key] = fp
}

// Get, uzak adrese ait parmak izi (yoksa/suresi dolduysa nil).
func (s *TLSFPStore) Get(remoteAddr string) *TLSFingerprint {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fp := s.m[remoteAddr]
	if fp == nil {
		return nil
	}
	if s.now().Sub(fp.At) > s.ttl {
		delete(s.m, remoteAddr)
		return nil
	}
	return fp
}

// Forget, baglanti kapaninca girdiyi siler (ConnState ile).
func (s *TLSFPStore) Forget(remoteAddr string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.m, remoteAddr)
	s.mu.Unlock()
}
