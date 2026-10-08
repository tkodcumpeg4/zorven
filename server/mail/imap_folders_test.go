package mail

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func imapCode(err error) imap.ResponseCode {
	if ie, ok := err.(*imap.Error); ok {
		return ie.Code
	}
	return ""
}

func listAll(t *testing.T, c *imapclient.Client, pattern string, opts *imap.ListOptions) map[string]*imap.ListData {
	t.Helper()
	if opts == nil {
		opts = &imap.ListOptions{}
	}
	boxes, err := c.List("", pattern, opts).Collect()
	if err != nil {
		t.Fatalf("list %q: %v", pattern, err)
	}
	out := map[string]*imap.ListData{}
	for _, b := range boxes {
		out[b.Mailbox] = b
	}
	return out
}

func hasAttr(d *imap.ListData, a imap.MailboxAttr) bool {
	if d == nil {
		return false
	}
	for _, x := range d.Attrs {
		if x == a {
			return true
		}
	}
	return false
}

func appendTo(t *testing.T, c *imapclient.Client, box, subject, mid string) *imap.AppendData {
	t.Helper()
	raw := "From: ali@example.com\r\nTo: " + testBox + "\r\nSubject: " + subject +
		"\r\nMessage-ID: " + mid + "\r\nDate: Mon, 02 Jan 2006 15:04:05 +0000\r\n\r\ngovde\r\n"
	cmd := c.Append(box, int64(len(raw)), nil)
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

func TestIMAP_FolderCreateListNested(t *testing.T) {
	e := newIMAPEnv(t)
	c := e.login(t)

	if err := c.Create("Projeler/2026/Q1", nil).Wait(); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Ust klasorler otomatik olusur.
	all := listAll(t, c, "*", nil)
	for _, n := range []string{"Projeler", "Projeler/2026", "Projeler/2026/Q1"} {
		if all[n] == nil {
			t.Fatalf("%s listede yok: %v", n, all)
		}
	}
	if !hasAttr(all["Projeler"], imap.MailboxAttrHasChildren) || !hasAttr(all["Projeler/2026"], imap.MailboxAttrHasChildren) {
		t.Fatalf("HasChildren eksik: %+v %+v", all["Projeler"], all["Projeler/2026"])
	}
	if !hasAttr(all["Projeler/2026/Q1"], imap.MailboxAttrHasNoChildren) || !hasAttr(all["INBOX"], imap.MailboxAttrHasNoChildren) {
		t.Fatal("HasNoChildren eksik")
	}
	// '%' yalniz bir seviye.
	top := listAll(t, c, "%", nil)
	if top["Projeler"] == nil || top["Projeler/2026"] != nil {
		t.Fatalf("%% bir seviyeden fazlasi dondu: %v", top)
	}
	sub := listAll(t, c, "Projeler/%", nil)
	if len(sub) != 1 || sub["Projeler/2026"] == nil {
		t.Fatalf("Projeler/%%: %v", sub)
	}

	// Var olan ve sistem adlari.
	for _, n := range []string{"Projeler", "projeler/2026", "INBOX", "inbox", "Sent", "Trash", "Drafts"} {
		err := c.Create(n, nil).Wait()
		if imapCode(err) != imap.ResponseCodeAlreadyExists {
			t.Fatalf("create %q: kod=%q err=%v", n, imapCode(err), err)
		}
	}
	// Sistem klasorunun altina olusturma yok.
	if err := c.Create("Sent/Alt", nil).Wait(); imapCode(err) != imap.ResponseCodeCannot {
		t.Fatalf("Sent/Alt: %v", err)
	}
	// Gecersiz adlar.
	for _, n := range []string{"a\x01b", "a//b", "../x", "x/./y"} {
		if err := c.Create(n, nil).Wait(); err == nil {
			t.Fatalf("gecersiz ad kabul edildi: %q", n)
		}
	}
	if err := c.Create(strings.Repeat("a", 201), nil).Wait(); err == nil {
		t.Fatal("201 karakterlik ad kabul edildi")
	}
	// Sondaki ayirac yalniz hiyerarsi bildirimi: klasor olusur.
	if err := c.Create("Arsiv/", nil).Wait(); err != nil {
		t.Fatalf("Arsiv/: %v", err)
	}
	if listAll(t, c, "Arsiv", nil)["Arsiv"] == nil {
		t.Fatal("Arsiv/ olusmadi")
	}
}

func TestIMAP_FolderSpecialUseCreate(t *testing.T) {
	e := newIMAPEnv(t)
	c := e.login(t)
	if !c.Caps().Has(imap.CapCreateSpecialUse) {
		t.Fatal("CREATE-SPECIAL-USE ilan edilmedi")
	}
	if err := c.Create("Arsivim", &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttrArchive}}).Wait(); err != nil {
		t.Fatalf("create use: %v", err)
	}
	all := listAll(t, c, "*", &imap.ListOptions{ReturnSpecialUse: true})
	if !hasAttr(all["Arsivim"], imap.MailboxAttrArchive) {
		t.Fatalf("\\Archive yok: %+v", all["Arsivim"])
	}
	sel := listAll(t, c, "*", &imap.ListOptions{SelectSpecialUse: true})
	if sel["Arsivim"] == nil || sel["Sent"] == nil || sel["Notlar"] != nil {
		t.Fatalf("special-use secimi: %v", sel)
	}
	// Desteklenmeyen nitelik.
	err := c.Create("X", &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttrSent}}).Wait()
	if imapCode(err) != imap.ResponseCode("USEATTR") {
		t.Fatalf("USEATTR bekleniyordu: %v", err)
	}
}

