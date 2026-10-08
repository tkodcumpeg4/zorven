package mail

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const (
	testBox    = "acme@mail.zorven.app"
	testTenant = "ten_a"
)

type imapEnv struct {
	st   *memMailStore
	hub  *Hub
	srv  *IMAPServer
	addr string
	pw   string
	pwID string
}

func newIMAPEnv(t *testing.T) *imapEnv {
	t.Helper()
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", "pro")
	pw := GenerateAppPassword()
	rec := st.addAppPassword(testTenant, testBox, pw)
	auth := NewAuthenticator(st, "mail.zorven.app", "zorven.app", []string{"info"}, nil)
	hub := NewHub()
	srv := NewIMAPServer(auth, st, hub, nil, true, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &imapEnv{st: st, hub: hub, srv: srv, addr: ln.Addr().String(), pw: pw, pwID: rec.ID}
}

func (e *imapEnv) seed(subject, mid, body string) store.MailMessage {
	raw := "From: Ali <ali@example.com>\r\nTo: " + testBox + "\r\nSubject: " + subject +
		"\r\nMessage-ID: " + mid + "\r\nDate: Mon, 02 Jan 2006 15:04:05 +0000\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n"
	m, err := e.st.InsertMailMessage(context.Background(), store.MailMessage{
		TenantID: testTenant, Direction: "inbound", From: "ali@example.com", To: testBox,
		Subject: subject, TextBody: body, MessageID: mid, Raw: raw})
	if err != nil {
		panic(err)
	}
	return m
}

func (e *imapEnv) dial(t *testing.T, opts *imapclient.Options) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(e.addr, opts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (e *imapEnv) login(t *testing.T) *imapclient.Client {
	t.Helper()
	c := e.dial(t, nil)
	if err := c.Login(testBox, e.pw).Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func TestIMAP_LoginRules(t *testing.T) {
	e := newIMAPEnv(t)

	c := e.dial(t, nil)
	if err := c.Login(testBox, "yanlis-parola").Wait(); err == nil {
		t.Fatal("yanlis parola kabul edildi")
	}
	c2 := e.dial(t, nil)
	if err := c2.Login("baska@mail.zorven.app", e.pw).Wait(); err == nil {
		t.Fatal("bilinmeyen kutu kabul edildi")
	}
	// Dogru parola; bosluk/tire/buyuk harf farki onemsiz.
	c3 := e.dial(t, nil)
	if err := c3.Login(strings.ToUpper(testBox), strings.ToUpper(strings.ReplaceAll(e.pw, "-", " "))).Wait(); err != nil {
		t.Fatalf("normalize edilmis giris basarisiz: %v", err)
	}
	if e.st.passwordByID(e.pwID).LastUsedAt == nil {
		t.Fatal("last_used_at guncellenmedi")
	}
	// Iptal edilen parola artik girmez.
	e.st.revokePassword(e.pwID)
	c4 := e.dial(t, nil)
	if err := c4.Login(testBox, e.pw).Wait(); err == nil {
		t.Fatal("iptal edilen parola kabul edildi")
	}
}

func TestIMAP_ListSelectFetchStore(t *testing.T) {
	e := newIMAPEnv(t)
	m1 := e.seed("Merhaba dunya", "<one@x>", "Birinci govde")
	// Eski kayit: ham (raw) yok; alanlardan yeniden olusturulmali.
	legacy, _ := e.st.InsertMailMessage(context.Background(), store.MailMessage{
		TenantID: testTenant, Direction: "inbound", From: "Veli <veli@example.com>", To: testBox,
		Subject: "Eski kayit çş", TextBody: "Eski govde", MessageID: "<legacy@x>"})
	_ = m1

	c := e.login(t)

	boxes, err := c.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	attrs := map[string][]imap.MailboxAttr{}
	for _, b := range boxes {
		attrs[b.Mailbox] = b.Attrs
	}
	for _, name := range []string{"INBOX", "Sent", "Trash", "Drafts"} {
		if _, ok := attrs[name]; !ok {
			t.Fatalf("%s listede yok: %v", name, attrs)
		}
	}
	has := func(name string, a imap.MailboxAttr) bool {
		for _, x := range attrs[name] {
			if x == a {
				return true
			}
		}
		return false
	}
	if !has("Sent", imap.MailboxAttrSent) || !has("Trash", imap.MailboxAttrTrash) || !has("Drafts", imap.MailboxAttrDrafts) {
		t.Fatalf("special-use eksik: %v", attrs)
	}

	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if sel.NumMessages != 2 || sel.UIDNext != 3 || sel.UIDValidity == 0 {
		t.Fatalf("select beklenmedik: %+v", sel)
	}

	bodySec := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.SeqSetNum(1, 2), &imap.FetchOptions{
		UID: true, Flags: true, RFC822Size: true, InternalDate: true, Envelope: true,
		BodyStructure: &imap.FetchItemBodyStructure{}, BodySection: []*imap.FetchItemBodySection{bodySec},
	}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("%d mesaj geldi", len(msgs))
	}
	if msgs[0].UID != 1 || msgs[1].UID != 2 {
		t.Fatalf("uid sirasi: %d %d", msgs[0].UID, msgs[1].UID)
	}
	if msgs[0].Envelope == nil || msgs[0].Envelope.Subject != "Merhaba dunya" {
		t.Fatalf("envelope: %+v", msgs[0].Envelope)
	}
	b0 := msgs[0].FindBodySection(bodySec)
	if !strings.Contains(string(b0), "Birinci govde") || int64(len(b0)) != msgs[0].RFC822Size {
		t.Fatalf("govde/boyut tutarsiz: size=%d len=%d", msgs[0].RFC822Size, len(b0))
	}
	// Yeniden olusturulan eski kayit gecerli RFC822 ve boyutu tutarli.
	b1 := string(msgs[1].FindBodySection(bodySec))
	if !strings.Contains(b1, "Subject: =?utf-8?q?") && !strings.Contains(b1, "Subject: Eski") {
		t.Fatalf("yeniden olusturulan baslik yok:\n%s", b1)
	}
	if !strings.Contains(b1, "Eski govde") || int64(len(b1)) != msgs[1].RFC822Size {
		t.Fatalf("eski kayit govdesi/boyutu: size=%d len=%d\n%s", msgs[1].RFC822Size, len(b1), b1)
	}
	if msgs[1].Envelope == nil || msgs[1].Envelope.Subject != "Eski kayit çş" {
		t.Fatalf("eski kayit envelope: %+v", msgs[1].Envelope)
	}
	if msgs[1].BodyStructure == nil {
		t.Fatal("bodystructure yok")
	}
	if len(e.st.get(legacy.ID).RawData()) == 0 {
		t.Fatal("yeniden olusturulan ham mesaj saklanmadi")
	}

	// STORE \Seen DB'ye yansir; BODY[] (peek olmadan) da okundu yapar.
	if err := c.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true,
		Flags: []imap.Flag{imap.FlagSeen, imap.FlagFlagged}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if got := e.st.get(m1.ID); !got.Seen || !got.Flagged {
		t.Fatalf("flag DB'ye yansimadi: %+v", got)
	}
	if _, err := c.Fetch(imap.UIDSetNum(2), &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{{}}}).Collect(); err != nil {
		t.Fatal(err)
	}
	if !e.st.get(legacy.ID).Seen {
		t.Fatal("BODY[] okuma \\Seen yapmadi")
	}
	if err := c.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true,
		Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if e.st.get(m1.ID).Seen {
		t.Fatal("\\Seen kaldirilamadi")
	}

	// SEARCH: konu ve bayrak.
	sd, err := c.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "dunya"}}}, nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if uids := sd.AllUIDs(); len(uids) != 1 || uids[0] != 1 {
		t.Fatalf("search sonucu: %v", sd.AllUIDs())
	}
	sd, err = c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if uids := sd.AllUIDs(); len(uids) != 1 || uids[0] != 1 {
		t.Fatalf("okunmamis arama: %v", sd.AllUIDs())
	}
}

