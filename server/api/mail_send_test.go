package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// mailStore, sendMail'in ihtiyac duydugu store cagrilarini taklit eder.
type mailStore struct {
	store.Store
	plan string
}

func (m *mailStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	return store.Tenant{ID: id, Slug: "acme"}, nil
}

func (m *mailStore) GetSubscription(ctx context.Context, tenantID string) (store.Subscription, error) {
	return store.Subscription{TenantID: tenantID, Plan: m.plan, Status: "active"}, nil
}

func (m *mailStore) InsertMailMessage(ctx context.Context, msg store.MailMessage) (store.MailMessage, error) {
	msg.ID = "mm_1"
	return msg, nil
}

func (m *mailStore) InsertMailAttachment(ctx context.Context, a store.MailAttachment) error {
	return nil
}

// fakeSMTP, RCPT TO adreslerini ve DATA icerigini kaydeden en kucuk SMTP sunucusu.
type fakeSMTP struct {
	mu    sync.Mutex
	rcpts []string
	data  []string
}

func startFakeSMTP(t *testing.T) (*fakeSMTP, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f, ln.Addr().String()
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
	w("220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			f.mu.Lock()
			f.rcpts = append(f.rcpts, strings.TrimSpace(line[len("RCPT TO:"):]))
			f.mu.Unlock()
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = append(f.data, b.String())
			f.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSendMail_ExternalRestrictionUsesEnvelopeAddress(t *testing.T) {
	smtpSrv, addr := startFakeSMTP(t)
	s := &Server{
		Store:      &mailStore{plan: "pro"},
		MailDomain: "mail.zorven.app",
		MailSender: mail.NewSender(addr, "mail.zorven.app"),
	}
	send := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/mail/send", bytes.NewReader(b))
		req = req.WithContext(withTenant(req.Context(), "ten_acme"))
		rec := httptest.NewRecorder()
		s.sendMail(rec, req)
		return rec
	}
	errCode := func(rec *httptest.ResponseRecorder) string {
		var out struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Error.Code
	}

	// Harici alici, gorunen adda ic adres olsa da: 403 ve SMTP'ye hic gitmez.
	for _, to := range []string{
		"victim@example.org",
		`"x@mail.zorven.app" <victim@example.org>`,
		"Victim@GMAIL.com",
	} {
		rec := send(map[string]any{"to": to, "subject": "s", "body": "b"})
		if rec.Code != http.StatusForbidden || errCode(rec) != "external_mail_not_allowed" {
			t.Fatalf("to=%q: 403 external_mail_not_allowed bekleniyordu, alinan %d %s", to, rec.Code, rec.Body.String())
		}
	}
	// Belirsiz / coklu / enjeksiyonlu alicilar: 422.
	for _, to := range []string{
		"bob@mail.zorven.app, victim@example.org",
		`"victim@example.org"@mail.zorven.app`,
		"bob@mail.zorven.app\r\nBcc: victim@example.org",
		"victim@example.org <bob@mail.zorven.app",
	} {
		rec := send(map[string]any{"to": to, "subject": "s", "body": "b"})
		if rec.Code != http.StatusUnprocessableEntity || errCode(rec) != "invalid_recipient" {
			t.Fatalf("to=%q: 422 invalid_recipient bekleniyordu, alinan %d %s", to, rec.Code, rec.Body.String())
		}
	}
	// In-Reply-To ve konu enjeksiyonu: 422.
	if rec := send(map[string]any{"to": "bob@mail.zorven.app", "body": "b", "in_reply_to": "<a@b>\r\nBcc: victim@example.org"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("in_reply_to CRLF 422 olmaliydi: %d", rec.Code)
	}
	if rec := send(map[string]any{"to": "bob@mail.zorven.app", "body": "b", "subject": "x\r\nBcc: victim@example.org"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("subject CRLF 422 olmaliydi: %d", rec.Code)
	}
	smtpSrv.mu.Lock()
	if len(smtpSrv.rcpts) != 0 {
		t.Fatalf("reddedilen istekler SMTP'ye ulasmamaliydi: %v", smtpSrv.rcpts)
	}
	smtpSrv.mu.Unlock()

	// Ic alici: 201, zarf alicisi tam olarak dogrulanan adres.
	rec := send(map[string]any{"to": "Bob <bob@Mail.Zorven.App>", "subject": "merhaba", "body": "b", "in_reply_to": "<x@y>"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("ic alici 201 olmaliydi: %d %s", rec.Code, rec.Body.String())
	}
	smtpSrv.mu.Lock()
	defer smtpSrv.mu.Unlock()
	if len(smtpSrv.rcpts) != 1 || smtpSrv.rcpts[0] != "<bob@mail.zorven.app>" {
		t.Fatalf("RCPT TO dogrulanan adres olmaliydi: %v", smtpSrv.rcpts)
	}
	if len(smtpSrv.data) != 1 || !strings.Contains(smtpSrv.data[0], "To: bob@mail.zorven.app\r\n") {
		t.Fatalf("To basligi dogrulanan adres olmaliydi: %v", smtpSrv.data)
	}
}

func TestSendMail_EnterpriseMayGoExternal(t *testing.T) {
	smtpSrv, addr := startFakeSMTP(t)
	s := &Server{
		Store:      &mailStore{plan: "enterprise"},
		MailDomain: "mail.zorven.app",
		MailSender: mail.NewSender(addr, "mail.zorven.app"),
	}
	b, _ := json.Marshal(map[string]any{"to": "Victim <victim@example.org>", "body": "b"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mail/send", bytes.NewReader(b))
	req = req.WithContext(withTenant(req.Context(), "ten_acme"))
	rec := httptest.NewRecorder()
	s.sendMail(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("enterprise harici gonderim 201 olmaliydi: %d %s", rec.Code, rec.Body.String())
	}
	smtpSrv.mu.Lock()
	defer smtpSrv.mu.Unlock()
	if len(smtpSrv.rcpts) != 1 || smtpSrv.rcpts[0] != "<victim@example.org>" {
		t.Fatalf("RCPT TO: %v", smtpSrv.rcpts)
	}
}