func TestIMAP_FolderAppendSelectCopyMoveUIDs(t *testing.T) {
	e := newIMAPEnv(t)
	m1 := e.seed("bir", "<one@x>", "g1")
	e.seed("iki", "<two@x>", "g2")
	_ = m1
	c := e.login(t)

	// Olmayan klasore APPEND/COPY/MOVE: TRYCREATE.
	raw := "Subject: x\r\n\r\ny\r\n"
	cmd := c.Append("Yok", int64(len(raw)), nil)
	_, _ = cmd.Write([]byte(raw))
	_ = cmd.Close()
	if _, err := cmd.Wait(); imapCode(err) != imap.ResponseCodeTryCreate {
		t.Fatalf("TRYCREATE bekleniyordu: %v", err)
	}
	if _, err := c.Select("Yok", nil).Wait(); imapCode(err) != imap.ResponseCodeNonExistent {
		t.Fatalf("select yok: %v", err)
	}

	for _, n := range []string{"A", "B"} {
		if err := c.Create(n, nil).Wait(); err != nil {
			t.Fatal(err)
		}
	}
	// Klasor basina bagimsiz UID dizisi ve UIDVALIDITY.
	a1 := appendTo(t, c, "A", "a1", "<a1@x>")
	a2 := appendTo(t, c, "A", "a2", "<a2@x>")
	b1 := appendTo(t, c, "B", "b1", "<b1@x>")
	if a1.UID != 1 || a2.UID != 2 || b1.UID != 1 {
		t.Fatalf("UID dizisi: a1=%d a2=%d b1=%d", a1.UID, a2.UID, b1.UID)
	}
	if a1.UIDValidity == 0 || a1.UIDValidity != a2.UIDValidity {
		t.Fatalf("UIDVALIDITY: %d %d", a1.UIDValidity, a2.UIDValidity)
	}

	sd, err := c.Select("A", nil).Wait()
	if err != nil || sd.NumMessages != 2 || sd.UIDNext != 3 || sd.UIDValidity != a1.UIDValidity {
		t.Fatalf("select A: %+v %v", sd, err)
	}
	st, err := c.Status("B", &imap.StatusOptions{NumMessages: true, UIDNext: true, UIDValidity: true}).Wait()
	if err != nil || *st.NumMessages != 1 || st.UIDNext != 2 {
		t.Fatalf("status B: %+v %v", st, err)
	}

	// COPY A -> B: hedefte yeni UID.
	cp, err := c.Copy(imap.UIDSetNum(1), "B").Wait()
	if err != nil || len(cp.DestUIDs) == 0 {
		t.Fatalf("copy: %+v %v", cp, err)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailUserFolderKey("A"))); n != 2 {
		t.Fatalf("COPY kaynaktan sildi: A=%d", n)
	}
	if got := e.st.all(testTenant, testBox, store.MailUserFolderKey("B")); len(got) != 2 || got[1].UID != 2 {
		t.Fatalf("B: %+v", got)
	}

	// MOVE A -> B ve INBOX -> A.
	mv, err := c.Move(imap.UIDSetNum(2), "B").Wait()
	if err != nil || mv.UIDValidity == 0 {
		t.Fatalf("move: %+v %v", mv, err)
	}
	if got := e.st.all(testTenant, testBox, store.MailUserFolderKey("B")); len(got) != 3 || got[2].UID != 3 {
		t.Fatalf("B sonrasi: %+v", got)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Move(imap.UIDSetNum(1), "A").Wait(); err != nil {
		t.Fatalf("inbox->A: %v", err)
	}
	if got := e.st.all(testTenant, testBox, store.MailUserFolderKey("A")); len(got) != 2 || got[1].UID != 3 {
		t.Fatalf("A (UID 3 bekleniyordu): %+v", got)
	}

	// Kullanici klasorunde \Deleted + EXPUNGE.
	if _, err := c.Select("B", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true,
		Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	got, err := c.Expunge().Collect()
	if err != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("expunge: %v %v", got, err)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailUserFolderKey("B"))); n != 2 {
		t.Fatalf("B'de %d mesaj", n)
	}
	// Baska kutu etkilenmez.
	if n := len(e.st.all(testTenant, "baska@mail.zorven.app", store.MailUserFolderKey("A"))); n != 0 {
		t.Fatal("baska kutuya sizdi")
	}
}