func TestIMAP_AppendSentDedupeMoveExpunge(t *testing.T) {
	e := newIMAPEnv(t)
	e.seed("bir", "<one@x>", "g1")
	e.seed("iki", "<two@x>", "g2")
	c := e.login(t)

	sentRaw := "From: " + testBox + "\r\nTo: x@example.com\r\nSubject: Giden\r\nMessage-ID: <sent1@mail.zorven.app>\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n\r\ngiden govde\r\n"
	appendRaw := func(box, raw string) *imap.AppendData {
		t.Helper()
		cmd := c.Append(box, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}})
		if _, err := cmd.Write([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Close(); err != nil {
			t.Fatal(err)
		}
		d, err := cmd.Wait()
		if err != nil {
			t.Fatalf("append %s: %v", box, err)
		}
		return d
	}

	// SMTP gonderim sunucusunun zaten sakladigi kopya: tekrar eklenmemeli.
	pre, _ := e.st.InsertMailMessage(context.Background(), store.MailMessage{TenantID: testTenant, Direction: "outbound",
		From: testBox, To: "x@example.com", Subject: "Giden", MessageID: "<sent1@mail.zorven.app>",
		Folder: store.MailFolderSent, Mailbox: testBox, Seen: true, Raw: sentRaw})
	d1 := appendRaw("Sent", sentRaw)
	if d1.UID != imap.UID(pre.UID) {
		t.Fatalf("APPEND tekillesmedi: uid=%d, mevcut=%d", d1.UID, pre.UID)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailFolderSent)); n != 1 {
		t.Fatalf("Sent'te %d mesaj, beklenen 1", n)
	}
	// Farkli Message-ID eklenir.
	d2 := appendRaw("Sent", strings.Replace(sentRaw, "<sent1@", "<sent2@", 1))
	if d2.UID == d1.UID || len(e.st.all(testTenant, testBox, store.MailFolderSent)) != 2 {
		t.Fatalf("yeni gonderilen eklenmedi: %+v", d2)
	}
	// Taslak: Draft bayragi ve panel-disi klasor.
	draftRaw := "From: " + testBox + "\r\nTo: y@example.com\r\nSubject: Taslak\r\nMessage-ID: <d1@x>\r\n\r\ntaslak\r\n"
	appendRaw("Drafts", draftRaw)
	if n := len(e.st.all(testTenant, testBox, store.MailFolderDrafts)); n != 1 {
		t.Fatalf("taslak sayisi %d", n)
	}

	// MOVE -> Trash.
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	mv, err := c.Move(imap.UIDSetNum(2), "Trash").Wait()
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if mv.UIDValidity == 0 {
		t.Fatalf("COPYUID eksik: %+v", mv)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailFolderInbox)); n != 1 {
		t.Fatalf("INBOX'ta %d mesaj, beklenen 1", n)
	}
	tr := e.st.all(testTenant, testBox, store.MailFolderTrash)
	if len(tr) != 1 || tr[0].Subject != "" && tr[0].TextBody != "g2" {
		t.Fatalf("Trash: %+v", tr)
	}
	if st, err := c.Status("Trash", &imap.StatusOptions{NumMessages: true, UIDNext: true}).Wait(); err != nil || *st.NumMessages != 1 {
		t.Fatalf("status: %+v %v", st, err)
	}

	// \Deleted + EXPUNGE kalici siler (panel silmesiyle tutarli).
	if err := c.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true,
		Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailFolderInbox)); n != 1 {
		t.Fatal("EXPUNGE oncesi mesaj silindi")
	}
	got, err := c.Expunge().Collect()
	if err != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("expunge: %v %v", got, err)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailFolderInbox)); n != 0 {
		t.Fatalf("INBOX'ta %d mesaj kaldi", n)
	}
	// Baska posta kutusunun mesajlari gorunmez.
	other := "baska@mail.zorven.app"
	if n := len(e.st.all(testTenant, other, store.MailFolderInbox)); n != 0 {
		t.Fatal("baska kutu mesaji sizdi")
	}
}

