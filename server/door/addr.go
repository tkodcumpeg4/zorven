package door

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Diger IP ailesi yan kanali.
//
// Sorun: ziyaretci kapiyi tarayicidan IPv4 ile acar, oyun/SSH istemcisi IPv6 ile baglanir
// (ya da tersi); grant yalniz tarayicinin adresine verildiginden baglanti reddedilir.
//
// Cozum: kapi sayfasi giris basarili olunca, KENDI ailesinin disindaki aileyi tek adresli
// bir hosta (door4.<platform> yalniz A, door6.<platform> yalniz AAAA) kisa omurlu imzali
// bir tokenla fetch eder. Uc, istegin kaynak adresini grant'e ek adres olarak baglar.
// Her sey en-iyi-caba: host cozulmez ya da istek basarisiz olursa ana grant etkilenmez.

// AddrPath, yan kanal ucunun yolu.
const AddrPath = "/_door/v1/addr"

// AddrTokenTTL, token omru.
const AddrTokenTTL = 2 * time.Minute

// Yan kanal hatalari.
var (
	ErrAddrDisabled = errors.New("diger aile yan kanali kapali")
	ErrAddrToken    = errors.New("gecersiz ya da suresi dolmus token")
	ErrAddrReplay   = errors.New("token bu aile icin zaten kullanildi")
	ErrAddrFamily   = errors.New("istek adresi hostun ailesiyle uyusmuyor")
	ErrAddrSame     = errors.New("adres grant'in ana adresiyle ayni ailede")
)

// AddrStore, yan kanalin ihtiyac duydugu ek depo yuzeyi (Store'dan opsiyonel).
type AddrStore interface {
	AddDoorGrantAddr(ctx context.Context, grantID, ip string) (store.DoorGrant, bool, error)
	DoorGrantAddrsByIP(ctx context.Context, tenantID, tunnelID, ip string) ([]string, error)
}

// allIPs, grant'in ana + ek adresleri.
func allIPs(g store.DoorGrant) []string {
	out := make([]string, 0, 1+len(g.ExtraIPs))
	out = append(out, g.IP)
	return append(out, g.ExtraIPs...)
}

// AddrEnabled, yan kanal kullanilabilir mi (anahtar var ve depo destekliyor).
func (s *Service) AddrEnabled() bool {
	if len(s.TokenKey) == 0 {
		return false
	}
	_, ok := s.St.(AddrStore)
	return ok
}

// AddrHost, grant'in ana adresinin AILESINDEN FARKLI ailenin yan kanal hostunu doner
// ("" = yan kanal yok / host tanimsiz).
func (s *Service) AddrHost(primaryIP string) string {
	if !s.AddrEnabled() {
		return ""
	}
	p := net.ParseIP(primaryIP)
	if p == nil {
		return ""
	}
	if p.To4() != nil {
		return s.V6Host
	}
	return s.V4Host
}

type addrClaims struct {
	GrantID  string
	TunnelID string
	Host     string // kapi sayfasinin hostu (CORS Origin kontrolu)
	Exp      time.Time
}

func (s *Service) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.TokenKey)
	m.Write([]byte("zorven-door-addr-v1|"))
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// IssueAddrToken, grant icin imzali kisa omurlu token uretir. Yan kanal kapaliysa "".
func (s *Service) IssueAddrToken(g store.DoorGrant, doorHost string) string {
	if !s.AddrEnabled() || g.ID == "" {
		return ""
	}
	payload := strings.Join([]string{
		g.ID, g.TunnelID, strings.ToLower(doorHost),
		strconv.FormatInt(s.now().Add(AddrTokenTTL).Unix(), 10),
	}, "|")
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(s.mac(payload))
}

// ParseAddrToken, tokenin imzasini ve suresini dogrular.
func (s *Service) ParseAddrToken(tok string) (addrClaims, error) {
	var c addrClaims
	if len(s.TokenKey) == 0 {
		return c, ErrAddrDisabled
	}
	i := strings.IndexByte(tok, '.')
	if i <= 0 {
		return c, ErrAddrToken
	}
	pb, err1 := base64.RawURLEncoding.DecodeString(tok[:i])
	sig, err2 := base64.RawURLEncoding.DecodeString(tok[i+1:])
	if err1 != nil || err2 != nil {
		return c, ErrAddrToken
	}
	if !hmac.Equal(sig, s.mac(string(pb))) {
		return c, ErrAddrToken
	}
	parts := strings.Split(string(pb), "|")
	if len(parts) != 4 {
		return c, ErrAddrToken
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return c, ErrAddrToken
	}
	c = addrClaims{GrantID: parts[0], TunnelID: parts[1], Host: parts[2], Exp: time.Unix(exp, 0)}
	if !s.now().Before(c.Exp) {
		return addrClaims{}, ErrAddrToken
	}
	return c, nil
}

