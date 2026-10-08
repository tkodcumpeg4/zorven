package pgstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

const fBox = "acme@mail.zorven.app"

func folderNames(t *testing.T, s *Store, box string) []string {
	t.Helper()
	l, err := s.ListMailFolders(context.Background(), "ten_a", box)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range l {
		out = append(out, f.Name)
	}
	return out
}

func TestMailFolders_CreateNestedAndRules(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")

	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "A/B/C", `\Archive`); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderNames(t, s, fBox), ","); got != "A,A/B,A/B/C" {
		t.Fatalf("ust klasorler olusmadi: %s", got)
	}
	l, _ := s.ListMailFolders(ctx, "ten_a", fBox)
	if l[2].SpecialUse != `\Archive` || !l[2].Subscribed || l[0].SpecialUse != "" {
		t.Fatalf("nitelikler: %+v", l)
	}
	// Var olan (buyuk/kucuk harf duyarsiz) ve gecersiz adlar.
	for _, n := range []string{"A", "a/b", "A/B/C"} {
		if err := s.CreateMailFolder(ctx, "ten_a", fBox, n, ""); !errors.Is(err, store.ErrMailFolderExists) {
			t.Fatalf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "x\x00y", "x\ty", "a*", "a%", "a//b", "/a", "a/", "INBOX", "sent/x", strings.Repeat("a", 201)} {
		if err := s.CreateMailFolder(ctx, "ten_a", fBox, n, ""); !errors.Is(err, store.ErrMailFolderInvalid) {
			t.Fatalf("%q: gecersiz bekleniyordu, %v", n, err)
		}
	}
	// Kutular birbirinden ayridir.
	if err := s.CreateMailFolder(ctx, "ten_a", "baska@mail.zorven.app", "A", ""); err != nil {
		t.Fatal(err)
	}
	names, _ := s.ListTenantMailFolderNames(ctx, "ten_a")
	if strings.Join(names, ",") != "A,A/B,A/B/C" {
		t.Fatalf("kiraci adlari: %v", names)
	}
}

func TestMailFolders_Limit(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	for i := 0; i < store.MaxMailFoldersPerMailbox; i++ {
		if err := s.CreateMailFolder(ctx, "ten_a", fBox, "f"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+itoa(i), ""); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "fazla", ""); !errors.Is(err, store.ErrMailFolderLimit) {
		t.Fatalf("sinir: %v", err)
	}
	// Ust klasorler de sayilir: tek yeni klasor + yeni ust = 2 satir.
	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "yeni/alt", ""); !errors.Is(err, store.ErrMailFolderLimit) {
		t.Fatalf("sinir (ust): %v", err)
	}
	// Baska kutunun siniri etkilenmez.
	if err := s.CreateMailFolder(ctx, "ten_a", "baska@mail.zorven.app", "fazla", ""); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestMailFolders_MessagesUIDAndMove(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "Is", ""); err != nil {
		t.Fatal(err)
	}
	key := store.MailUserFolderKey("Is")
	mk := func(mid, folder string) store.MailMessage {
		m, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
			From: "a@b.c", To: fBox, MessageID: mid, Folder: folder, Raw: "Subject: s\r\n\r\nb"})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a := mk("<1@x>", key)
	b := mk("<2@x>", key)
	inbox := mk("<3@x>", "")
	if a.UID != 1 || b.UID != 2 || inbox.UID != 1 {
		t.Fatalf("UID dizisi klasor basina bagimsiz olmali: %d %d %d", a.UID, b.UID, inbox.UID)
	}
	if _, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
		From: "a@b.c", To: fBox, Folder: "u:../x"}); err == nil {
		t.Fatal("gecersiz kullanici klasoru kabul edildi")
	}
	// Panel listesi klasore gore.
	list, err := s.ListMailMessages(ctx, "ten_a", key, 50)
	if err != nil || len(list) != 2 {
		t.Fatalf("liste: %v %v", list, err)
	}
	// INBOX -> kullanici klasoru: yeni UID 3.
	uid, err := s.MoveMailMessage(ctx, "ten_a", inbox.ID, key)
	if err != nil || uid != 3 {
		t.Fatalf("tasima: uid=%d err=%v", uid, err)
	}
	// Panelden tasima, hedef klasoru kutuda yoksa olusturur.
	other := mk("<4@x>", "")
	if _, err := s.MoveMailMessage(ctx, "ten_a", other.ID, store.MailUserFolderKey("Yeni/Alt")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderNames(t, s, fBox), ","); got != "Is,Yeni,Yeni/Alt" {
		t.Fatalf("tasima klasor olusturmadi: %s", got)
	}
	st, err := s.MailFolderState(ctx, "ten_a", fBox, key)
	if err != nil || st.UIDNext != 4 {
		t.Fatalf("state: %+v %v", st, err)
	}
}

