package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// updatesFake: store.Store gomulu (nil) + UpdatesStore'un bellek ici hali.
type updatesFake struct {
	store.Store
	mu      sync.Mutex
	posts   map[string]store.UpdatePost
	emailed map[string]bool
	prefs   map[string]bool
	claimed []string // ClaimUpdatePostResume kipleri
	resets  int
	creates int
	updates int
}

func newUpdatesFake() *updatesFake {
	now := time.Now().Add(-time.Hour)
	return &updatesFake{
		posts: map[string]store.UpdatePost{
			"pub":   {ID: "pub", Slug: "yayinda", Kind: "release", TitleTR: "Yayinda", PublishedAt: &now},
			"draft": {ID: "draft", Slug: "taslak", Kind: "update", TitleTR: "Taslak"},
		},
		emailed: map[string]bool{}, prefs: map[string]bool{},
	}
}

func (f *updatesFake) ListUpdatePosts(_ context.Context, only bool, _, _ int) ([]store.UpdatePost, int, error) {
	out := []store.UpdatePost{}
	for _, p := range f.posts {
		if only && p.PublishedAt == nil {
			continue
		}
		out = append(out, p)
	}
	return out, len(out), nil
}
func (f *updatesFake) GetUpdatePost(_ context.Context, id string) (store.UpdatePost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.posts[id]; ok {
		if f.emailed[id] {
			now := time.Now()
			p.EmailedAt = &now
		}
		return p, nil
	}
	return store.UpdatePost{}, store.ErrNotFound
}
func (f *updatesFake) GetUpdatePostBySlug(_ context.Context, slug string, _ bool) (store.UpdatePost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.posts {
		if p.Slug == slug {
			return p, nil
		}
	}
	return store.UpdatePost{}, store.ErrNotFound
}
func (f *updatesFake) CreateUpdatePost(_ context.Context, p store.UpdatePost) (store.UpdatePost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	p.ID = "new"
	if _, taken := f.posts["new"]; taken {
		p.ID = "new" + strconv.Itoa(f.creates)
	}
	f.posts[p.ID] = p
	return p, nil
}
func (f *updatesFake) UpdateUpdatePost(_ context.Context, p store.UpdatePost) (store.UpdatePost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates++
	if old, ok := f.posts[p.ID]; ok {
		p.PublishedAt, p.CreatedBy = old.PublishedAt, old.CreatedBy
		f.posts[p.ID] = p
	}
	return p, nil
}
func (f *updatesFake) DeleteUpdatePost(context.Context, string) error { return nil }
func (f *updatesFake) SetUpdatePostPublished(_ context.Context, id string, pub bool) (store.UpdatePost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.posts[id]
	if pub && p.PublishedAt == nil {
		now := time.Now()
		p.PublishedAt = &now
	} else if !pub {
		p.PublishedAt = nil
	}
	f.posts[id] = p
	return p, nil
}
func (f *updatesFake) ClaimUpdatePostEmail(_ context.Context, id string, force bool, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.emailed[id] && !force {
		return false, nil
	}
	f.emailed[id] = true
	return true, nil
}
func (f *updatesFake) SetUpdatePostEmailStats(context.Context, string, store.UpdateEmailStats) error {
	return nil
}
func (f *updatesFake) ListAnnouncementRecipients(context.Context, string, int) ([]store.AnnouncementRecipient, error) {
	return nil, nil
}
func (f *updatesFake) CountAnnouncementRecipients(context.Context) (int, error) { return 3, nil }
func (f *updatesFake) GetAnnouncementPref(_ context.Context, id string) (store.AnnouncementPref, error) {
	sub, ok := f.prefs[id] // opt-in: satir yoksa abone degil, sorulmadi
	return store.AnnouncementPref{Subscribed: sub, Asked: ok}, nil
}
func (f *updatesFake) SetAnnouncementPref(_ context.Context, id string, sub bool, _, _ string) error {
	f.prefs[id] = sub
	return nil
}
func (f *updatesFake) IsAnnouncementSubscribed(_ context.Context, id string) (bool, error) {
	return f.prefs[id], nil
}
func (f *updatesFake) ClaimUpdatePostResume(_ context.Context, id, mode string, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimed = append(f.claimed, mode)
	return f.emailed[id], nil
}
func (f *updatesFake) MarkInterruptedUpdateSends(context.Context) (int, error) { return 0, nil }
func (f *updatesFake) RecordUpdateDelivery(context.Context, string, string, string, string) error {
	return nil
}
func (f *updatesFake) UpdateDeliveryStatuses(context.Context, string, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f *updatesFake) CountUpdateDeliveries(context.Context, string) (int, int, error) {
	return 0, 0, nil
}
func (f *updatesFake) ResetUpdateDeliveries(context.Context, string) error {
	f.mu.Lock()
	f.resets++
	f.mu.Unlock()
	return nil
}