func TestIMAP_IdleNewMail(t *testing.T) {
	e := newIMAPEnv(t)
	e.seed("bir", "<one@x>", "g1")

	got := make(chan uint32, 4)
	c := e.dial(t, &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{
		Mailbox: func(d *imapclient.UnilateralDataMailbox) {
			if d.NumMessages != nil {
				got <- *d.NumMessages
			}
		},
	}})
	if err := c.Login(testBox, e.pw).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	idle, err := c.Idle()
	if err != nil {
		t.Fatalf("idle: %v", err)
	}
	defer idle.Close()

	time.Sleep(100 * time.Millisecond)
	e.seed("yeni", "<two@x>", "g2")
	e.hub.Notify(testTenant, testBox)

	select {
	case n := <-got:
		if n != 2 {
			t.Fatalf("EXISTS=%d, beklenen 2", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IDLE yeni mail bildirimi gelmedi")
	}
}

func TestReconstructRawAttachments(t *testing.T) {
	raw := ReconstructRaw(store.MailMessage{ID: "m1", From: "a@b.c", To: "d@e.f", Subject: "Ek", TextBody: "metin",
		ReceivedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		[]Attachment{{Filename: "a.txt", ContentType: "text/plain", Content: []byte("hello")}})
	subject, from, text, _, mid, _, atts := parseMessage([]byte(raw))
	if subject != "Ek" || !strings.Contains(from, "a@b.c") || strings.TrimSpace(text) != "metin" || mid == "" {
		t.Fatalf("ayristirma: %q %q %q %q", subject, from, text, mid)
	}
	if len(atts) != 1 || atts[0].Filename != "a.txt" || string(atts[0].Content) != "hello" {
		t.Fatalf("ek: %+v", atts)
	}
}
