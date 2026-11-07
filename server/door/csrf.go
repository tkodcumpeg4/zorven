package door

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kapi acma onayi icin CSRF jetonu.
//
// Kapi acma (grant olusturma) durum degistirir; GET ile TETIKLENEMEZ. Ziyaretci
// girisinden sonra gorunen onay sayfasi, imzali kisa omurlu bir jeton tasiyan
// POST formu icerir. Jeton (tunel, kapi hostu, istemci IP, bitis) uzerine HMAC'tir;
// baska siteden okunamaz (ayni kaynak sayfasi), baska tunel/host/IP icin gecmez.
//
// Anahtar: TokenKey tanimliysa ondan (alan ayrimli), degilse surece ozgu rastgele
// anahtar (tek surec; yeniden baslatmada acik onay sayfalari yenilenmelidir).

// CSRFTokenTTL, onay jetonunun omru.
const CSRFTokenTTL = 10 * time.Minute

var (
	csrfOnce sync.Once
	csrfProc []byte
)

func (s *Service) csrfKey() []byte {
	if len(s.TokenKey) > 0 {
		return s.TokenKey
	}
	csrfOnce.Do(func() {
		csrfProc = make([]byte, 32)
		_, _ = rand.Read(csrfProc)
	})
	return csrfProc
}

func (s *Service) csrfMAC(payload string) []byte {
	m := hmac.New(sha256.New, s.csrfKey())
	m.Write([]byte("zorven-door-csrf-v1|"))
	m.Write([]byte(payload))
	return m.Sum(nil)
}

func csrfPayload(tunnelID, host, ip string, exp int64) string {
	return strings.Join([]string{tunnelID, strings.ToLower(host), ip, strconv.FormatInt(exp, 10)}, "|")
}

// IssueCSRFToken, (tunel, kapi hostu, istemci IP) icin onay jetonu uretir.
func (s *Service) IssueCSRFToken(tunnelID, host, ip string) string {
	exp := s.now().Add(CSRFTokenTTL).Unix()
	payload := csrfPayload(tunnelID, host, NormalizeIP(ip), exp)
	return strconv.FormatInt(exp, 10) + "." + base64.RawURLEncoding.EncodeToString(s.csrfMAC(payload))
}

// VerifyCSRFToken, jetonun imzasini, suresini ve baglamini dogrular.
func (s *Service) VerifyCSRFToken(tok, tunnelID, host, ip string) bool {
	expStr, macStr, ok := strings.Cut(tok, ".")
	if !ok || len(tok) > 256 {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(macStr)
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, s.csrfMAC(csrfPayload(tunnelID, host, NormalizeIP(ip), exp))) {
		return false
	}
	return s.now().Unix() <= exp
}
