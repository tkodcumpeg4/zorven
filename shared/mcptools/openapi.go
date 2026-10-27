package mcptools

import (
	"regexp"
	"sort"
	"strings"
)

// Endpoint, OpenAPI belgesindeki tek bir islem (list_api_endpoints ciktisi).
type Endpoint struct {
	Method   string   `json:"method"`
	Path     string   `json:"path"` // /api/v1 onekiyle
	Summary  string   `json:"summary,omitempty"`
	Tag      string   `json:"tag,omitempty"`
	Required []string `json:"required_fields,omitempty"`
}

var (
	reOAPath   = regexp.MustCompile(`^  (/[^\s:]*):\s*$`)
	reOAMethod = regexp.MustCompile(`^    (get|put|post|delete|patch):\s*$`)
	reOAKey    = regexp.MustCompile(`^( *)([A-Za-z_$][\w$-]*):\s*(.*)$`)
)

// apiBase, OpenAPI "servers" adresinin yol kismi (belgedeki yollar buna goredir).
const apiBase = "/api/v1"

// ParseOpenAPIEndpoints, Zorven OpenAPI YAML'indan (yapisi bilinen, 2 bosluk girintili)
// islem listesini cikarir. Genel amacli bir YAML ayristirici DEGILDIR: yalnizca
// paths -> yol -> yontem -> {summary, tags, requestBody...required} alanlarini
// satir bazli okur; bagimlilik eklememek icin bilincli tercih.
func ParseOpenAPIEndpoints(spec []byte) []Endpoint {
	var out []Endpoint
	inPaths := false
	var cur *Endpoint
	inReqBody := false
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	curPath := ""
	for _, line := range strings.Split(string(spec), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			// ust duzey anahtar
			flush()
			inPaths = strings.HasPrefix(line, "paths:")
			continue
		}
		if !inPaths {
			continue
		}
		if m := reOAPath.FindStringSubmatch(line); m != nil {
			flush()
			curPath = m[1]
			inReqBody = false
			continue
		}
		if m := reOAMethod.FindStringSubmatch(line); m != nil {
			flush()
			cur = &Endpoint{Method: strings.ToUpper(m[1]), Path: apiBase + curPath}
			inReqBody = false
			continue
		}
		if cur == nil {
			continue
		}
		km := reOAKey.FindStringSubmatch(line)
		if km == nil {
			continue
		}
		indent, key, val := len(km[1]), km[2], strings.TrimSpace(km[3])
		if indent == 6 {
			inReqBody = key == "requestBody"
			switch key {
			case "summary":
				cur.Summary = unquoteYAML(val)
			case "tags":
				cur.Tag = firstFlowItem(val)
			}
			continue
		}
		if inReqBody && key == "required" && indent > 6 && strings.HasPrefix(val, "[") && cur.Required == nil {
			cur.Required = flowItems(val)
		}
	}
	flush()
	return out
}

func unquoteYAML(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

func flowItems(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	if i := strings.Index(v, "]"); i >= 0 {
		v = v[:i]
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = unquoteYAML(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstFlowItem(v string) string {
	if it := flowItems(v); len(it) > 0 {
		return it[0]
	}
	return ""
}

// FilterEndpoints, yontem ve serbest metin filtresi uygular (yol, ozet veya etiket
// icinde, buyuk/kucuk harf duyarsiz) ve yola/yonteme gore siralar.
func FilterEndpoints(eps []Endpoint, method, query string) []Endpoint {
	method = strings.ToUpper(strings.TrimSpace(method))
	query = strings.ToLower(strings.TrimSpace(query))
	var out []Endpoint
	for _, e := range eps {
		if method != "" && e.Method != method {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(e.Path+" "+e.Summary+" "+e.Tag), query) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}
