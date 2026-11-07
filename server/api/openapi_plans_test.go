package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pathBlock, OpenAPI metninde "  <yol>:" ile baslayan yol blogunu (bir sonraki yola kadar) doner.
func pathBlock(spec []byte, path string) string {
	lines := strings.Split(string(spec), "\n")
	var out []string
	in := false
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "  "+path+":") {
			in = true
			continue
		}
		if in && strings.HasPrefix(l, "  /") && !strings.HasPrefix(l, "   ") {
			break
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// Ek 1 madde 6: GET /api/v1/plans KIMLIK ister (kod dogru, belge yanlisti). OpenAPI belgeleri
// (tr/en/ja/zh) /plans icin "security: []" (herkese acik) DEMEMELI ve 401 yanitini tanimlamali.
func TestOpenAPIPlansDocumentedAsAuthenticated(t *testing.T) {
	specs := map[string][]byte{"tr": openapiYAML, "en": openapiEnYAML}
	for lang, spec := range specs {
		block := pathBlock(spec, "/plans")
		if block == "" {
			t.Fatalf("%s: /plans yolu bulunamadi", lang)
		}
		if strings.Contains(block, "security: []") {
			t.Errorf("%s: /plans herkese acik (security: []) olarak belgelenmemeli", lang)
		}
		if !strings.Contains(block, "401") || !strings.Contains(block, "responses/Unauthorized") {
			t.Errorf("%s: /plans icin 401 Unauthorized yaniti tanimli olmali", lang)
		}
	}
	// Herkese acik kalanlar etkilenmedi: /health hala security: [].
	if !strings.Contains(pathBlock(openapiYAML, "/health"), "security: []") {
		t.Error("/health herkese acik kalmali")
	}
}

// Kodun tarafi: kimliksiz GET /api/v1/plans, acik yollar listesinde olmadigi surece 401 alir.
func TestPlansWithoutCredentialsIs401(t *testing.T) {
	reached := false
	mw := &Middleware{
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }),
		// main.go'daki PublicPaths ile ayni turde: /plans YOK.
		PublicPaths: map[string]bool{"/api/v1/health": true, "/api/v1/openapi.yaml": true},
	}
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/plans", nil))
	if w.Code != http.StatusUnauthorized || reached {
		t.Fatalf("kimliksiz /plans 401 olmali ve isleyiciye ulasmamali: kod=%d ulasti=%v", w.Code, reached)
	}
}
