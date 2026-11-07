package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Liste sayfalama sozlesmesi (.planning/2026-10-27-sayfalama):
//   - ?limit=<1..500>&offset=<>=0>; limit verilmezse VARSAYILAN 500.
//   - Yanit govdesi DEGISMEZ; toplam X-Total-Count, devami X-Has-More basliginda.
//   - Gecersiz (sayi degil / negatif / limit<1) deger -> 400 invalid_request.
//
// (Eski cursor tabanli opt-in sayfalama rest_conventions.go'da korunur.)
const (
	listDefaultLimit = 500
	listMaxLimit     = 500
)

// parseListPage, limit/offset sorgu parametrelerini cozer. limit>500 sessizce
// 500'e kelepcelenir.
func parseListPage(r *http.Request) (store.Page, error) {
	q := r.URL.Query()
	pg := store.Page{Limit: listDefaultLimit}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return pg, errBadPage("limit 1 ile 500 arasinda bir tam sayi olmali")
		}
		if n > listMaxLimit {
			n = listMaxLimit
		}
		pg.Limit = n
	}
	if v := strings.TrimSpace(q.Get("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return pg, errBadPage("offset 0 veya daha buyuk bir tam sayi olmali")
		}
		pg.Offset = n
	}
	return pg, nil
}

type errBadPage string

func (e errBadPage) Error() string { return string(e) }

// pageOf, parseListPage'i calistirir; hata varsa 400 yazar ve ok=false doner.
func pageOf(w http.ResponseWriter, r *http.Request) (store.Page, bool) {
	pg, err := parseListPage(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return pg, false
	}
	return pg, true
}

// setTotal, X-Total-Count basligini yazar.
func setTotal(w http.ResponseWriter, n int) {
	w.Header().Set("X-Total-Count", strconv.Itoa(n))
}

// setHasMore, X-Has-More basligini yazar (COUNT pahali oldugunda toplam yerine).
func setHasMore(w http.ResponseWriter, more bool) {
	w.Header().Set("X-Has-More", strconv.FormatBool(more))
}

// setPageTotals, hem X-Total-Count hem de X-Has-More yazar (got = bu sayfadaki satir).
func setPageTotals(w http.ResponseWriter, pg store.Page, got, total int) {
	setTotal(w, total)
	setHasMore(w, pg.Offset+got < total)
}

// legacyCursorMode, eski cursor tabanli sayfalamanin istendigini bildirir
// (?cursor=... verildi). Bu durumda eski yol (id sirali, X-Next-Cursor) calisir.
func legacyCursorMode(r *http.Request) bool {
	return strings.TrimSpace(r.URL.Query().Get("cursor")) != ""
}

// slicePage, bellekteki bir listeyi sayfalar ve basliklari yazar. YALNIZCA SQL'e
// indirilemeyen listeler icindir (cagri-aninda hesaplanan durum, yetki suzgeci).
func slicePage[T any](w http.ResponseWriter, pg store.Page, items []T) []T {
	total := len(items)
	start := pg.Offset
	if start > total {
		start = total
	}
	end := total
	if pg.Limit > 0 && start+pg.Limit < end {
		end = start + pg.Limit
	}
	page := items[start:end]
	if page == nil {
		page = []T{}
	}
	setPageTotals(w, pg, len(page), total)
	return page
}