func TestIMAP_FolderRename(t *testing.T) {
	e := newIMAPEnv(t)
	c := e.login(t)
	if err := c.Create("Is/Musteri", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	d1 := appendTo(t, c, "Is/Musteri", "m1", "<m1@x>")
	appendTo(t, c, "Is/Musteri", "m2", "<m2@x>")
	if err := c.Create("Bos", nil).Wait(); err != nil {
		t.Fatal(err)
	}

	// Alt klasorleriyle birlikte yeniden adlandirma, mesaj ve UID'ler korunur.
	if err := c.Rename("Is", "Calisma/Eski", nil).Wait(); err != nil {
		t.Fatalf("rename: %v", err)
	}
	all := listAll(t, c, "*", nil)
	for _, n := range []string{"Calisma", "Calisma/Eski", "Calisma/Eski/Musteri"} {
		if all[n] == nil {
			t.Fatalf("%s yok: %v", n, all)
		}
	}
	if all["Is"] != nil || all["Is/Musteri"] != nil {
		t.Fatalf("eski adlar kaldi: %v", all)
	}
	sd, err := c.Select("Calisma/Eski/Musteri", nil).Wait()
	if err != nil || sd.NumMessages != 2 || sd.UIDNext != 3 || sd.UIDValidity != d1.UIDValidity {
		t.Fatalf("select: %+v %v", sd, err)
	}
	// Eski adla yeniden olusturulan klasor farkli UIDVALIDITY alir.
	if err := c.Create("Is/Musteri", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	st, err := c.Status("Is/Musteri", &imap.StatusOptions{UIDValidity: true, UIDNext: true}).Wait()
	if err != nil || st.UIDValidity == d1.UIDValidity || st.UIDNext != 1 {
		t.Fatalf("yeniden olusturulan: %+v %v", st, err)
	}

	// Hatalar.
	cases := []struct {
		from, to string
		code     imap.ResponseCode
	}{
		{"INBOX", "Baska", imap.ResponseCodeCannot},
		{"Sent", "Baska", imap.ResponseCodeCannot},
		{"Bos", "Trash", imap.ResponseCodeAlreadyExists},
		{"Bos", "Calisma", imap.ResponseCodeAlreadyExists},
		{"Bos", "Sent/Alt", imap.ResponseCodeCannot},
		{"Yok", "Yeni", imap.ResponseCodeNonExistent},
		{"Calisma", "Calisma/Alt", imap.ResponseCodeCannot},
	}
	for _, tc := range cases {
		err := c.Rename(tc.from, tc.to, nil).Wait()
		if err == nil {
			t.Fatalf("rename %s -> %s basarili oldu", tc.from, tc.to)
		}
		if imapCode(err) != tc.code {
			t.Fatalf("rename %s -> %s: kod=%q, beklenen %q (%v)", tc.from, tc.to, imapCode(err), tc.code, err)
		}
	}
}

func TestIMAP_FolderDelete(t *testing.T) {
	e := newIMAPEnv(t)
	c := e.login(t)
	if err := c.Create("Ust/Alt", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	appendTo(t, c, "Ust/Alt", "x", "<x@x>")

	if err := c.Delete("Ust").Wait(); imapCode(err) != imap.ResponseCodeHasChildren {
		t.Fatalf("HASCHILDREN bekleniyordu: %v", err)
	}
	for _, n := range []string{"INBOX", "Sent", "Trash", "Drafts"} {
		if err := c.Delete(n).Wait(); err == nil {
			t.Fatalf("sistem klasoru silindi: %s", n)
		}
	}
	if err := c.Delete("Yok").Wait(); imapCode(err) != imap.ResponseCodeNonExistent {
		t.Fatalf("yok klasor: %v", err)
	}
	if _, err := c.Select("Ust/Alt", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("Ust/Alt").Wait(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := len(e.st.all(testTenant, testBox, store.MailUserFolderKey("Ust/Alt"))); n != 0 {
		t.Fatalf("mesajlar silinmedi: %d", n)
	}
	if err := c.Delete("Ust").Wait(); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	all := listAll(t, c, "*", nil)
	if all["Ust"] != nil || all["Ust/Alt"] != nil || len(all) != 4 {
		t.Fatalf("liste: %v", all)
	}
}

func TestIMAP_FolderSubscribe(t *testing.T) {
	e := newIMAPEnv(t)
	c := e.login(t)
	if err := c.Create("Haber", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	subs := func() map[string]*imap.ListData {
		return listAll(t, c, "*", &imap.ListOptions{SelectSubscribed: true})
	}
	if subs()["Haber"] == nil {
		t.Fatal("yeni klasor abone degil")
	}
	if err := c.Unsubscribe("Haber").Wait(); err != nil {
		t.Fatal(err)
	}
	s := subs()
	if s["Haber"] != nil || s["INBOX"] == nil {
		t.Fatalf("abonelik: %v", s)
	}
	if listAll(t, c, "Haber", nil)["Haber"] == nil {
		t.Fatal("LIST abonelikten bagimsiz olmali")
	}
	if err := c.Subscribe("Haber").Wait(); err != nil {
		t.Fatal(err)
	}
	if subs()["Haber"] == nil {
		t.Fatal("yeniden abone olunamadi")
	}
	if !hasAttr(subs()["Haber"], imap.MailboxAttrSubscribed) {
		t.Fatal("\\Subscribed yok")
	}
	if err := c.Subscribe("Yok").Wait(); imapCode(err) != imap.ResponseCodeNonExistent {
		t.Fatalf("yok klasor aboneligi: %v", err)
	}
	// Sistem klasorleri aboneligi kaldirilamaz (zararsiz no-op).
	if err := c.Unsubscribe("INBOX").Wait(); err != nil {
		t.Fatal(err)
	}
	if subs()["INBOX"] == nil {
		t.Fatal("INBOX aboneligi kalkti")
	}
}
