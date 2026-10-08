package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// localMailStore, yerel teslim + panel listesi testleri icin bellek ici store.
type localMailStore struct {
	store.Store
	mu       sync.Mutex
	tenants  map[string]store.Tenant // slug -> tenant
	msgs     []store.MailMessage
	listArgs []string
}

func (m *localMailStore) GetTenant(_ context.Context, id string) (store.Tenant, error) {
	for _, t := range m.tenants {
		if t.ID == id {
			return t, nil
		}
	}
	return store.Tenant{}, store.ErrNotFound
}

func (m *localMailStore) GetTenantBySlug(_ context.Context, slug string) (store.Tenant, error) {
	if t, ok := m.tenants[slug]; ok {
		return t, nil
	}
	return store.Tenant{}, store.ErrNotFound
}

func (m *localMailStore) GetSubscription(_ context.Context, tenantID string) (store.Subscription, error) {
	return store.Subscription{TenantID: tenantID, Plan: "free", Status: "active"}, nil
}

func (m *localMailStore) InsertMailMessage(_ context.Context, msg store.MailMessage) (store.MailMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg.ID = "mm_" + string(rune('a'+len(m.msgs)))
	m.msgs = append(m.msgs, msg)
	return msg, nil
}

func (m *localMailStore) InsertMailAttachment(context.Context, store.MailAttachment) error { return nil }

func (m *localMailStore) ListMailMessages(_ context.Context, _ string, folder string, _ int) ([]store.MailMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listArgs = append(m.listArgs, folder)
	return nil, nil
}

func newLocalMailServer(t *testing.T) (*Server, *localMailStore, *fakeSMTP) {
	t.Helper()
	smtpSrv, addr := startFakeSMTP(t)
	st := &localMailStore{tenants: map[string]store.Tenant{
		"acme": {ID: "ten_acme", Slug: "acme"},
		"bob":  {ID: "ten_bob", Slug: "bob"},
	}}
	sender := mail.NewSender(addr, "mail.zorven.app")
	sender.Local = mail.NewLocal(st, "mail.zorven.app", "zorven.app", []string{"info"})
	return &Server{Store: st, MailDomain: "mail.zorven.app", MailSender: sender}, st, smtpSrv
}

func panelSend(s *Server, to string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"to": to, "subject": "Merhaba", "body": "govde"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mail/send", bytes.NewReader(b))
	req = req.WithContext(withTenant(req.Context(), "ten_acme"))
	rec := httptest.NewRecorder()
	s.sendMail(rec, req)
	return rec
}

// Panelden yerel kutuya (tenant ve sistem kutusu) gonderim relay'e GITMEZ,
// alicinin INBOX kaydi olarak teslim edilir.
func TestSendMail_LocalRecipientsDeliveredNotRelayed(t *testing.T) {
	s, st, smtpSrv := newLocalMailServer(t)
	for _, to := range []string{"bob@mail.zorven.app", "info@zorven.app"} {
		if rec := panelSend(s, to); rec.Code != http.StatusCreated {
			t.Fatalf("to=%s: %d %s", to, rec.Code, rec.Body.String())
		}
	}
	smtpSrv.mu.Lock()
	n := len(smtpSrv.rcpts)
	smtpSrv.mu.Unlock()
	if n != 0 {
		t.Fatalf("yerel mail relay'e gitti: %d RCPT", n)
	}
	var bobIn, infoIn, sent int
	for _, m := range st.msgs {
		switch {
		case m.Direction == "inbound" && m.TenantID == "ten_bob" && m.Folder == store.MailFolderInbox && m.Mailbox == "bob@mail.zorven.app" && len(m.RawData()) > 0:
			bobIn++
		case m.Direction == "inbound" && m.TenantID == store.DefaultTenantID && m.Mailbox == "info@zorven.app":
			infoIn++
		case m.Direction == "outbound" && m.TenantID == "ten_acme":
			sent++
		}
	}
	if bobIn != 1 || infoIn != 1 || sent != 2 {
		t.Fatalf("bob=%d info=%d sent=%d; kayitlar: %+v", bobIn, infoIn, sent, st.msgs)
	}
}

// Var olmayan yerel adres: acik hata, hicbir sey kaydedilmez/relay'e gitmez.
func TestSendMail_UnknownLocalRecipientRejected(t *testing.T) {
	s, st, smtpSrv := newLocalMailServer(t)
	for _, to := range []string{"yok@mail.zorven.app", "nobody@zorven.app"} {
		rec := panelSend(s, to)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "unknown_recipient") {
			t.Fatalf("to=%s: 422 unknown_recipient bekleniyordu: %d %s", to, rec.Code, rec.Body.String())
		}
	}
	if len(st.msgs) != 0 || len(smtpSrv.rcpts) != 0 {
		t.Fatalf("hicbir sey kaydedilmemeli: %d kayit, %d rcpt", len(st.msgs), len(smtpSrv.rcpts))
	}
}

// Panel listesi klasore gore: box=inbox|sent|trash.
func TestListMail_UsesFolder(t *testing.T) {
	s, st, _ := newLocalMailServer(t)
	for _, box := range []string{"", "inbox", "sent", "trash"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/messages?box="+box, nil)
		req = req.WithContext(withTenant(req.Context(), "ten_acme"))
		rec := httptest.NewRecorder()
		s.listMail(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("box=%s: %d", box, rec.Code)
		}
	}
	want := []string{"inbox", "inbox", "sent", "trash"}
	if len(st.listArgs) != len(want) {
		t.Fatalf("cagrilar: %v", st.listArgs)
	}
	for i := range want {
		if st.listArgs[i] != want[i] {
			t.Fatalf("cagrilar: %v, beklenen %v", st.listArgs, want)
		}
	}
}
