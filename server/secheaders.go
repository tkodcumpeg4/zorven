package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// F-20: panel / kontrol duzlemi / pazarlama sitesi icin tarayici guvenlik
// basliklari. Tunel (musteri trafigi) yanitlarina UYGULANMAZ; yalnizca
// kontrol host dalinda cagrilir.

// permissionsPolicy: panel kamera/mikrofon/ekran paylasimi kullanmaz
// (web/app'te getUserMedia/getDisplayMedia yok), bu yuzden istisna yok.
const permissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"

// analyticsOrigins, analytics host adlarindan https origin listesi uretir.
func analyticsOrigins(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, "https://"+h)
		}
	}
	return out
}

// CSP modu: ZORVEN_CSP_MODE=enforce (varsayilan) | report (acil geri donus).
const (
	cspEnforce = "enforce"
	cspReport  = "report"
)

// cspModeFromEnv, ortam degiskeninden modu okur; bilinmeyen deger enforce'a duser.
func cspModeFromEnv(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), cspReport) {
		return cspReport
	}
	return cspEnforce
}

// cspModeFromEnvs: ZORVEN_CSP_MODE=report VEYA ZORVEN_CSP_REPORT_ONLY=true/1
// Report-Only moduna alir (asamali yayin / staging).
func cspModeFromEnvs(mode, reportOnly string) string {
	switch strings.ToLower(strings.TrimSpace(reportOnly)) {
	case "1", "true", "yes", "on":
		return cspReport
	}
	return cspModeFromEnv(mode)
}

var (
	inlineScriptRe = regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	scriptTypeRe   = regexp.MustCompile(`(?i)\stype\s*=\s*["']?([^"'\s>]+)`)
)

// inlineScriptHashes, verilen dosya sistemlerindeki .html dosyalarindan
// calistirilabilir satir ici <script> govdelerinin CSP sha256 kaynaklarini
// ("'sha256-...'") cikarir. src'li, bos ve veri bloklari (ld+json vb.) atlanir;
// importmap/module/js dahildir (script-src onlari da denetler).
func inlineScriptHashes(fsys ...fs.FS) []string {
	set := map[string]bool{}
	for _, f := range fsys {
		fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
				return nil
			}
			b, err := fs.ReadFile(f, p)
			if err != nil {
				return nil
			}
			for _, m := range inlineScriptRe.FindAllSubmatch(b, -1) {
				attrs := " " + strings.ToLower(string(m[1]))
				if strings.Contains(attrs, "src=") || len(strings.TrimSpace(string(m[2]))) == 0 {
					continue
				}
				if tm := scriptTypeRe.FindStringSubmatch(attrs); tm != nil {
					switch tm[1] {
					case "module", "importmap", "text/javascript", "application/javascript":
					default:
						continue
					}
				}
				sum := sha256.Sum256(m[2])
				set["'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'"] = true
			}
			return nil
		})
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	cspHashesOnce sync.Once
	cspHashes     []string
)

// embeddedInlineHashes: gomulu panel + pazarlama sitesi HTML'lerinin hash'leri
// (bir kez, ilk istekte hesaplanir; icerik degismez).
func embeddedInlineHashes() []string {
	cspHashesOnce.Do(func() {
		var list []fs.FS
		if s, err := fs.Sub(webdist, "webdist"); err == nil {
			list = append(list, s)
		}
		if s, err := fs.Sub(marketingFS, "marketing"); err == nil {
			list = append(list, s)
		}
		cspHashes = inlineScriptHashes(list...)
	})
	return cspHashes
}

// isAPIDocsPath:Scalar API dokumani sayfalari (TR/EN/JA, .html'li ve sonda / ile).
func isAPIDocsPath(p string) bool {
	p = strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".html")
	return p == "/docs/api" || p == "/en/docs/api" || p == "/ja/docs/api" || p == "/zh/docs/api"
}

// buildCSP, yola gore CSP degerini uretir. Harici kaynaklar: Scalar
// (cdn.jsdelivr.net, surum sabit + SRI) ve self-hosted Umami (analytics host).
// 'unsafe-eval' YALNIZ API dokumani sayfalarinda (Scalar tam spec islerken ister).
// Google Fonts YOK; panel ikonlari derlemede paketli (api.iconify.design YOK),
// bu yuzden connect-src yalniz self + wss + analytics.
//
// script-src: gomulu HTML'lerdeki satir ici scriptlerin sha256 hash'leri
// eklenir. 'unsafe-inline' YALNIZ hash listesi bos degilse, CSP3'te yok sayilan
// eski-tarayici yedegi olarak kalir; hash yoksa HIC eklenmez. Satir ici olay
// isleyicisi (onclick=) yoktur; script-src-attr 'none' bunu zorunlu kilar.
func buildCSP(analytics []string, apiDocs bool, hashes []string) string {
	script := []string{"'self'"}
	script = append(script, hashes...)
	if len(hashes) > 0 {
		script = append(script, "'unsafe-inline'")
	}
	script = append(script, "https://cdn.jsdelivr.net")
	if apiDocs {
		script = append(script, "'unsafe-eval'")
	}
	script = append(script, analytics...)
	// "IP'mi algila" (tokens.vue) artik kendi ucumuzu kullanir (/api/v1/my-ip); ucuncu taraf yok.
	connect := append([]string{"'self'", "wss:"}, analytics...)
	parts := []string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"script-src-attr 'none'",
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net",
		// blob: ekran paylasimi icin zorunlu: MJPEG kareleri <img src=blob:>,
		// H.264 MSE akisi <video src=blob:> ile cizilir.
		"img-src 'self' data: https: blob:",
		"media-src 'self' blob:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connect, " "),
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	return strings.Join(parts, "; ")
}

// setControlSecurityHeaders, CSP + Permissions-Policy ekler. mode report ise
// Report-Only basligi, aksi halde zorunlu Content-Security-Policy.
func setControlSecurityHeaders(h http.Header, analytics []string, path, mode string) {
	h.Set("Permissions-Policy", permissionsPolicy)
	name := "Content-Security-Policy"
	if mode == cspReport {
		name += "-Report-Only"
	}
	h.Set(name, buildCSP(analytics, isAPIDocsPath(path), embeddedInlineHashes()))
}
