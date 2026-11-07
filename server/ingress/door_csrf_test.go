package ingress

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
)

func doorCSRFSetup(t *testing.T) (*Handler, *memDoorStore, *http.Cookie, string) {
	hash, _ := auth.HashSecret("s3cret")
	h, ms := newDoorHandler(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`, true)
	cookie := basicLogin(t, h, "alice", "s3cret")
	r := doorReq("GET", "/")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	m := regexp.MustCompile(`name="zv_csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		t.Fatalf("onay sayfasi beklendi (%d): %s", w.Code, w.Body.String())
	}
	return h, ms, cookie, m[1]
}

func doorPost(h *Handler, cookie *http.Cookie, token string, hdr map[string]string) *httptest.ResponseRecorder {
	pr := httptest.NewRequest("POST", "https://"+doorHost+"/", strings.NewReader(url.Values{"zv_csrf": {token}}.Encode()))
	pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		pr.Header.Set(k, v)
	}
	pr.RemoteAddr = "203.0.113.7:4444"
	pr.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, pr)
	return w
}

func TestDoorGETNeverOpens(t *testing.T) {
	_, ms, _, _ := doorCSRFSetup(t) // GET yapildi, onay sayfasi goruldu
	if activeGrants(ms) != 0 {
		t.Fatal("GET grant acmamali")
	}
}

func TestDoorOpenPostOrigin(t *testing.T) {
	h, ms, cookie, tok := doorCSRFSetup(t)
	own := "https://" + doorHost

	// baska site -> 403
	if w := doorPost(h, cookie, tok, map[string]string{"Origin": "https://evil.example.org"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", w.Code)
	}
	// Origin "null" -> 403
	if w := doorPost(h, cookie, tok, map[string]string{"Origin": "null"}); w.Code != http.StatusForbidden {
		t.Fatalf("null origin: %d", w.Code)
	}
	// Origin da Referer da yok -> 403
	if w := doorPost(h, cookie, tok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("basliksiz: %d", w.Code)
	}
	// Origin yok, baska siteden Referer -> 403
	if w := doorPost(h, cookie, tok, map[string]string{"Referer": "https://evil.example.org/x"}); w.Code != http.StatusForbidden {
		t.Fatalf("yabanci referer: %d", w.Code)
	}
	if activeGrants(ms) != 0 {
		t.Fatal("reddedilen isteklerde grant olusmamali")
	}
	// jeton yok / bozuk -> 403 (Origin dogru olsa da)
	for _, bad := range []string{"", "x.y", "9999999999." + strings.Repeat("A", 43)} {
		if w := doorPost(h, cookie, bad, map[string]string{"Origin": own}); w.Code != http.StatusForbidden {
			t.Fatalf("gecersiz jeton %q: %d", bad, w.Code)
		}
	}
	if h.Door.VerifyCSRFToken(tok, "tun_other", doorHost, "203.0.113.7") {
		t.Fatal("jeton baska tunel icin gecmemeli")
	}
	if h.Door.VerifyCSRFToken(tok, "tun_1", doorHost, "198.51.100.1") {
		t.Fatal("jeton baska IP icin gecmemeli")
	}
	if activeGrants(ms) != 0 {
		t.Fatal("gecersiz jetonla grant olusmamali")
	}
	// Origin yok ama ayni host Referer -> acilir
	if w := doorPost(h, cookie, tok, map[string]string{"Referer": own + "/"}); w.Code != http.StatusOK || activeGrants(ms) != 1 {
		t.Fatalf("referer ile acma: %d grants=%d", w.Code, activeGrants(ms))
	}
	// dogru Origin -> acilir
	if w := doorPost(h, cookie, tok, map[string]string{"Origin": own}); w.Code != http.StatusOK {
		t.Fatalf("origin ile acma: %d", w.Code)
	}
}

func TestDoorOpenPostRequiresLogin(t *testing.T) {
	hash, _ := auth.HashSecret("s3cret")
	h, ms := newDoorHandler(t, "basic", `{"username":"alice","password_hash":"`+hash+`"}`, true)
	tok := h.Door.IssueCSRFToken("tun_1", doorHost, "203.0.113.7")
	pr := httptest.NewRequest("POST", "https://"+doorHost+"/", strings.NewReader(url.Values{"zv_csrf": {tok}}.Encode()))
	pr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pr.Header.Set("Origin", "https://"+doorHost)
	pr.RemoteAddr = "203.0.113.7:4444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, pr)
	if w.Code != http.StatusUnauthorized || activeGrants(ms) != 0 {
		t.Fatalf("girissiz POST grant acmamali: %d", w.Code)
	}
}

func TestDoorCloseRequiresSameOrigin(t *testing.T) {
	h, _, cookie, _ := doorCSRFSetup(t)
	cr := httptest.NewRequest("POST", "https://"+doorHost+doorClosePath, nil)
	cr.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, cr)
	if w.Code != http.StatusForbidden {
		t.Fatalf("Origin/Referer'siz kapatma 403 olmali: %d", w.Code)
	}
}
