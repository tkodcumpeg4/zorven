package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

type importResp struct {
	DryRun  bool           `json:"dry_run"`
	Counts  map[string]int `json:"counts"`
	Results []importResult `json:"results"`
}

func doImport(t *testing.T, s *Server, query, body string) (int, importResp) {
	t.Helper()
	rec := updatesCall(s, s.adminImportUpdates, "POST", "/x"+query, "", body, true)
	var out importResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const impOne = `{"slug":"yeni-yazi","kind":"release","version":"1.0","title_tr":"Yeni","body_tr":"govde","published":true}`

func TestUpdatesImportForbiddenForNonAdmin(t *testing.T) {
	s := &Server{Store: newUpdatesFake()}
	if rec := updatesCall(s, s.adminImportUpdates, "POST", "/x", "", impOne, false); rec.Code != http.StatusForbidden {
		t.Fatalf("ice aktarma: %d", rec.Code)
	}
	if rec := updatesCall(s, s.adminExportUpdates, "GET", "/x", "", "", false); rec.Code != http.StatusForbidden {
		t.Fatalf("disa aktarma: %d", rec.Code)
	}
}

func TestUpdatesImportValidation(t *testing.T) {
	f := newUpdatesFake()
	s := &Server{Store: f}
	body := `[{"slug":"a","title_tr":"A","body_tr":"x","bogus":1},
	 {"slug":"b","kind":"zzz","title_tr":"B","body_tr":"x"},
	 {"slug":"c","title_tr":"C"},
	 {"slug":"d","title_tr":"","body_tr":"x"},
	 {"slug":"Bad Slug","title_tr":"E","body_tr":"x"},
	 {"slug":"f","kind":"maintenance","title_tr":"F","body_tr":"x","maintenance_start":"2026-10-20T10:00:00Z","maintenance_end":"2026-10-20T09:00:00Z"},
	 {"slug":"g","kind":"maintenance","title_tr":"G","body_tr":"x","maintenance_start":"yarin"},
	 {"slug":"ok","title_tr":"Ok","body_tr":"x"},
	 {"slug":"ok","title_tr":"Ok2","body_tr":"x"}]`
	code, out := doImport(t, s, "", body)
	if code != 200 {
		t.Fatalf("kod %d", code)
	}
	if out.Counts["error"] != 8 || out.Counts["create"] != 1 {
		t.Fatalf("sayilar: %+v", out.Counts)
	}
	if !strings.Contains(out.Results[0].Messages[0], "bogus") {
		t.Fatalf("bilinmeyen alan: %+v", out.Results[0])
	}
	if f.creates != 1 {
		t.Fatalf("yazim sayisi %d", f.creates)
	}
	for _, bad := range []string{"", "5", "[]", `{"posts":[],"x":1}`} {
		if code, _ := doImport(t, s, "", bad); code != http.StatusBadRequest {
			t.Fatalf("%q: %d", bad, code)
		}
	}
	if code, _ := doImport(t, s, "?mode=sil", impOne); code != http.StatusBadRequest {
		t.Fatalf("gecersiz mode: %d", code)
	}
	big := `{"slug":"a","title_tr":"A","body_tr":"` + strings.Repeat("x", 1<<20) + `"}`
	if code, _ := doImport(t, s, "", big); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("buyuk govde: %d", code)
	}
}

func TestUpdatesImportDryRunWritesNothing(t *testing.T) {
	f := newUpdatesFake()
	s := &Server{Store: f}
	body := `[` + impOne + `,{"slug":"taslak","title_tr":"T","body_tr":"x"}]`
	_, out := doImport(t, s, "?dry_run=true&mode=upsert&publish=true", body)
	if !out.DryRun || out.Counts["create"] != 1 || out.Counts["update"] != 1 {
		t.Fatalf("onizleme: %+v", out)
	}
	if f.creates != 0 || f.updates != 0 || len(f.posts) != 2 {
		t.Fatalf("onizleme yazdi: creates=%d updates=%d posts=%d", f.creates, f.updates, len(f.posts))
	}
}

