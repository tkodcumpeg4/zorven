package session

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("secret decode: %v", err)
	}
	return NewManager(raw)
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	m := newTestManager(t)
	val, err := m.Issue(Session{UserID: "usr_1", TenantID: "ten_1", Login: "octocat", Method: "github"}, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	s, err := m.Verify(val)
	if err != nil {
		t.Fatalf("Verify gecerli cerezi reddetti: %v", err)
	}
	if s.Login != "octocat" || s.Method != "github" || s.TenantID != "ten_1" || s.UserID != "usr_1" {
		t.Fatalf("beklenmeyen oturum: %+v", s)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	m := newTestManager(t)
	val, _ := m.Issue(Session{UserID: "usr_1", TenantID: "ten_1", Login: "octocat", Method: "github"}, time.Hour)
	// Imzayi boz: son karakteri degistir.
	tampered := val[:len(val)-1] + "X"
	if _, err := m.Verify(tampered); err == nil {
		t.Fatal("bozuk imza kabul edildi")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	m := newTestManager(t)
	val, _ := m.Issue(Session{UserID: "usr_1", TenantID: "ten_1", Login: "octocat", Method: "github"}, time.Hour)
	// Payload'u degistir (imza artik tutmamali).
	_, sig, _ := strings.Cut(val, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker","method":"github","exp":9999999999}`))
	if _, err := m.Verify(forged + "." + sig); err == nil {
		t.Fatal("degistirilmis payload kabul edildi")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	m := newTestManager(t)
	val, _ := m.Issue(Session{UserID: "usr_1", TenantID: "ten_1", Login: "octocat", Method: "github"}, -time.Second) // gecmiste
	if _, err := m.Verify(val); err == nil {
		t.Fatal("suresi dolmus cerez kabul edildi")
	}
}

func TestVerifyRejectsForeignSecret(t *testing.T) {
	m1 := newTestManager(t)
	m2 := newTestManager(t)
	val, _ := m1.Issue(Session{UserID: "usr_1", TenantID: "ten_1", Login: "octocat", Method: "github"}, time.Hour)
	if _, err := m2.Verify(val); err == nil {
		t.Fatal("baska anahtarla imzalanmis cerez kabul edildi")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	m := newTestManager(t)
	for _, bad := range []string{"", "noseparator", "a.b.c", "...", "x."} {
		if _, err := m.Verify(bad); err == nil {
			t.Fatalf("cop girdi kabul edildi: %q", bad)
		}
	}
}

// --- Kiraci tasima ---------------------------------------------------------

// Cerez yetki kapsamini (kiraci) tasimali.
func TestIssueCarriesTenant(t *testing.T) {
	m := newTestManager(t)
	val, err := m.Issue(Session{
		UserID: "usr_1", TenantID: "ten_1", Login: "octocat", Method: "github",
	}, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := m.Verify(val)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != "usr_1" || got.TenantID != "ten_1" || got.Login != "octocat" {
		t.Fatalf("oturum bilgisi tasinmadi: %+v", got)
	}
}

// Kiracisiz cerez URETILEMEMELI: cerez yetki kapsamini tasir.
func TestIssueRejectsMissingTenant(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Issue(Session{UserID: "usr_1", Login: "octocat"}, time.Hour); err == nil {
		t.Fatal("kiracisiz oturum uretilmemeliydi")
	}
}

// Eski bicim (tid tasimayan) cerez GECERSIZ sayilmali: kabul edilseydi
// tenantFor bos kiraci ile calisir ve kapsam kontrolu sessizce cokerdi.
func TestVerifyRejectsLegacySessionWithoutTenant(t *testing.T) {
	m := newTestManager(t)
	legacy := m.signPayloadForTest(`{"sub":"octocat","method":"github","exp":9999999999}`)
	if _, err := m.Verify(legacy); err == nil {
		t.Fatal("kiracisiz eski cerez reddedilmeliydi")
	}
}

// signPayloadForTest, verilen ham JSON'u gecerli bicimde imzalar.
// Yalnizca testlerde kullanilir; eski bicim cerezleri uretmeye yarar.
func (m *Manager) signPayloadForTest(js string) string {
	b64 := base64.RawURLEncoding.EncodeToString([]byte(js))
	return b64 + "." + m.sign(b64)
}
