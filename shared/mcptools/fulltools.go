package mcptools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

// ScopeFull, OAuth "full" (tam erisim) kapsami. Bu kapsam yalniz Zorven OAuth
// yetkilendirme sunucusunda verilir (kullanici onayi + taze giris ile); iki genel
// API arac (list_api_endpoints, call_api) yalnizca bu kapsamla kaydedilir.
const ScopeFull = "full"

// maxCallAPIBody, call_api yanit govdesi tavani (kesilirse "truncated": true).
const maxCallAPIBody = 256 << 10

// apiPathRe, call_api yolunda izin verilen karakterler. Yuzde-kodlama, sorgu,
// parca, ters egik cizgi ve bosluk YOK: yol tek bir /api/v1 kaynagina baglanir.
var apiPathRe = regexp.MustCompile(`^/api/v1/[A-Za-z0-9/_\-.~:@,+=]*$`)

// checkAPIPath, call_api yolunu dogrular: yalniz /api/v1, normalize, kacis yok.
func checkAPIPath(p string) error {
	if len(p) > 1024 || !apiPathRe.MatchString(p) {
		return fmt.Errorf("gecersiz path: yalnizca /api/v1/... yollari (sorgu parametresini query ile verin; %%, ?, #, bosluk yok)")
	}
	if path.Clean(p) != p {
		return fmt.Errorf("gecersiz path: normalize edilmemis (.., //, sondaki /)")
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." || s == "." {
			return fmt.Errorf("gecersiz path: '.' ve '..' segmentleri yasak")
		}
	}
	if strings.Contains(p, "//") {
		return fmt.Errorf("gecersiz path: bos segment")
	}
	return nil
}

// streamingPath, soket/akis uclari: surec-ici sentetik istekle calismaz.
func streamingPath(p string) bool {
	p = strings.TrimSuffix(p, "/")
	if p == "/api/v1/events" {
		return true
	}
	return strings.HasSuffix(p, "/terminal") || strings.HasSuffix(p, "/screen")
}

var destructiveVerbs = map[string]bool{
	"revoke": true, "revoke-sessions": true, "rotate": true, "freeze": true, "send": true,
}

// callDestructive, cagrinin geri donusu zor/yikici sayilip confirm=true isteyip istemedigi:
// her DELETE ve iptal/rotasyon/dondurma/gonderim fiilleri.
func callDestructive(method, p string) bool {
	if method == "DELETE" {
		return true
	}
	segs := strings.Split(strings.TrimSuffix(p, "/"), "/")
	return destructiveVerbs[segs[len(segs)-1]]
}

// IsTicketPath, terminal/ekran WS biletlerini ayirt eder (denetimde ozel eylem).
func IsTicketPath(p string) bool {
	p = strings.TrimSuffix(p, "/")
	return p == "/api/v1/terminal-ticket" || p == "/api/v1/screen-ticket"
}

type ListAPIEndpointsIn struct {
	Common
	Filter string `json:"filter,omitempty" jsonschema:"Case-insensitive text matched against path, summary and tag (e.g. 'policies', 'sso', 'tunnels/{id}')."`
	Method string `json:"method,omitempty" jsonschema:"Only this HTTP method: GET, POST, PUT, PATCH or DELETE."`
	Page
}

type CallAPIIn struct {
	Common
	Method  string            `json:"method" jsonschema:"HTTP method: GET, POST, PUT, PATCH or DELETE."`
	Path    string            `json:"path" jsonschema:"Zorven REST API path starting with /api/v1/, without query string, e.g. /api/v1/tunnels. Find paths with list_api_endpoints."`
	Query   map[string]string `json:"query,omitempty" jsonschema:"Query string parameters."`
	Body    any               `json:"body,omitempty" jsonschema:"JSON request body for POST, PUT and PATCH."`
	Confirm bool              `json:"confirm,omitempty" jsonschema:"Must be true for DELETE and other destructive calls (revoke, rotate, freeze, send). Without it the call is refused and nothing is sent."`
}

