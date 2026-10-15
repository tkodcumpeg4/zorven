package updates

import (
	"html"
	"net/http"
	"strings"
)

type unsubText struct {
	Title, DoneUnsub, DoneSub, Invalid, Resub, Unsub, Hint, Home, Confirm string
}

var unsubTexts = map[string]unsubText{
	LangTR: {"Duyuru e-postaları", "Artık Zorven ürün duyuru e-postaları almayacaksın.", "Duyuru e-postalarına yeniden abone oldun.",
		"Bu bağlantı geçersiz veya süresi dolmuş.", "Yanlışlıkla mı oldu? Yeniden abone ol", "Abonelikten çık",
		"Bu tercihi istediğin zaman panelde Profil sayfasından da değiştirebilirsin.", "Zorven ana sayfası",
		"Zorven ürün duyuru e-postalarından çıkmak istiyor musun? Onaylamak için düğmeye bas."},
	LangEN: {"Announcement emails", "You will no longer receive Zorven product announcement emails.", "You are subscribed to announcement emails again.",
		"This link is invalid or has expired.", "Did this by mistake? Subscribe again", "Unsubscribe",
		"You can also change this any time on the Profile page of your dashboard.", "Zorven home",
		"Do you want to stop receiving Zorven product announcement emails? Press the button to confirm."},
	LangJA: {"お知らせメール", "今後 Zorven の製品お知らせメールは届きません。", "お知らせメールを再度購読しました。",
		"このリンクは無効か、有効期限が切れています。", "間違いですか？再度購読する", "お知らせメールの配信を停止",
		"この設定はダッシュボードのプロフィールページからいつでも変更できます。", "Zorven ホーム",
		"Zorven の製品お知らせメールの配信を停止しますか？確認するにはボタンを押してください。"},
}

// serveUnsub: abonelikten cikma (onayli) ve RFC 8058 tek tik.
//
//	GET/HEAD /u/announcements?token=...&lang=  -> YALNIZ onay sayfasi (durum DEGISMEZ);
//	                                              posta guvenlik tarayicilari (Safe Links vb.)
//	                                              baglantiyi onceden acar, yanlislikla cikarmamali
//	POST (form action=unsubscribe)             -> abonelikten cikar
//	POST (govde List-Unsubscribe=One-Click)    -> onaysiz abonelikten cikar (RFC 8058)
//	POST (form action=resubscribe)             -> yeniden abone eder
//
// Jeton durumsuz HMAC'tir ve tek basina yetkilendirir; cerez yok, formlar ayni kaynaga gider.
// Yanlis jeton hicbir tercihi degistirmez.
func (s *Site) serveUnsub(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	lang := NormLang(r.FormValue("lang"))
	tx := unsubTexts[lang]
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.PostFormValue("token")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")

	userID, ok := ParseUnsubToken(s.Key, token)
	if !ok {
		s.unsubPage(w, http.StatusBadRequest, lang, tx.Invalid, "", "", "")
		return
	}
	if r.Method != http.MethodPost {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.unsubPage(w, http.StatusOK, lang, tx.Confirm, tx.Unsub, "unsubscribe", token)
		return
	}
	action := r.PostFormValue("action")
	oneClick := r.PostFormValue("List-Unsubscribe") == "One-Click"
	resub := action == "resubscribe"
	if !resub && action != "unsubscribe" && !oneClick {
		s.unsubPage(w, http.StatusBadRequest, lang, tx.Invalid, "", "", "")
		return
	}
	if err := s.Store.SetAnnouncementPref(r.Context(), userID, resub, "", "unsub"); err != nil {
		s.logger().Error("duyuru tercihi yazilamadi", "hata", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if resub {
		s.unsubPage(w, http.StatusOK, lang, tx.DoneSub, "", "", token)
		return
	}
	s.unsubPage(w, http.StatusOK, lang, tx.DoneUnsub, tx.Resub, "resubscribe", token)
}

// unsubPage: btnLabel doluysa o eylemi (action) POST eden bir form gosterir.
func (s *Site) unsubPage(w http.ResponseWriter, status int, lang, msg, btnLabel, action, token string) {
	tx := unsubTexts[lang]
	e := html.EscapeString
	var form string
	if btnLabel != "" && token != "" {
		form = `<form method="post" action="/u/announcements?lang=` + lang + `"><input type="hidden" name="token" value="` + e(token) + `">` +
			`<input type="hidden" name="action" value="` + e(action) + `"><button type="submit">` + e(btnLabel) + `</button></form>`
	}
	page := `<!doctype html><html lang="` + lang + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<meta name="robots" content="noindex"><title>` + e(tx.Title) + ` | Zorven</title>` +
		`<style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;font:16px/1.6 system-ui,sans-serif;background:#07090e;color:#e6edf3}` +
		`@media(prefers-color-scheme:light){body{background:#f6f8fb;color:#0b0e12}}` +
		`main{max-width:460px;padding:32px 24px;text-align:center}h1{font-size:20px}a{color:#3ddc84}` +
		`button{margin-top:8px;padding:10px 18px;border-radius:10px;border:1px solid #3ddc84;background:transparent;color:inherit;font:inherit;cursor:pointer}</style></head>` +
		`<body><main><h1>` + e(tx.Title) + `</h1><p>` + e(msg) + `</p>` + form +
		`<p style="font-size:14px;opacity:.75">` + e(tx.Hint) + `</p><p><a href="/">` + e(tx.Home) + `</a></p></main></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(strings.TrimSpace(page)))
}
