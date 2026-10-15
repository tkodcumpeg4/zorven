package updates

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// fakeStore, UpdatesStore'un bellek ici uygulamasi. Kullanici listesi
// "dogrulanmamis" ve "abonelikten cikmis" durumlari da icerir; depo gercek
// SQL'deki suzmeyi taklit eder, kuyruk ayrica son kontrol yapar.
type fakeStore struct {
	mu     sync.Mutex
	posts  []store.UpdatePost
	users  []store.AnnouncementRecipient
	sub    map[string]bool // true = acik riza (opt-in)
	deliv  map[string]string
	delivN map[string]int // alici basina gonderim denemesi (kayit sayisi)
	claims int
	stats  store.UpdateEmailStats
}

func newFake() *fakeStore {
	return &fakeStore{sub: map[string]bool{}, deliv: map[string]string{}, delivN: map[string]int{}}
}

func (f *fakeStore) ListUpdatePosts(_ context.Context, onlyPublished bool, limit, offset int) ([]store.UpdatePost, int, error) {
	var out []store.UpdatePost
	for _, p := range f.posts {
		if onlyPublished && p.PublishedAt == nil {
			continue
		}
		out = append(out, p)
	}
	return out, len(out), nil
}
func (f *fakeStore) GetUpdatePost(_ context.Context, id string) (store.UpdatePost, error) {
	for _, p := range f.posts {
		if p.ID == id {
			return p, nil
		}
	}
	return store.UpdatePost{}, store.ErrNotFound
}
func (f *fakeStore) GetUpdatePostBySlug(_ context.Context, slug string, onlyPublished bool) (store.UpdatePost, error) {
	for _, p := range f.posts {
		if p.Slug == slug && (!onlyPublished || p.PublishedAt != nil) {
			return p, nil
		}
	}
	return store.UpdatePost{}, store.ErrNotFound
}
func (f *fakeStore) CreateUpdatePost(context.Context, store.UpdatePost) (store.UpdatePost, error) {
	return store.UpdatePost{}, nil
}
func (f *fakeStore) UpdateUpdatePost(context.Context, store.UpdatePost) (store.UpdatePost, error) {
	return store.UpdatePost{}, nil
}
func (f *fakeStore) DeleteUpdatePost(context.Context, string) error { return nil }
func (f *fakeStore) SetUpdatePostPublished(context.Context, string, bool) (store.UpdatePost, error) {
	return store.UpdatePost{}, nil
}
func (f *fakeStore) ClaimUpdatePostEmail(context.Context, string, bool, int) (bool, error) {
	f.claims++
	return true, nil
}
func (f *fakeStore) SetUpdatePostEmailStats(_ context.Context, _ string, st store.UpdateEmailStats) error {
	f.mu.Lock()
	f.stats = st
	f.mu.Unlock()
	return nil
}
func (f *fakeStore) ListAnnouncementRecipients(_ context.Context, after string, limit int) ([]store.AnnouncementRecipient, error) {
	var out []store.AnnouncementRecipient
	for _, u := range f.users {
		if u.UserID > after && f.sub[u.UserID] {
			out = append(out, u) // dogrulanmamislar dahil: kuyruk kendisi eler
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (f *fakeStore) CountAnnouncementRecipients(context.Context) (int, error) {
	return len(f.users), nil
}
func (f *fakeStore) GetAnnouncementPref(_ context.Context, id string) (store.AnnouncementPref, error) {
	return store.AnnouncementPref{Subscribed: f.sub[id]}, nil
}
func (f *fakeStore) SetAnnouncementPref(_ context.Context, id string, sub bool, _, _ string) error {
	f.mu.Lock()
	f.sub[id] = sub
	f.mu.Unlock()
	return nil
}
func (f *fakeStore) IsAnnouncementSubscribed(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sub[id], nil
}

func (f *fakeStore) ClaimUpdatePostResume(context.Context, string, string, int) (bool, error) {
	return true, nil
}
func (f *fakeStore) MarkInterruptedUpdateSends(context.Context) (int, error) { return 0, nil }
func (f *fakeStore) RecordUpdateDelivery(_ context.Context, _, uid, status, _ string) error {
	f.mu.Lock()
	f.deliv[uid] = status
	f.delivN[uid]++
	f.mu.Unlock()
	return nil
}
func (f *fakeStore) UpdateDeliveryStatuses(_ context.Context, _ string, ids []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, id := range ids {
		if st, ok := f.deliv[id]; ok {
			out[id] = st
		}
	}
	return out, nil
}
func (f *fakeStore) CountUpdateDeliveries(context.Context, string) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sent, failed int
	for _, st := range f.deliv {
		if st == store.DeliverySent {
			sent++
		} else {
			failed++
		}
	}
	return sent, failed, nil
}
func (f *fakeStore) ResetUpdateDeliveries(context.Context, string) error {
	f.deliv = map[string]string{}
	return nil
}

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestUnsubTokenRoundTripAndTamper(t *testing.T) {
	tok := UnsubToken(testKey, "user_1")
	if id, ok := ParseUnsubToken(testKey, tok); !ok || id != "user_1" {
		t.Fatalf("gecerli jeton reddedildi: %q %v", id, ok)
	}
	if _, ok := ParseUnsubToken([]byte("baska-anahtar-baska-anahtar-1234"), tok); ok {
		t.Fatal("baska anahtarla dogrulandi")
	}
	other := UnsubToken(testKey, "user_2")
	forged := strings.SplitN(other, ".", 2)[0] + "." + strings.SplitN(tok, ".", 2)[1]
	if _, ok := ParseUnsubToken(testKey, forged); ok {
		t.Fatal("sahte jeton kabul edildi")
	}
	for _, bad := range []string{"", ".", "abc", "a.b", strings.Repeat("x", 600)} {
		if _, ok := ParseUnsubToken(testKey, bad); ok {
			t.Fatalf("bozuk jeton kabul edildi: %q", bad)
		}
	}
	if _, ok := ParseUnsubToken(nil, tok); ok {
		t.Fatal("anahtarsiz dogrulama")
	}
}

func publishedPost() store.UpdatePost {
	now := time.Now().Add(-time.Hour)
	return store.UpdatePost{ID: "upd_1", Slug: "v0-2-9", Kind: "release", Version: "0.2.9",
		TitleTR: "Yeni surum", BodyTR: "- **madde**", TitleEN: "New release", BodyEN: "- item", PublishedAt: &now}
}

func TestCampaignSkipsUnsubscribedAndUnverified(t *testing.T) {
	f := newFake()
	f.posts = []store.UpdatePost{publishedPost()}
	f.users = []store.AnnouncementRecipient{
		{UserID: "a", Email: "a@x.com", Verified: true},
		{UserID: "b", Email: "b@x.com", Verified: false}, // dogrulanmamis
		{UserID: "c", Email: "c@x.com", Verified: true, Lang: "en"},
		{UserID: "d", Email: "d@x.com", Verified: true}, // kuyruk beklerken cikacak
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		f.sub[id] = true
	}
	var mu sync.Mutex
	var sent []string
	var hdrs [][]string
	c := &Campaign{Store: f, Key: testKey, BaseURL: "https://zorven.app", From: "Zorven <no-reply@zorven.app>", Rate: 1000,
		Mailer: func(from, to, subject, text, html string, headers []string) error {
			mu.Lock()
			sent = append(sent, to)
			hdrs = append(hdrs, headers)
			mu.Unlock()
			if to == "a@x.com" {
				f.SetAnnouncementPref(context.Background(), "d", false, "", "") // a gonderilirken d cikar
			}
			return nil
		}}
	if !c.Run(context.Background(), f.posts[0], 4) {
		t.Fatal("Run reddedildi")
	}
	if strings.Join(sent, ",") != "a@x.com,c@x.com" {
		t.Fatalf("beklenmeyen alicilar: %v", sent)
	}
	if f.stats.Sent != 2 || f.stats.Failed != 0 || !f.stats.Done {
		t.Fatalf("istatistik: %+v", f.stats)
	}
	joined := strings.Join(hdrs[0], "\n")
	if !strings.Contains(joined, "List-Unsubscribe: <https://zorven.app/u/announcements?token=") ||
		!strings.Contains(joined, "List-Unsubscribe-Post: List-Unsubscribe=One-Click") {
		t.Fatalf("RFC 8058 basliklari eksik: %s", joined)
	}
}

func recipients(f *fakeStore, ids ...string) {
	for _, id := range ids {
		f.users = append(f.users, store.AnnouncementRecipient{UserID: id, Email: id + "@x.com", Verified: true})
		f.sub[id] = true
	}
}

// Opt-in: tercih satiri/riza olmayan kullaniciya gonderilmez.
func TestCampaignOptInOnly(t *testing.T) {
	f := newFake()
	recipients(f, "a")
	f.users = append(f.users, store.AnnouncementRecipient{UserID: "z", Email: "z@x.com", Verified: true}) // riza yok
	var sent []string
	c := &Campaign{Store: f, Key: testKey, BaseURL: "https://zorven.app", Rate: 1000,
		Mailer: func(_, to, _, _, _ string, _ []string) error { sent = append(sent, to); return nil }}
	c.Run(context.Background(), publishedPost(), 1)
	if strings.Join(sent, ",") != "a@x.com" {
		t.Fatalf("riza vermeyene gonderildi: %v", sent)
	}
}

func TestCampaignResumeSkipsSentAndRetryFailed(t *testing.T) {
	f := newFake()
	recipients(f, "a", "b", "c")
	f.deliv["a"] = store.DeliverySent
	f.deliv["b"] = store.DeliveryFailed
	var mu sync.Mutex
	var sent []string
	mk := func(mode string, fail map[string]bool) *Campaign {
		return &Campaign{Store: f, Key: testKey, BaseURL: "https://zorven.app", Rate: 1000, Mode: mode,
			Mailer: func(_, to, _, _, _ string, _ []string) error {
				mu.Lock()
				sent = append(sent, to)
				mu.Unlock()
				if fail[to] {
					return errors.New("smtp 450")
				}
				return nil
			}}
	}
	// devam: a (gonderilmis) atlanir; b (basarisiz) ve c gonderilir
	mk("", map[string]bool{"b@x.com": true}).Run(context.Background(), publishedPost(), 3)
	if strings.Join(sent, ",") != "b@x.com,c@x.com" {
		t.Fatalf("devam alicilari: %v", sent)
	}
	if f.stats.Sent != 2 || f.stats.Failed != 1 || !f.stats.Done || f.stats.Interrupted {
		t.Fatalf("istatistik: %+v", f.stats)
	}
	// basarisizlari yeniden dene: yalniz b
	sent = nil
	mk(store.SendModeRetryFailed, nil).Run(context.Background(), publishedPost(), 3)
	if strings.Join(sent, ",") != "b@x.com" {
		t.Fatalf("retry_failed alicilari: %v", sent)
	}
	if f.stats.Sent != 3 || f.stats.Failed != 0 || !f.stats.Done {
		t.Fatalf("retry istatistigi: %+v", f.stats)
	}
	// tamamen bitmis gunlukte ikinci calistirma kimseye gondermez
	sent = nil
	mk("", nil).Run(context.Background(), publishedPost(), 3)
	if len(sent) != 0 {
		t.Fatalf("zaten gonderilmislere tekrar gonderildi: %v", sent)
	}
}

func TestCampaignCancelledMarksInterruptedNotDone(t *testing.T) {
	f := newFake()
	recipients(f, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	c := &Campaign{Store: f, Key: testKey, BaseURL: "https://zorven.app", Rate: 1000,
		Mailer: func(string, string, string, string, string, []string) error { cancel(); return nil }}
	c.Run(ctx, publishedPost(), 2)
	if f.stats.Done || !f.stats.Interrupted || f.stats.Sent != 1 {
		t.Fatalf("kesilen gonderim: %+v", f.stats)
	}
}

func TestCampaignNoConcurrentRunForSamePost(t *testing.T) {
	f := newFake()
	f.users = []store.AnnouncementRecipient{{UserID: "a", Email: "a@x.com", Verified: true}}
	f.sub["a"] = true
	release := make(chan struct{})
	started := make(chan struct{})
	c := &Campaign{Store: f, Key: testKey, BaseURL: "https://zorven.app", Rate: 1000,
		Mailer: func(string, string, string, string, string, []string) error {
			close(started)
			<-release
			return nil
		}}
	p := publishedPost()
	done := make(chan bool)
	go func() { done <- c.Run(context.Background(), p, 1) }()
	<-started
	if c.Run(context.Background(), p, 1) {
		t.Fatal("ayni yazi icin ikinci es zamanli Run kabul edildi")
	}
	close(release)
	if !<-done {
		t.Fatal("ilk Run basarisiz")
	}
}

func siteWith(f *fakeStore) (*Site, http.Handler) {
	var mfs fs.FS = fstest.MapFS{
		"guncellemeler.html": {Data: []byte(`<html lang="tr"><head><title>x</title><meta name="description" content="d">
<!-- zv:head -->
  <link rel="canonical" href="https://zorven.app/guncellemeler">
<!-- /zv:head --></head><body><header></header><main>old</main></body></html>`)},
		"en/updates.html": {Data: []byte(`<html lang="en"><head><title>x</title><meta name="description" content="d">
<!-- zv:head --><!-- /zv:head --></head><body><main>old</main></body></html>`)},
	}
	s := &Site{Store: f, FS: mfs, Key: testKey, BaseURL: "https://zorven.app"}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	return s, s.Wrap(next)
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestSitePublicOnlyPublishedAndFallback(t *testing.T) {
	f := newFake()
	pub := publishedPost()
	draft := store.UpdatePost{ID: "upd_2", Slug: "taslak", Kind: "update", TitleTR: "GIZLI TASLAK"}
	f.posts = []store.UpdatePost{pub, draft}
	_, h := siteWith(f)

	if rec := get(h, "GET", "/guncellemeler"); rec.Code != 200 || strings.Contains(rec.Body.String(), "GIZLI TASLAK") || !strings.Contains(rec.Body.String(), "Yeni surum") {
		t.Fatalf("liste hatali: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(h, "GET", "/guncellemeler/taslak"); rec.Code != 404 {
		t.Fatalf("taslak acik: %d", rec.Code)
	}
	if rec := get(h, "GET", "/en/updates/v0-2-9"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "New release") {
		t.Fatalf("en yazi hatali: %d", rec.Code)
	}
	// ja ceviri yok: en'e duser ve not gosterir; ja iskeleti yoksa en iskeleti kullanilir.
	rec := get(h, "GET", "/ja/updates/v0-2-9")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "New release") || !strings.Contains(rec.Body.String(), "upd-note") {
		t.Fatalf("ja yedek hatali: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(h, "GET", "/guncellemeler/..%2fx"); rec.Code == 200 {
		t.Fatal("gecersiz slug 200")
	}
	if rec := get(h, "GET", "/about"); rec.Code != 299 {
		t.Fatalf("ilgisiz yol next'e gecmedi: %d", rec.Code)
	}
	rss := get(h, "GET", "/en/updates/rss.xml")
	if rss.Code != 200 || !strings.Contains(rss.Header().Get("Content-Type"), "rss+xml") || strings.Contains(rss.Body.String(), "taslak") {
		t.Fatalf("rss hatali: %d %s", rss.Code, rss.Body.String())
	}
	if rec := get(h, "POST", "/guncellemeler"); rec.Code != 405 {
		t.Fatalf("POST izinli: %d", rec.Code)
	}
}

func post(h http.Handler, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSiteUnsubscribeFlow(t *testing.T) {
	f := newFake()
	f.sub["user_9"] = true
	_, h := siteWith(f)
	raw := UnsubToken(testKey, "user_9")
	tok := url.QueryEscape(raw)

	// gecersiz jeton hicbir sey degistirmez
	if rec := get(h, "GET", "/u/announcements?token=bozuk"); rec.Code != 400 || !f.sub["user_9"] {
		t.Fatalf("gecersiz jeton: %d", rec.Code)
	}
	// GET (ve HEAD) yalniz onay sayfasi gosterir: tarayicilar/guvenlik taramalari abonelik bozmaz
	for _, m := range []string{"GET", "HEAD"} {
		rec := get(h, m, "/u/announcements?token="+tok+"&lang=en")
		if rec.Code != 200 || !f.sub["user_9"] {
			t.Fatalf("%s abonelikten cikardi: %d", m, rec.Code)
		}
		if m == "GET" && (!strings.Contains(rec.Body.String(), `method="post"`) || !strings.Contains(rec.Body.String(), `value="unsubscribe"`) ||
			!strings.Contains(rec.Body.String(), "Unsubscribe")) {
			t.Fatalf("onay formu yok: %s", rec.Body.String())
		}
	}
	for lang, want := range map[string]string{"tr": "Abonelikten çık", "ja": "お知らせメールの配信を停止"} {
		if rec := get(h, "GET", "/u/announcements?token="+tok+"&lang="+lang); !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s onay metni yok", lang)
		}
	}
	// tanimsiz eylemli POST degistirmez
	if rec := post(h, "/u/announcements", url.Values{"token": {raw}}.Encode()); rec.Code != 400 || !f.sub["user_9"] {
		t.Fatalf("eylemsiz POST: %d", rec.Code)
	}
	// form POST action=unsubscribe
	if rec := post(h, "/u/announcements?lang=tr", url.Values{"token": {raw}, "action": {"unsubscribe"}}.Encode()); rec.Code != 200 || f.sub["user_9"] {
		t.Fatalf("form abonelikten cikaramadi: %d", rec.Code)
	}
	// yeniden abone
	if rec := post(h, "/u/announcements", url.Values{"token": {raw}, "action": {"resubscribe"}}.Encode()); rec.Code != 200 || !f.sub["user_9"] {
		t.Fatalf("yeniden abonelik: %d", rec.Code)
	}
	// RFC 8058 tek tik POST onaysiz calisir
	if rec := post(h, "/u/announcements?token="+tok, "List-Unsubscribe=One-Click"); rec.Code != 200 || f.sub["user_9"] {
		t.Fatalf("tek tik POST abonelikten cikaramadi: %d", rec.Code)
	}
}

func TestBuildEmailEscapesAndLocalizes(t *testing.T) {
	p := publishedPost()
	p.TitleJA = "新リリース"
	p.BodyJA = "<script>alert(1)</script> [x](javascript:1)"
	em := BuildEmail(p, "ja", "https://zorven.app", testKey, "u1")
	if strings.Contains(em.HTML, "<script") || strings.Contains(strings.ToLower(em.HTML), "href=\"javascript") {
		t.Fatalf("e-posta HTML'i guvensiz: %s", em.HTML)
	}
	if !strings.Contains(em.HTML, "新リリース") || !strings.Contains(em.HTML, `lang="ja"`) {
		t.Fatal("ja icerik/dil yok")
	}
	if !strings.Contains(em.Text, "/u/announcements?token=") {
		t.Fatal("duz metinde abonelik baglantisi yok")
	}
}