// useToken, (token, aile) cifti icin tek kullanimi atomik isaretler; ikinci kez false.
func (s *Service) useToken(tok string, fam int, exp time.Time) bool {
	h := sha256.Sum256([]byte(tok))
	key := string(h[:]) + strconv.Itoa(fam)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addrUsed == nil {
		s.addrUsed = map[string]time.Time{}
	}
	if len(s.addrUsed) > 256 {
		for k, e := range s.addrUsed {
			if !e.After(now) {
				delete(s.addrUsed, k)
			}
		}
	}
	if e, ok := s.addrUsed[key]; ok && e.After(now) {
		return false
	}
	s.addrUsed[key] = exp
	return true
}

func ipFamily(ip string) int {
	p := net.ParseIP(ip)
	if p == nil {
		return 0
	}
	if p.To4() != nil {
		return 4
	}
	return 6
}

// LearnAddr, tokenin grant'ine fam ailesinden remote adresini ek adres olarak baglar.
// remote'un ailesi fam ile uyusmuyorsa (ornegin v6 uc v4 uzerinden cagrildi) token
// YAKILMAZ ve ErrAddrFamily doner. Basarida normalize edilmis adres, token claim'leri ve
// guncel grant doner.
func (s *Service) LearnAddr(ctx context.Context, tok string, fam int, remote string) (string, addrClaims, error) {
	as, ok := s.St.(AddrStore)
	if !ok || len(s.TokenKey) == 0 {
		return "", addrClaims{}, ErrAddrDisabled
	}
	c, err := s.ParseAddrToken(tok)
	if err != nil {
		return "", c, err
	}
	ip := NormalizeIP(remote)
	if ip == "" || ipFamily(ip) != fam {
		return "", c, ErrAddrFamily
	}
	if !s.useToken(tok, fam, c.Exp) {
		return "", c, ErrAddrReplay
	}
	g, added, err := as.AddDoorGrantAddr(ctx, c.GrantID, ip)
	if err != nil {
		return "", c, err
	}
	if g.TunnelID != c.TunnelID {
		return "", c, ErrAddrToken
	}
	if !added {
		return ip, c, ErrAddrSame
	}
	s.Invalidate(c.TunnelID)
	s.emit(accesslog.Event{
		TenantID: g.TenantID, TunnelID: g.TunnelID, Hostname: c.Host,
		Method: g.Method, Identity: g.Identity, Success: true,
		Reason: accesslog.ReasonDoorAddrLearned, ClientIP: ip,
	})
	return ip, c, nil
}

// AddrHandler, door4/door6 hostlarindaki GET /_door/v1/addr?t=<token> ucunu sunar.
// Host V4Host ise aile 4, V6Host ise 6; baska host 404. clientIP istek kaynagini verir
// (nil ise RemoteAddr). CORS: yanit yalniz tokendaki kapi sayfasi origin'ine acilir.
func (s *Service) AddrHandler(clientIP func(*http.Request) string) http.Handler {
	if clientIP == nil {
		clientIP = func(r *http.Request) string {
			h, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				return r.RemoteAddr
			}
			return h
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("Cache-Control", "no-store")
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Vary", "Origin")
		fam := 0
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch {
		case s.V4Host != "" && host == strings.ToLower(s.V4Host):
			fam = 4
		case s.V6Host != "" && host == strings.ToLower(s.V6Host):
			fam = 6
		}
		if fam == 0 || r.URL.Path != AddrPath || !s.AddrEnabled() {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			hdr.Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		tok := r.URL.Query().Get("t")
		c, perr := s.ParseAddrToken(tok)
		// Origin: varsa tokendaki kapi hostuyla birebir ayni olmali; ACAO yalniz o zaman.
		if o := r.Header.Get("Origin"); o != "" {
			if perr != nil || o != "https://"+c.Host {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "origin"})
				return
			}
			hdr.Set("Access-Control-Allow-Origin", o)
		}
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		ip, _, err := s.LearnAddr(ctx, tok, fam, clientIP(r))
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{"ip": ip, "family": fam})
		case errors.Is(err, ErrAddrSame):
			writeJSON(w, http.StatusOK, map[string]any{"ip": ip, "family": fam, "noop": true})
		case errors.Is(err, ErrAddrFamily):
			writeJSON(w, http.StatusConflict, map[string]any{"error": "family"})
		case errors.Is(err, ErrAddrReplay):
			writeJSON(w, http.StatusConflict, map[string]any{"error": "replay"})
		case errors.Is(err, ErrAddrToken), errors.Is(err, store.ErrNotFound):
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token"})
		default:
			s.logWarn("door: ek adres baglanamadi", "hata", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal"})
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