func updatesCall(s *Server, h http.HandlerFunc, method, target, path, body string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if path != "" {
		req.SetPathValue("id", path)
	}
	ctx := req.Context()
	if admin {
		ctx = withPlatformAdmin(ctx)
	}
	ctx = withUser(ctx, &AuthUser{ID: "u1", Email: "u1@example.com", Role: "owner"})
	rec := httptest.NewRecorder()
	h(rec, req.WithContext(ctx))
	return rec
}

func TestUpdatesAdminEndpointsForbiddenForNonAdmin(t *testing.T) {
	s := &Server{Store: newUpdatesFake()}
	cases := []struct {
		name string
		h    http.HandlerFunc
		m    string
		id   string
		body string
	}{
		{"list", s.adminListUpdates, "GET", "", ""},
		{"create", s.adminCreateUpdate, "POST", "", `{"slug":"a","title_tr":"A"}`},
		{"get", s.adminGetUpdate, "GET", "pub", ""},
		{"update", s.adminUpdateUpdate, "PUT", "pub", `{"slug":"a","title_tr":"A"}`},
		{"delete", s.adminDeleteUpdate, "DELETE", "pub", ""},
		{"publish", s.adminPublishUpdate, "POST", "draft", ""},
		{"unpublish", s.adminUnpublishUpdate, "POST", "pub", ""},
		{"preview", s.adminPreviewUpdate, "POST", "", `{"markdown":"x"}`},
		{"subscribers", s.adminUpdateSubscribers, "GET", "", ""},
		{"test", s.adminTestUpdateEmail, "POST", "pub", ""},
		{"send", s.adminSendUpdate, "POST", "pub", ""},
	}
	for _, c := range cases {
		if rec := updatesCall(s, c.h, c.m, "/x", c.id, c.body, false); rec.Code != http.StatusForbidden {
			t.Errorf("%s: admin olmayan icin %d (403 bekleniyordu)", c.name, rec.Code)
		}
	}
}

