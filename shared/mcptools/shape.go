package mcptools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	defaultLimit = 50
	maxLimit     = 200

	// maxOutputBytes, bir arac ciktisinin ust siniri; asinca en buyuk dizi
	// kisaltilir (model baglaminin sisip tasmasini onler).
	maxOutputBytes = 64 << 10

	// maxHeaderValue, istek/yanit baslik degerlerinin kisaltma siniri.
	maxHeaderValue = 256
	// maxBodyChars, include_bodies=true iken her govdeden donen en fazla bayt.
	maxBodyBytes = 16 << 10
)

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// seg, bir yol parcasini dogrular ve kacirir. Bos kimlik acik bir hatadir.
func seg(name, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("%s zorunlu", name)
	}
	if len(v) > 200 {
		return "", fmt.Errorf("%s cok uzun", name)
	}
	return url.PathEscape(v), nil
}

// decodeObject, govdeyi bir JSON nesnesine cozer.
func decodeObject(body []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("sunucu yaniti cozulemedi: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// decodeList, govdeyi bir JSON dizisine cozer.
func decodeList(body []byte) ([]any, error) {
	var l []any
	if err := json.Unmarshal(body, &l); err != nil {
		return nil, fmt.Errorf("sunucu yaniti cozulemedi: %w", err)
	}
	if l == nil {
		l = []any{}
	}
	return l, nil
}

// pageOut, bellekteki bir listeye offset/limit uygular.
func pageOut(items []any, offset, limit int) map[string]any {
	if offset < 0 {
		offset = 0
	}
	limit = clampLimit(limit)
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return map[string]any{
		"items":    items[offset:end],
		"count":    end - offset,
		"total":    total,
		"offset":   offset,
		"has_more": end < total,
	}
}

// listOut, sunucunun zaten sayfaladigi bir listeyi sarar.
func listOut(items []any, offset, limit int, hasMore bool) map[string]any {
	return map[string]any{
		"items":    items,
		"count":    len(items),
		"offset":   offset,
		"limit":    limit,
		"has_more": hasMore,
	}
}

// field, bir nesneden dize alani okur.
func field(o any, key string) string {
	m, ok := o.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// truncStr, dizeyi UTF-8 sinirini bozmadan n bayta kisaltir.
func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...[kisaltildi]"
}

// truncHeaders, {"Baslik": ["deger", ...]} haritasinin degerlerini kisaltir.
func truncHeaders(h any) any {
	m, ok := h.(map[string]any)
	if !ok {
		return h
	}
	for k, v := range m {
		list, ok := v.([]any)
		if !ok {
			continue
		}
		for i, e := range list {
			if s, ok := e.(string); ok {
				list[i] = truncStr(s, maxHeaderValue)
			}
		}
		m[k] = list
	}
	return m
}

// capOutput, ciktinin JSON boyutunu maxOutputBytes altina indirir: en buyuk
// ust-duzey dizi, sigana kadar yariya indirilir ve "truncated" isaretlenir.
func capOutput(out map[string]any) {
	if out == nil {
		return
	}
	for i := 0; i < 32; i++ {
		raw, err := json.Marshal(out)
		if err != nil || len(raw) <= maxOutputBytes {
			return
		}
		bestKey, bestLen := "", 1
		for k, v := range out {
			if l, ok := v.([]any); ok && len(l) > bestLen {
				bestKey, bestLen = k, len(l)
			}
		}
		if bestKey == "" {
			// Kisaltilacak dizi yok: dize alanlarini kisalt.
			for k, v := range out {
				if s, ok := v.(string); ok && len(s) > 1024 {
					out[k] = truncStr(s, 1024)
				}
			}
			out["truncated"] = true
			return
		}
		l := out[bestKey].([]any)
		out[bestKey] = l[:bestLen/2]
		if c, ok := out["count"].(int); ok && bestKey == "items" {
			out["count"] = min(c, bestLen/2)
		}
		out["truncated"] = true
		out["truncated_note"] = "Cikti boyut siniri nedeniyle kisaltildi; daha fazlasi icin offset/limit veya filtre kullanin."
		if bestKey == "items" {
			out["has_more"] = true
		}
	}
}

// apiError, hata yanitini kisa bir insan-okur metne cevirir.
func apiError(r Response) error {
	code, msg := "", ""
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(r.Body, &e) == nil && len(e.Error) > 0 {
		var obj struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(e.Error, &obj) == nil {
			code, msg = obj.Code, obj.Message
		} else {
			var s string
			if json.Unmarshal(e.Error, &s) == nil {
				msg = s
			}
		}
	}
	if msg == "" {
		msg = truncStr(strings.TrimSpace(string(r.Body)), 200)
	}
	if code == "" {
		code = "http_error"
	}
	text := fmt.Sprintf("%s: %s (HTTP %d)", code, truncStr(msg, 300), r.Status)
	if ra := r.Header.Get("Retry-After"); ra != "" && r.Status == 429 {
		text += "; " + ra + " sn sonra tekrar deneyin"
	}
	return fmt.Errorf("%s", text)
}
