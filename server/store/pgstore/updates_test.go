package pgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestUpdatePostsLifecycle(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE updates_posts, updates_email_deliveries, announcement_prefs CASCADE`); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateUpdatePost(ctx, store.UpdatePost{Slug: "v1", Kind: "release", TitleTR: "Bir", BodyTR: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUpdatePost(ctx, store.UpdatePost{Slug: "V1", Kind: "update", TitleTR: "Cakisma"}); !errors.Is(err, store.ErrUpdateSlugTaken) {
		t.Fatalf("slug cakismasi: %v", err)
	}
	// taslak kamuya gorunmez
	if list, _, _ := s.ListUpdatePosts(ctx, true, 10, 0); len(list) != 0 {
		t.Fatal("taslak kamuya sizdi")
	}
	if _, err := s.GetUpdatePostBySlug(ctx, "v1", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("taslak slug ile bulundu: %v", err)
	}
	// yayinlanmamis yazi e-postalanamaz
	if ok, _ := s.ClaimUpdatePostEmail(ctx, p.ID, false, 1); ok {
		t.Fatal("taslak sahiplenildi")
	}
	if _, err := s.SetUpdatePostPublished(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUpdatePostBySlug(ctx, "V1", true); err != nil {
		t.Fatalf("yayinlanmis bulunamadi: %v", err)
	}
	if ok, _ := s.ClaimUpdatePostEmail(ctx, p.ID, false, 5); !ok {
		t.Fatal("ilk sahiplenme basarisiz")
	}
	if ok, _ := s.ClaimUpdatePostEmail(ctx, p.ID, false, 5); ok {
		t.Fatal("ikinci sahiplenme (idempotans) gecti")
	}
	if ok, _ := s.ClaimUpdatePostEmail(ctx, p.ID, true, 5); !ok {
		t.Fatal("force sahiplenme gecmedi")
	}
	if err := s.DeleteUpdatePost(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAnnouncementRecipients(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE announcement_prefs`); err != nil {
		t.Fatal(err)
	}
	ins := func(id, email string, verified bool) {
		t.Helper()
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO "user" ("id","name","email","emailVerified") VALUES ($1,$1,$2,$3)
			 ON CONFLICT ("id") DO UPDATE SET "emailVerified" = EXCLUDED."emailVerified"`, id, email, verified); err != nil {
			t.Fatal(err)
		}
	}
	ins("ann_ok", "ann_ok@example.com", true)
	ins("ann_unverified", "ann_unv@example.com", false)
	ins("ann_unsub", "ann_unsub@example.com", true)
	ins("ann_norow", "ann_norow@example.com", true)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), `DELETE FROM "user" WHERE "id" LIKE 'ann_%'`)
	})
	// OPT-IN: yalniz acik riza verenler alici olur.
	for _, id := range []string{"ann_ok", "ann_unverified"} {
		if err := s.SetAnnouncementPref(ctx, id, true, "tr", "signup"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetAnnouncementPref(ctx, "ann_unsub", true, "en", "profile"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnnouncementPref(ctx, "ann_unsub", false, "", "unsub"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListAnnouncementRecipients(ctx, "ann_", 100)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.UserID] = true
	}
	if !ids["ann_ok"] || ids["ann_unverified"] || ids["ann_unsub"] || ids["ann_norow"] {
		t.Fatalf("alici suzmesi hatali: %v", ids)
	}
	// Satirsiz kullanici abone DEGIL ve "sorulmadi".
	if sub, _ := s.IsAnnouncementSubscribed(ctx, "ann_norow"); sub {
		t.Fatal("satirsiz kullanici abone sayildi (opt-in ihlali)")
	}
	if pref, _ := s.GetAnnouncementPref(ctx, "ann_norow"); pref.Subscribed || pref.Asked {
		t.Fatalf("satirsiz tercih: %+v", pref)
	}
	if pref, _ := s.GetAnnouncementPref(ctx, "ann_unsub"); pref.Subscribed || !pref.Asked || pref.Lang != "en" {
		t.Fatalf("reddeden tercih: %+v", pref)
	}
	if err := s.SetAnnouncementPref(ctx, "ann_unsub", true, "", "banner"); err != nil {
		t.Fatal(err)
	}
	if pref, _ := s.GetAnnouncementPref(ctx, "ann_unsub"); !pref.Subscribed || pref.Lang != "en" || pref.Source != "banner" {
		t.Fatalf("tercih: %+v", pref)
	}
}

func TestUpdateDeliveryLogAndResume(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `TRUNCATE updates_posts CASCADE`); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateUpdatePost(ctx, store.UpdatePost{Slug: "dl", Kind: "update", TitleTR: "Gunluk"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetUpdatePostPublished(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.ClaimUpdatePostEmail(ctx, p.ID, false, 3); !ok {
		t.Fatal("sahiplenilemedi")
	}
	_ = s.RecordUpdateDelivery(ctx, p.ID, "u1", store.DeliverySent, "")
	_ = s.RecordUpdateDelivery(ctx, p.ID, "u2", store.DeliveryFailed, "smtp 450")
	_ = s.RecordUpdateDelivery(ctx, p.ID, "u2", store.DeliveryFailed, "smtp 450")
	m, _ := s.UpdateDeliveryStatuses(ctx, p.ID, []string{"u1", "u2", "u3"})
	if m["u1"] != "sent" || m["u2"] != "failed" || len(m) != 2 {
		t.Fatalf("durumlar: %v", m)
	}
	if sent, failed, _ := s.CountUpdateDeliveries(ctx, p.ID); sent != 1 || failed != 1 {
		t.Fatalf("sayac: %d %d", sent, failed)
	}
	// Yeniden baslatma: bitmemis gonderim interrupted olur, devam sahiplenilir.
	if n, err := s.MarkInterruptedUpdateSends(ctx); err != nil || n != 1 {
		t.Fatalf("interrupted: %d %v", n, err)
	}
	got, _ := s.GetUpdatePost(ctx, p.ID)
	if !got.EmailStats.Interrupted || got.EmailStats.Done {
		t.Fatalf("stats: %+v", got.EmailStats)
	}
	if ok, _ := s.ClaimUpdatePostResume(ctx, p.ID, store.SendModeRetryFailed, 3); ok {
		t.Fatal("bitmemis gonderimde retry_failed kabul edildi")
	}
	if ok, _ := s.ClaimUpdatePostResume(ctx, p.ID, store.SendModeResume, 3); !ok {
		t.Fatal("resume sahiplenilemedi")
	}
	got, _ = s.GetUpdatePost(ctx, p.ID)
	if got.EmailStats.Interrupted {
		t.Fatal("resume sonrasi interrupted kaldi")
	}
	_ = s.SetUpdatePostEmailStats(ctx, p.ID, store.UpdateEmailStats{Total: 3, Sent: 2, Failed: 1, Done: true})
	if ok, _ := s.ClaimUpdatePostResume(ctx, p.ID, store.SendModeResume, 3); ok {
		t.Fatal("biten gonderim resume edildi")
	}
	if ok, _ := s.ClaimUpdatePostResume(ctx, p.ID, store.SendModeRetryFailed, 3); !ok {
		t.Fatal("retry_failed sahiplenilemedi")
	}
	if err := s.ResetUpdateDeliveries(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if sent, failed, _ := s.CountUpdateDeliveries(ctx, p.ID); sent+failed != 0 {
		t.Fatal("gunluk sifirlanmadi")
	}
}