func fullSpecs() []spec {
	return []spec{
		tool("list_api_endpoints", "List API endpoints",
			"List Zorven REST API endpoints from the OpenAPI document (method, path, summary, required body fields). Use the filter to find an endpoint, then run it with call_api. Requires the full (complete access) scope.",
			ScopeFull, false, true, func(p map[string]*jsonschema.Schema) {
				pageTweak(p)
			},
			func(do call, in ListAPIEndpointsIn) (map[string]any, error) {
				r, err := do(Request{Method: "GET", Path: "/api/v1/openapi.en.yaml"})
				if err != nil {
					return nil, err
				}
				all := FilterEndpoints(ParseOpenAPIEndpoints(r.Body), in.Method, in.Filter)
				items := make([]any, len(all))
				for i, e := range all {
					items[i] = e
				}
				out := pageOut(items, in.Offset, in.Limit)
				out["note"] = "Paths are relative to the API root and already include /api/v1. Some endpoints exist but are not in the OpenAPI document; unknown paths return 404 from call_api."
				return out, nil
			}, projOf[ListAPIEndpointsIn]),

		tool("call_api", "Call Zorven API",
			"Call any Zorven REST API endpoint (/api/v1 only) with the signed-in user's full permissions: same role, resource policies, plan limits and project selection as the web panel. Credential-minting endpoints (API tokens, service accounts, app passwords, device approval) and organization deletion are excluded (403 full_scope_excluded). DELETE and other destructive calls need confirm=true. Responses are capped at 256 KB (truncated is reported). Requires the full scope."+untrusted,
			ScopeFull, true, false, nil,
			func(do call, in CallAPIIn) (map[string]any, error) {
				method := strings.ToUpper(strings.TrimSpace(in.Method))
				switch method {
				case "GET", "POST", "PUT", "PATCH", "DELETE":
				default:
					return nil, fmt.Errorf("method GET, POST, PUT, PATCH veya DELETE olmali")
				}
				if err := checkAPIPath(in.Path); err != nil {
					return nil, err
				}
				if streamingPath(in.Path) {
					return nil, fmt.Errorf("bu uc bir WebSocket/akis ucudur; call_api ile cagrilamaz (terminal/ekran icin *-ticket uclari kullanilir)")
				}
				if callDestructive(method, in.Path) && !in.Confirm {
					return nil, fmt.Errorf("confirm_required: %s %s yikici/geri donusu zor bir islemdir. Onaylamak icin confirm=true ile tekrar cagirin (hicbir istek gonderilmedi)", method, in.Path)
				}
				if method == "GET" && in.Body != nil {
					return nil, fmt.Errorf("GET isteginde body olmaz")
				}
				q := url.Values{}
				for k, v := range in.Query {
					if k == "" || len(k) > 128 || len(v) > 2048 {
						return nil, fmt.Errorf("gecersiz query parametresi")
					}
					q.Set(k, v)
				}
				resp, err := do(Request{Method: method, Path: in.Path, Query: q, Body: in.Body})
				if err != nil && resp.Status == 0 {
					return nil, err
				}
				out := map[string]any{"status": resp.Status, "ok": resp.Status < 400}
				raw := resp.Body
				truncated := false
				if len(raw) > maxCallAPIBody {
					raw, truncated = raw[:maxCallAPIBody], true
				}
				switch {
				case len(raw) == 0:
				case !utf8.Valid(raw) && !truncated:
					out["body"] = fmt.Sprintf("[ikili yanit, %d bayt, content-type=%s; call_api yalnizca metin/JSON dondurur]", len(resp.Body), resp.Header.Get("Content-Type"))
				default:
					if !truncated && json.Valid(raw) {
						var v any
						_ = json.Unmarshal(raw, &v)
						out["body"] = v
					} else {
						out["body"] = truncStrBytes(string(raw), maxCallAPIBody)
					}
				}
				if truncated {
					out["truncated"] = true
					out["total_bytes"] = len(resp.Body)
					out["truncated_note"] = "Yanit 256 KB sinirinda kesildi; limit/cursor veya filtre kullanin."
				}
				if rc := resp.Header.Get("X-Next-Cursor"); rc != "" {
					out["next_cursor"] = rc
				}
				return out, nil
			}, projOf[CallAPIIn], destructiveHint),
	}
}

// truncStrBytes, dizeyi UTF-8 sinirini bozmadan n bayta indirir (ek not yazmaz).
func truncStrBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
