package pgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func mailTestTenant(t *testing.T, s *Store, id, slug string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO tenants (id, slug, created_at) VALUES ($1,$2,now()) ON CONFLICT DO NOTHING`, id, slug); err != nil {
		t.Fatalf("kiraci eklenemedi: %v", err)
	}
}

func TestMailUID_BackfillOrdersByReceivedAt(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	if _, err := s.pool.Exec(ctx, `TRUNCATE mail_messages, mail_uid_state CASCADE`); err != nil {
		t.Fatal(err)
	}
	// Eski (0059 oncesi) satirlari taklit et: folder='' uid=0.
	if _, err := s.pool.Exec(ctx, `DROP INDEX IF EXISTS ux_mail_mailbox_folder_uid`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ins := func(id, dir, from, to string, age time.Duration) {
		t.Helper()
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO mail_messages (id, tenant_id, direction, from_addr, to_addr, received_at, folder, mailbox, uid)
			 VALUES ($1,'ten_a',$2,$3,$4,$5,'','',0)`, id, dir, from, to, now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	ins("m3", "inbound", "x@y.z", "Acme@Mail.Zorven.app", time.Minute)
	ins("m1", "inbound", "x@y.z", "acme@mail.zorven.app", 3*time.Hour)
	ins("m2", "inbound", "x@y.z", "acme@mail.zorven.app", 2*time.Hour)
	ins("s1", "outbound", "acme@mail.zorven.app", "p@q.r", time.Hour)
	if _, err := s.pool.Exec(ctx, `DELETE FROM mail_uid_state`); err != nil {
		t.Fatal(err)
	}

	sqlText, err := migrationFS.ReadFile("migrations/0060_mail_imap.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, string(sqlText)); err != nil {
		t.Fatalf("0059 yeniden uygulanamadi: %v", err)
	}

	idx, err := s.ListMailIndex(ctx, "ten_a", "acme@mail.zorven.app", store.MailFolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 3 || idx[0].ID != "m1" || idx[1].ID != "m2" || idx[2].ID != "m3" {
		t.Fatalf("beklenen sira m1,m2,m3: %+v", idx)
	}
	for i, e := range idx {
		if e.UID != uint32(i+1) {
			t.Fatalf("uid[%d]=%d, beklenen %d", i, e.UID, i+1)
		}
	}
	sent, _ := s.ListMailIndex(ctx, "ten_a", "acme@mail.zorven.app", store.MailFolderSent)
	if len(sent) != 1 || sent[0].UID != 1 {
		t.Fatalf("sent dizini beklenmedik: %+v", sent)
	}

	// Yeni mesaj kaldigi yerden devam eder (UID 4) ve UIDVALIDITY sabittir.
	st1, err := s.MailFolderState(ctx, "ten_a", "acme@mail.zorven.app", store.MailFolderInbox)
	if err != nil {
		t.Fatal(err)
	}
	if st1.UIDNext != 4 || st1.UIDValidity == 0 {
		t.Fatalf("durum beklenmedik: %+v", st1)
	}
	saved, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
		From: "a@b.c", To: "ACME@mail.zorven.app", Subject: "yeni", Raw: "Subject: yeni\r\n\r\nx"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.UID != 4 || saved.Mailbox != "acme@mail.zorven.app" || saved.Folder != store.MailFolderInbox {
		t.Fatalf("kaydedilen beklenmedik: %+v", saved)
	}
	st2, _ := s.MailFolderState(ctx, "ten_a", "acme@mail.zorven.app", store.MailFolderInbox)
	if st2.UIDValidity != st1.UIDValidity || st2.UIDNext != 5 {
		t.Fatalf("durum2 beklenmedik: %+v (onceki %+v)", st2, st1)
	}
}

func TestMailUID_ConcurrentInsertsAreUnique(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	const n = 12
	var wg sync.WaitGroup
	uids := make(chan uint32, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
				From: "a@b.c", To: "acme@mail.zorven.app", Raw: "Subject: s\r\n\r\nb"})
			if err != nil {
				t.Error(err)
				return
			}
			uids <- m.UID
		}()
	}
	wg.Wait()
	close(uids)
	seen := map[uint32]bool{}
	for u := range uids {
		if seen[u] {
			t.Fatalf("tekrar eden uid %d", u)
		}
		seen[u] = true
	}
	if len(seen) != n {
		t.Fatalf("%d uid bekleniyordu, %d geldi", n, len(seen))
	}
}