func TestMailFolders_RenameKeepsMessagesAndUIDs(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	for _, n := range []string{"Is/Musteri", "Bos"} {
		if err := s.CreateMailFolder(ctx, "ten_a", fBox, n, ""); err != nil {
			t.Fatal(err)
		}
	}
	child := store.MailUserFolderKey("Is/Musteri")
	for i := 0; i < 2; i++ {
		if _, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
			From: "a@b.c", To: fBox, Folder: child, Raw: "Subject: s\r\n\r\nb"}); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.MailFolderState(ctx, "ten_a", fBox, child)

	if err := s.RenameMailFolder(ctx, "ten_a", fBox, "Is", "Calisma/Eski"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(folderNames(t, s, fBox), ","); got != "Bos,Calisma,Calisma/Eski,Calisma/Eski/Musteri" {
		t.Fatalf("adlar: %s", got)
	}
	newKey := store.MailUserFolderKey("Calisma/Eski/Musteri")
	idx, err := s.ListMailIndex(ctx, "ten_a", fBox, newKey)
	if err != nil || len(idx) != 2 || idx[0].UID != 1 || idx[1].UID != 2 {
		t.Fatalf("tasinan dizin: %+v %v", idx, err)
	}
	after, _ := s.MailFolderState(ctx, "ten_a", fBox, newKey)
	if after.UIDNext != 3 || after.UIDValidity != before.UIDValidity {
		t.Fatalf("durum: %+v onceki %+v", after, before)
	}
	if old, _ := s.ListMailIndex(ctx, "ten_a", fBox, child); len(old) != 0 {
		t.Fatalf("eski klasorde mesaj kaldi: %+v", old)
	}
	// Eski ad yeniden olusturulursa UID sayaci sifirlanir, UIDVALIDITY degisir.
	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "Is/Musteri", ""); err != nil {
		t.Fatal(err)
	}
	re, _ := s.MailFolderState(ctx, "ten_a", fBox, child)
	if re.UIDNext != 1 || re.UIDValidity == before.UIDValidity {
		t.Fatalf("yeniden olusturulan: %+v onceki %+v", re, before)
	}

	// Hatalar.
	for _, tc := range []struct {
		from, to string
		want     error
	}{
		{"Bos", "Calisma", store.ErrMailFolderExists},
		{"bos", "X", store.ErrNotFound},
		{"Bos", "Bos", store.ErrMailFolderInvalid},
		{"Calisma", "Calisma/Alt", store.ErrMailFolderInvalid},
		{"Bos", "Sent", store.ErrMailFolderInvalid},
		{"Yok", "Y", store.ErrNotFound},
	} {
		if err := s.RenameMailFolder(ctx, "ten_a", fBox, tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Fatalf("%s -> %s: %v, beklenen %v", tc.from, tc.to, err, tc.want)
		}
	}
	// Yalniz harf buyuklugu degisimi serbest.
	if err := s.RenameMailFolder(ctx, "ten_a", fBox, "Bos", "BOS"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
}

func TestMailFolders_DeleteSubscribe(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	mailTestTenant(t, s, "ten_a", "acme")
	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "Ust/Alt", ""); err != nil {
		t.Fatal(err)
	}
	key := store.MailUserFolderKey("Ust/Alt")
	m, err := s.InsertMailMessage(ctx, store.MailMessage{TenantID: "ten_a", Direction: "inbound",
		From: "a@b.c", To: fBox, Folder: key, Raw: "Subject: s\r\n\r\nb"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMailAttachment(ctx, store.MailAttachment{MessageID: m.ID, TenantID: "ten_a",
		Filename: "a.txt", ContentType: "text/plain", SizeBytes: 1, Content: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	v0, _ := s.MailFolderState(ctx, "ten_a", fBox, key)

	if err := s.SetMailFolderSubscribed(ctx, "ten_a", fBox, "Ust/Alt", false); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListMailFolders(ctx, "ten_a", fBox); l[1].Subscribed {
		t.Fatal("abonelik kaldirilmadi")
	}
	if err := s.SetMailFolderSubscribed(ctx, "ten_a", fBox, "Yok", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("yok: %v", err)
	}

	if err := s.DeleteMailFolder(ctx, "ten_a", fBox, "Ust"); !errors.Is(err, store.ErrMailFolderHasChildren) {
		t.Fatalf("alt klasorlu silme: %v", err)
	}
	if err := s.DeleteMailFolder(ctx, "ten_a", fBox, "Yok"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("yok silme: %v", err)
	}
	if err := s.DeleteMailFolder(ctx, "ten_a", fBox, "Ust/Alt"); err != nil {
		t.Fatal(err)
	}
	if idx, _ := s.ListMailIndex(ctx, "ten_a", fBox, key); len(idx) != 0 {
		t.Fatalf("mesajlar silinmedi: %+v", idx)
	}
	if atts, _ := s.ListMailAttachments(ctx, "ten_a", m.ID); len(atts) != 0 {
		t.Fatal("ekler silinmedi")
	}
	if err := s.CreateMailFolder(ctx, "ten_a", fBox, "Ust/Alt", ""); err != nil {
		t.Fatal(err)
	}
	v1, _ := s.MailFolderState(ctx, "ten_a", fBox, key)
	if v1.UIDValidity == v0.UIDValidity || v1.UIDNext != 1 {
		t.Fatalf("yeniden olusturulan klasor: %+v onceki %+v", v1, v0)
	}
	if err := s.DeleteMailFolder(ctx, "ten_a", fBox, "Ust/Alt"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMailFolder(ctx, "ten_a", fBox, "Ust"); err != nil {
		t.Fatal(err)
	}
	if got := folderNames(t, s, fBox); len(got) != 0 {
		t.Fatalf("kalan: %v", got)
	}
}
