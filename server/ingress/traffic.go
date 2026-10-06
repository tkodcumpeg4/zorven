package ingress

import (
	"encoding/json"
	"net/http"
	"strings"
)

// FAZ 6 — trafik politikası. Tünel başına istek/yanıt başlığı ekle-değiştir-sil
// ve yönlendirme (redirect) kuralları. Kurallar router snapshot'ında BİR KEZ
// parse edilir (Router.Reload); ingress hot-path'i yalnızca hazır struct'ı
// uygular — per-istek JSON parse yoktur.

// trafficPolicy, bir tünelin parse edilmiş trafik kuralları.
type trafficPolicy struct {
	RequestHeaders  headerRules    `json:"request_headers"`
	ResponseHeaders headerRules    `json:"response_headers"`
	Redirects       []redirectRule `json:"redirects"`
}

// headerRules, eklenecek/değiştirilecek (Set) ve silinecek (Remove) başlıklar.
type headerRules struct {
	Set    map[string]string `json:"set"`
	Remove []string          `json:"remove"`
}

// redirectRule, yol ön-ekine göre yönlendirme. Location boşsa kural yok sayılır.
type redirectRule struct {
	MatchPrefix string `json:"match_prefix"`
	Location    string `json:"location"`
	Status      int    `json:"status"` // 301|302|307|308; geçersizse 302
}

// parseTrafficPolicy, ham JSON'ı parse eder. Boş/anlamsızsa nil döner (kural yok).
func parseTrafficPolicy(raw []byte) *trafficPolicy {
	if len(raw) == 0 {
		return nil
	}
	var p trafficPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	if len(p.RequestHeaders.Set) == 0 && len(p.RequestHeaders.Remove) == 0 &&
		len(p.ResponseHeaders.Set) == 0 && len(p.ResponseHeaders.Remove) == 0 &&
		len(p.Redirects) == 0 {
		return nil
	}
	return &p
}

// applyToRequest, giden isteğin (backend'e iletilen) başlıklarına kuralları uygular.
func (p *trafficPolicy) applyToRequest(h http.Header) {
	if p == nil {
		return
	}
	for _, k := range p.RequestHeaders.Remove {
		h.Del(k)
	}
	for k, v := range p.RequestHeaders.Set {
		h.Set(k, v)
	}
}

// applyToResponse, dönen yanıtın başlıklarına kuralları uygular.
func (p *trafficPolicy) applyToResponse(h http.Header) {
	if p == nil {
		return
	}
	for _, k := range p.ResponseHeaders.Remove {
		h.Del(k)
	}
	for k, v := range p.ResponseHeaders.Set {
		h.Set(k, v)
	}
}

// matchRedirect, istek yolu bir redirect kuralının ön-ekiyle eşleşiyorsa
// (location, status, true) döner. En UZUN eşleşen ön-ek kazanır (özgüllük).
func (p *trafficPolicy) matchRedirect(path string) (string, int, bool) {
	if p == nil {
		return "", 0, false
	}
	bestLen := -1
	var loc string
	var status int
	for _, rr := range p.Redirects {
		if rr.Location == "" {
			continue
		}
		if rr.MatchPrefix == "" || strings.HasPrefix(path, rr.MatchPrefix) {
			if len(rr.MatchPrefix) > bestLen {
				bestLen = len(rr.MatchPrefix)
				loc = rr.Location
				status = rr.Status
			}
		}
	}
	if bestLen < 0 {
		return "", 0, false
	}
	switch status {
	case 301, 302, 307, 308:
	default:
		status = http.StatusFound // 302
	}
	return loc, status, true
}