func TestMailFlagsMoveExpunge(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	box := "acme@mail.zorven.app"
	mk := func(mid string) store.MailMessage {
		m, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
			From: "a@b.c", To: box, MessageID: mid, Raw: "Subject: s\r\n\r\nb"})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b, c := mk("<a@x>"), mk("<b@x>"), mk("<c@x>")

	yes := true
	if err := s.SetMailFlags(ctx, "ten_a", a.ID, store.MailFlagUpdate{Seen: &yes, Flagged: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMailFlags(ctx, "ten_a", b.ID, store.MailFlagUpdate{Deleted: &yes}); err != nil {
		t.Fatal(err)
	}
	// Panelin okundu sutunu ile ayni.
	pm, _ := s.GetMailMessage(ctx, "ten_a", a.ID)
	if !pm.Seen {
		t.Fatal("IMAP \\Seen paneldeki seen sutununa yansimadi")
	}
	if n, _ := s.CountUnseenMail(ctx, "ten_a"); n != 2 {
		t.Fatalf("okunmamis=%d, beklenen 2", n)
	}

	// MOVE: Trash'te yeni UID, panel listesinden kaybolur.
	uid, err := s.MoveMailMessage(ctx, "ten_a", c.ID, store.MailFolderTrash)
	if err != nil || uid != 1 {
		t.Fatalf("move uid=%d err=%v", uid, err)
	}
	panel, _ := s.ListMailMessages(ctx, "ten_a", "inbound", 50)
	for _, m := range panel {
		if m.ID == c.ID {
			t.Fatal("Trash'e tasinan mesaj panel gelen kutusunda gorunuyor")
		}
	}
	if n, _ := s.CountUnseenMail(ctx, "ten_a"); n != 1 {
		t.Fatalf("okunmamis=%d, beklenen 1 (c Trash'te)", n)
	}

	// EXPUNGE yalniz \Deleted olanlari siler.
	gone, err := s.ExpungeMail(ctx, "ten_a", box, store.MailFolderInbox, nil)
	if err != nil || len(gone) != 1 || gone[0] != b.UID {
		t.Fatalf("expunge=%v err=%v", gone, err)
	}
	idx, _ := s.ListMailIndex(ctx, "ten_a", box, store.MailFolderInbox)
	if len(idx) != 1 || idx[0].ID != a.ID {
		t.Fatalf("expunge sonrasi dizin: %+v", idx)
	}
	// Silinen UID tekrar kullanilmaz.
	d := mk("<d@x>")
	if d.UID != 4 {
		t.Fatalf("yeni uid=%d, beklenen 4", d.UID)
	}

	// Message-ID ile arama (APPEND tekillestirme).
	e, found, err := s.FindMailByMessageID(ctx, "ten_a", box, store.MailFolderInbox, "<a@x>")
	if err != nil || !found || e.ID != a.ID {
		t.Fatalf("find: %+v %v %v", e, found, err)
	}
	if _, found, _ := s.FindMailByMessageID(ctx, "ten_a", box, store.MailFolderSent, "<a@x>"); found {
		t.Fatal("baska klasorde bulunmamali")
	}
	// Baska kiraci goremez.
	mailTestTenant(t, s, "ten_b", "other")
	if err := s.SetMailFlags(ctx, "ten_b", a.ID, store.MailFlagUpdate{Seen: &yes}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("baska kiraci icin ErrNotFound bekleniyordu: %v", err)
	}
}

func TestMailAppPasswords(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	p, err := s.CreateMailAppPassword(ctx, store.MailAppPassword{TenantID: "ten_a",
		Mailbox: "Acme@Mail.Zorven.app", Label: "telefon", PasswordHash: "argon2id$x$y"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Mailbox != "acme@mail.zorven.app" || p.RevokedAt != nil || p.LastUsedAt != nil {
		t.Fatalf("beklenmedik kayit: %+v", p)
	}
	active, _ := s.ListActiveMailAppPasswords(ctx, "ACME@mail.zorven.app")
	if len(active) != 1 || active[0].PasswordHash != "argon2id$x$y" {
		t.Fatalf("aktif liste: %+v", active)
	}
	if err := s.TouchMailAppPassword(ctx, p.ID, "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListMailAppPasswords(ctx, "ten_a", "")
	if len(list) != 1 || list[0].LastUsedIP != "1.2.3.4" || list[0].LastUsedAt == nil {
		t.Fatalf("liste: %+v", list)
	}
	// Baska kiraci iptal edemez.
	mailTestTenant(t, s, "ten_b", "other")
	if err := s.RevokeMailAppPassword(ctx, "ten_b", p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("baska kiraci: %v", err)
	}
	if err := s.RevokeMailAppPassword(ctx, "ten_a", p.ID); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.ListActiveMailAppPasswords(ctx, "acme@mail.zorven.app"); len(active) != 0 {
		t.Fatalf("iptal sonrasi aktif olmamali: %+v", active)
	}
	list, _ = s.ListMailAppPasswords(ctx, "ten_a", "acme@mail.zorven.app")
	if len(list) != 1 || list[0].RevokedAt == nil {
		t.Fatalf("iptal edilmis kayit listede kalmali: %+v", list)
	}
	// Aktif sinir.
	for i := 0; i < maxMailAppPasswords; i++ {
		if _, err := s.CreateMailAppPassword(ctx, store.MailAppPassword{TenantID: "ten_a",
			Mailbox: "acme@mail.zorven.app", PasswordHash: "h"}); err != nil {
			t.Fatalf("%d. parola: %v", i, err)
		}
	}
	if _, err := s.CreateMailAppPassword(ctx, store.MailAppPassword{TenantID: "ten_a",
		Mailbox: "acme@mail.zorven.app", PasswordHash: "h"}); !errors.Is(err, store.ErrMailAppPasswordLimit) {
		t.Fatalf("sinir hatasi bekleniyordu: %v", err)
	}
}