func TestUpdatesImportCreateOnlyVsUpsert(t *testing.T) {
	f := newUpdatesFake() // "taslak" slug'i var
	s := &Server{Store: f}
	body := `[{"slug":"taslak","title_tr":"Degisti","body_tr":"y"},{"slug":"yeni","title_tr":"Yeni","body_tr":"x"}]`
	_, out := doImport(t, s, "", body)
	if out.Counts["skip"] != 1 || out.Counts["create"] != 1 || f.updates != 0 {
		t.Fatalf("create_only: %+v", out)
	}
	if f.posts["draft"].TitleTR != "Taslak" {
		t.Fatal("create_only var olani degistirdi")
	}
	_, out = doImport(t, s, "?mode=upsert", body)
	if out.Counts["update"] != 2 {
		t.Fatalf("upsert: %+v", out)
	}
	if f.posts["draft"].TitleTR != "Degisti" || f.posts["draft"].BodyTR != "y" {
		t.Fatalf("upsert guncellemedi: %+v", f.posts["draft"])
	}
}

func TestUpdatesImportPublishGating(t *testing.T) {
	f := newUpdatesFake()
	s := &Server{Store: f}
	// publish secenegi yok: published=true yoksayilir, taslak olur
	_, out := doImport(t, s, "", impOne)
	if out.Counts["create"] != 1 || out.Results[0].Publish || len(out.Results[0].Messages) == 0 {
		t.Fatalf("%+v", out)
	}
	if f.posts[out.Results[0].ID].PublishedAt != nil {
		t.Fatal("secenek olmadan yayinlandi")
	}
	// secenek var ama published=false: taslak
	_, out = doImport(t, s, "?publish=true", `{"slug":"t2","title_tr":"T","body_tr":"x","published":false}`)
	if f.posts[out.Results[0].ID].PublishedAt != nil {
		t.Fatal("published=false yayinlandi")
	}
	// ikisi de var: yayinlanir
	_, out = doImport(t, s, "?publish=true", `{"slug":"t3","title_tr":"T","body_tr":"x","published":true}`)
	if p := f.posts[out.Results[0].ID]; p.PublishedAt == nil || !out.Results[0].Publish {
		t.Fatalf("yayinlanmadi: %+v", out)
	}
}

func TestUpdatesExportImportRoundTrip(t *testing.T) {
	f := newUpdatesFake()
	start := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	f.posts["m"] = store.UpdatePost{ID: "m", Slug: "bakim", Kind: "maintenance", TitleTR: "Bakim", BodyTR: "b",
		TitleJA: "保守", BodyJA: "本文", MaintenanceStart: &start, MaintenanceEnd: &end}
	for _, id := range []string{"pub", "draft"} {
		p := f.posts[id]
		p.BodyTR = "govde " + id
		f.posts[id] = p
	}
	s := &Server{Store: f}

	rec := updatesCall(s, s.adminExportUpdates, "GET", "/x", "", "", true)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("disa aktarma: %d", rec.Code)
	}
	exported := rec.Body.String()
	rec2 := updatesCall(s, s.adminExportUpdates, "GET", "/x?ids=m", "", "", true)
	if strings.Count(rec2.Body.String(), `"slug"`) != 1 || !strings.Contains(rec2.Body.String(), "bakim") {
		t.Fatalf("ids filtresi: %s", rec2.Body.String())
	}

	// bos bir depoya yayin secenegiyle ice aktar: ayni icerik geri gelmeli
	f2 := &updatesFake{posts: map[string]store.UpdatePost{}, emailed: map[string]bool{}, prefs: map[string]bool{}}
	s2 := &Server{Store: f2}
	code, out := doImport(t, s2, "?publish=true", exported)
	if code != 200 || out.Counts["create"] != 3 || out.Counts["error"] != 0 {
		t.Fatalf("ice aktarma: %d %+v", code, out)
	}
	rec3 := updatesCall(s2, s2.adminExportUpdates, "GET", "/x", "", "", true)
	if normExport(t, rec3.Body.String()) != normExport(t, exported) {
		t.Fatalf("gidis-donus farkli:\n%s\n---\n%s", exported, rec3.Body.String())
	}
}

// normExport, disa aktarma ciktisini slug'a gore anahtarlar (liste sirasi belirsiz).
func normExport(t *testing.T, s string) string {
	t.Helper()
	var env struct {
		Posts []map[string]any `json:"posts"`
	}
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	for _, p := range env.Posts {
		m[p["slug"].(string)] = p
	}
	b, _ := json.Marshal(m)
	return string(b)
}
