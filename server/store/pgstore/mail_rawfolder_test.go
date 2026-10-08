package pgstore

import (
	"bytes"
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// 8-bit (ISO-8859-9) + NUL iceren ham mesaj raw_bytes'ta birebir saklanir ve
// RFC822.SIZE'a esas boyut (ListMailIndex) bayt sayisiyla eslesir.
func TestMailRawBytes_RoundTripWithNUL(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	box := "acme@mail.zorven.app"
	raw := []byte("Subject: s\r\nContent-Type: text/plain; charset=iso-8859-9\r\n\r\n\xfd\xfe\xf0\x00\xff son\r\n")
	m, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
		From: "a@b.c", To: box, RawBytes: raw})
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.GetMailMessageFull(ctx, "ten_a", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(full.RawData(), raw) {
		t.Fatalf("ham baytlar degisti: %q", full.RawData())
	}
	idx, err := s.ListMailIndex(ctx, "ten_a", box, store.MailFolderInbox)
	if err != nil || len(idx) != 1 || idx[0].Size != int64(len(raw)) {
		t.Fatalf("dizin boyutu: %+v %v (beklenen %d)", idx, err, len(raw))
	}
	// Eski TEXT Raw verisi hala okunur (geri uyum).
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO mail_messages (id, tenant_id, direction, from_addr, to_addr, folder, mailbox, uid, raw)
		 VALUES ('legacy','ten_a','inbound','a@b.c',$1,'inbox',$1,99,'Subject: eski')`, box); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetMailMessageFull(ctx, "ten_a", "legacy")
	if err != nil || string(old.RawData()) != "Subject: eski" {
		t.Fatalf("eski raw: %q %v", old.RawData(), err)
	}
}

// IMAP ile Sent'e tasinan mesaj panelde Sent listesinde (direction'a bakmadan) cikar.
func TestListMailMessages_ByFolder(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	box := "acme@mail.zorven.app"
	in, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
		From: "a@b.c", To: box, Subject: "gelen", Raw: "Subject: gelen\r\n\r\nx"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
		From: "a@b.c", To: box, Subject: "kalan", Raw: "Subject: kalan\r\n\r\nx"}); err != nil {
		t.Fatal(err)
	}
	subjects := func(folder string) []string {
		t.Helper()
		l, err := s.ListMailMessages(ctx, "ten_a", folder, 50)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range l {
			out = append(out, m.Subject)
		}
		return out
	}
	if _, err := s.MoveMailMessage(ctx, "ten_a", in.ID, store.MailFolderSent); err != nil {
		t.Fatal(err)
	}
	if got := subjects(store.MailFolderSent); len(got) != 1 || got[0] != "gelen" {
		t.Fatalf("Sent listesi: %v", got)
	}
	if got := subjects(store.MailFolderInbox); len(got) != 1 || got[0] != "kalan" {
		t.Fatalf("INBOX listesi: %v", got)
	}
	if _, err := s.MoveMailMessage(ctx, "ten_a", in.ID, store.MailFolderTrash); err != nil {
		t.Fatal(err)
	}
	if got := subjects(store.MailFolderTrash); len(got) != 1 {
		t.Fatalf("Trash listesi: %v", got)
	}
	if got := subjects(store.MailFolderSent); len(got) != 0 {
		t.Fatalf("Sent bos olmali: %v", got)
	}
}