func TestUpdatesPublicOnlyPublished(t *testing.T) {
	s := &Server{Store: newUpdatesFake()}
	rec := updatesCall(s, s.publicUpdates, "GET", PublicUpdatesPath+"?lang=en", "", "", false)
	if rec.Code != 200 {
		t.Fatalf("kod %d", rec.Code)
	}
	var out struct {
		Updates []struct{ Slug string } `json:"updates"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Updates) != 1 || out.Updates[0].Slug != "yayinda" {
		t.Fatalf("yalniz yayinlanmis donmeli: %s", rec.Body.String())
	}
}

func TestUpdatesCreateValidation(t *testing.T) {
	s := &Server{Store: newUpdatesFake()}
	for _, bad := range []string{
		`{"slug":"Bad Slug","title_tr":"A"}`,
		`{"slug":"rss","title_tr":"A"}`,
		`{"slug":"ok","title_tr":""}`,
		`{"slug":"ok","title_tr":"A","kind":"hack"}`,
		`{"slug":"ok","title_tr":"A\nB"}`,
	} {
		if rec := updatesCall(s, s.adminCreateUpdate, "POST", "/x", "", bad, true); rec.Code != 400 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if rec := updatesCall(s, s.adminCreateUpdate, "POST", "/x", "", `{"slug":"ok-1","title_tr":"A","kind":"release"}`, true); rec.Code != 201 {
		t.Fatalf("gecerli olusturma: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUpdatesSendIdempotentAndNeedsPublished(t *testing.T) {
	f := newUpdatesFake()
	s := &Server{Store: f, MailSender: mail.NewSender("127.0.0.1:1", "zorven.app"), AnnounceKey: []byte("0123456789abcdef0123456789abcdef")}

	if rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "draft", "", true); rec.Code != http.StatusConflict {
		t.Fatalf("taslak gonderimi: %d", rec.Code)
	}
	rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "pub", `{}`, true)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"queued":3`) {
		t.Fatalf("ilk gonderim: %d %s", rec.Code, rec.Body.String())
	}
	// Kuyruk (alici yok) hemen biter; sonra ikinci istek idempotans nedeniyle reddedilir.
	time.Sleep(100 * time.Millisecond)
	if rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "pub", `{}`, true); rec.Code != http.StatusConflict {
		t.Fatalf("ikinci gonderim engellenmedi: %d", rec.Code)
	}
	// force onay metni olmadan reddedilir (yanlislikla herkese yeniden gonderim olmasin)
	if rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "pub", `{"force":true}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("onaysiz force kabul edildi: %d", rec.Code)
	}
	if rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "pub", `{"force":true,"confirm":"RESEND"}`, true); rec.Code != http.StatusAccepted {
		t.Fatalf("force reddedildi: %d %s", rec.Code, rec.Body.String())
	}
	if f.resets != 1 {
		t.Fatalf("force gunlugu sifirlamadi: %d", f.resets)
	}
	time.Sleep(100 * time.Millisecond)
	// devam / basarisizlari yeniden dene
	for _, mode := range []string{"resume", "retry_failed"} {
		rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "pub", `{"action":"`+mode+`"}`, true)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s reddedildi: %d %s", mode, rec.Code, rec.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Join(f.claimed, ",") != "resume,retry_failed" {
		t.Fatalf("kipler: %v", f.claimed)
	}
	if rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "pub", `{"action":"zz"}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("gecersiz action: %d", rec.Code)
	}
	// hic gonderilmemis yazide resume yok
	if rec := updatesCall(s, s.adminSendUpdate, "POST", "/x", "draft", `{"action":"resume"}`, true); rec.Code != http.StatusConflict {
		t.Fatalf("taslakta resume: %d", rec.Code)
	}
}

func TestAnnouncementPrefToggle(t *testing.T) {
	f := newUpdatesFake()
	s := &Server{Store: f}
	rec := updatesCall(s, s.putAnnouncementPref, "PUT", "/x", "", `{"subscribed":false,"lang":"ja"}`, false)
	if rec.Code != 200 || f.prefs["u1"] {
		t.Fatalf("tercih yazilmadi: %d", rec.Code)
	}
	rec = updatesCall(s, s.getAnnouncementPref, "GET", "/x", "", "", false)
	if !strings.Contains(rec.Body.String(), `"subscribed":false`) || !strings.Contains(rec.Body.String(), `"asked":true`) {
		t.Fatalf("tercih okunamadi: %s", rec.Body.String())
	}
}

// Opt-in varsayilani: hic yazilmamis kullanici abone degil ve "sorulmadi".
func TestAnnouncementPrefDefaultsToOptOut(t *testing.T) {
	s := &Server{Store: newUpdatesFake()}
	rec := updatesCall(s, s.getAnnouncementPref, "GET", "/x", "", "", false)
	if !strings.Contains(rec.Body.String(), `"subscribed":false`) || !strings.Contains(rec.Body.String(), `"asked":false`) {
		t.Fatalf("varsayilan opt-in olmali: %s", rec.Body.String())
	}
}
