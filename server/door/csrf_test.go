package door

import (
	"testing"
	"time"
)

func TestCSRFTokenExpiryAndBinding(t *testing.T) {
	now := time.Now()
	s := New(nil, nil, nil)
	s.Now = func() time.Time { return now }
	tok := s.IssueCSRFToken("tun_1", "Door.Example.com", "203.0.113.7")
	if !s.VerifyCSRFToken(tok, "tun_1", "door.example.com", "203.0.113.7") {
		t.Fatal("gecerli jeton reddedildi")
	}
	if s.VerifyCSRFToken(tok, "tun_2", "door.example.com", "203.0.113.7") ||
		s.VerifyCSRFToken(tok, "tun_1", "other.example.com", "203.0.113.7") ||
		s.VerifyCSRFToken(tok, "tun_1", "door.example.com", "203.0.113.8") {
		t.Fatal("jeton baglam disinda gecti")
	}
	now = now.Add(CSRFTokenTTL + time.Second)
	if s.VerifyCSRFToken(tok, "tun_1", "door.example.com", "203.0.113.7") {
		t.Fatal("suresi dolmus jeton gecti")
	}
}
