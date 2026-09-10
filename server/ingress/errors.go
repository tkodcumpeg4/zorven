package ingress

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
)

// writeError, ingress hatalarini istemcinin bekledigi bicimde doner.
//
// api_contract.md §3: Accept basligi JSON isteyen istemcilere JSON,
// tarayicilara markali HTML hata sayfasi.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("X-Rpshell-Error", code)

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": code, "message": msg},
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(errorPage(status, code, msg)))
}

// wantsJSON, Accept basliginda JSON'un HTML'den once gelip gelmedigine bakar.
func wantsJSON(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if accept == "" {
		return false
	}
	if !strings.Contains(accept, "application/json") {
		return false
	}
	// Hem JSON hem HTML kabul ediliyorsa (tarayicilar bazen boyle yapar)
	// once gelen kazanir.
	jsonAt := strings.Index(accept, "application/json")
	htmlAt := strings.Index(accept, "text/html")
	return htmlAt == -1 || jsonAt < htmlAt
}

// errorPage, dashboard ile ayni gorsel dile sahip minimal bir hata sayfasi.
// Renkler web/app/assets/css/main.css token'lariyla ayni.
func errorPage(status int, code, msg string) string {
	return `<!doctype html>
<html lang="tr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark">
<title>` + html.EscapeString(code) + ` · zorven</title>
<style>
  :root { color-scheme: dark; }
  body {
    margin: 0; min-height: 100vh;
    display: grid; place-items: center;
    background: #0A0A0A; color: #F5F5F5;
    font-family: 'IBM Plex Sans', ui-sans-serif, system-ui, sans-serif;
    padding: 24px;
  }
  .card {
    max-width: 30rem; width: 100%;
    border: 1px solid #262626; border-radius: 12px;
    background: #171717; padding: 28px;
  }
  .code {
    font-family: ui-monospace, 'JetBrains Mono', monospace;
    font-size: 11px; letter-spacing: .08em; text-transform: uppercase;
    color: #8A8A8A;
  }
  h1 { margin: 10px 0 6px; font-size: 22px; font-weight: 700; color: #fff; }
  p  { margin: 0; color: #A3A3A3; font-size: 14px; line-height: 1.55; }
  .status {
    margin-top: 20px; padding-top: 16px; border-top: 1px solid #262626;
    font-family: ui-monospace, 'JetBrains Mono', monospace;
    font-size: 12px; color: #8A8A8A;
  }
  .dot { display:inline-block; width:6px; height:6px; border-radius:50%;
         background:#22C55E; margin-right:7px; vertical-align:middle; }
</style>
</head>
<body>
  <div class="card">
    <p class="code">` + html.EscapeString(code) + `</p>
    <h1>` + http.StatusText(status) + `</h1>
    <p>` + html.EscapeString(msg) + `</p>
    <div class="status"><span class="dot"></span>zorven tunnel</div>
  </div>
</body>
</html>`
}
