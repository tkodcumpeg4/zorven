package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"
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

// ValidateTrafficConfig, trafik politikasi config'ini KAYIT zamaninda dogrular
// (A4 #12). Eskiden API yalniz "JSON nesnesi mi" bakiyordu: bos/CRLF baslik adi,
// CRLF Location ve 999/-1 durum kodu kaydediliyor, bos ad tunelin tum
// isteklerini 502 yapiyordu. Ingress yine savunmali (sanitize); bu dogrulama
// kullaniciya "kaydedildi ama calismiyor" yerine 422 gostermek icindir.
// Bos config gecerlidir.
func ValidateTrafficConfig(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var p trafficPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("config gecerli bir JSON nesnesi olmali: %v", err)
	}
	if err := p.RequestHeaders.validate("request_headers"); err != nil {
		return err
	}
	if err := p.ResponseHeaders.validate("response_headers"); err != nil {
		return err
	}
	for i, rr := range p.Redirects {
		if err := validateRedirect(rr.Location, rr.Status); err != nil {
			return fmt.Errorf("redirects[%d]: %v", i, err)
		}
	}
	return nil
}

// validate, baslik kurallarinin adlarini ve degerlerini RFC 7230'a gore denetler.
func (hr headerRules) validate(field string) error {
	for k, v := range hr.Set {
		if !httpguts.ValidHeaderFieldName(k) {
			return fmt.Errorf("%s.set: gecersiz baslik adi %q", field, k)
		}
		if !httpguts.ValidHeaderFieldValue(v) {
			return fmt.Errorf("%s.set: %q basliginin degeri gecersiz (kontrol karakteri/CRLF)", field, k)
		}
	}
	for _, k := range hr.Remove {
		if !httpguts.ValidHeaderFieldName(k) {
			return fmt.Errorf("%s.remove: gecersiz baslik adi %q", field, k)
		}
	}
	return nil
}

// validateRedirect: Location baslik degeri olarak gecerli olmali (CR/LF yok);
// durum kodu 0 (varsayilan 302) ya da 301/302/307/308. Calisma zamani diger
// kodlari zaten 302'ye indiriyordu; kayitta reddetmek yaniltici "kaydedildi"yi onler.
func validateRedirect(loc string, status int) error {
	if loc != "" && !httpguts.ValidHeaderFieldValue(loc) {
		return fmt.Errorf("location gecersiz karakter iceriyor (CR/LF/kontrol)")
	}
	switch status {
	case 0, 301, 302, 307, 308:
		return nil
	}
	return fmt.Errorf("redirect status 301, 302, 307 ya da 308 olmali: %d", status)
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
	if dropped := p.sanitize(); dropped > 0 {
		// Yalniz Reload'da (sicak yolda degil) calisir; satir sayisi tunel sayisiyla sinirli.
		slog.Default().Warn("trafik politikasinda gecersiz kurallar atlandi", "adet", dropped)
	}
	if len(p.RequestHeaders.Set) == 0 && len(p.RequestHeaders.Remove) == 0 &&
		len(p.ResponseHeaders.Set) == 0 && len(p.ResponseHeaders.Remove) == 0 &&
		len(p.Redirects) == 0 {
		return nil
	}
	return &p
}

// sanitize, gecersiz baslik adlarini/degerlerini ve Location'u atar; atilan
// kural sayisini doner.
//
// SAVUNMA (A4 #12): API isleyicisi su an bos/CRLF iceren baslik adlarini ve
// CRLF Location'u kabul ediyor. Bos ad ("") istek basligina girince ajanin
// net/http istemcisi "invalid header field name" ile isteyi reddediyor ve
// tunelin TUM istekleri 502 oluyordu; yanit tarafinda net/http bu basliklari
// zaten yazmaz ama kural yine anlamsizdir. Durum kodu matchRedirect'te
// 301/302/307/308'e sinirlanir.
func (p *trafficPolicy) sanitize() (dropped int) {
	dropped += p.RequestHeaders.sanitize()
	dropped += p.ResponseHeaders.sanitize()
	if len(p.Redirects) > 0 {
		kept := p.Redirects[:0]
		for _, rr := range p.Redirects {
			if rr.Location != "" && !httpguts.ValidHeaderFieldValue(rr.Location) {
				dropped++
				continue
			}
			kept = append(kept, rr)
		}
		p.Redirects = kept
	}
	return dropped
}

func (hr *headerRules) sanitize() (dropped int) {
	for k, v := range hr.Set {
		if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) {
			delete(hr.Set, k)
			dropped++
		}
	}
	if len(hr.Remove) > 0 {
		kept := hr.Remove[:0]
		for _, k := range hr.Remove {
			if !httpguts.ValidHeaderFieldName(k) {
				dropped++
				continue
			}
			kept = append(kept, k)
		}
		hr.Remove = kept
	}
	return dropped
}

// applyToRequest, giden isteğin (backend'e iletilen) başlıklarına kuralları uygular.
func (p *trafficPolicy) applyToRequest(h http.Header) {
	if p == nil {
		return
	}
	for _, k := range p.RequestHeaders.Remove {
		h.Del(k)
	}
	setValid(h, p.RequestHeaders.Set)
}

// setValid, kurallari uygular; gecersiz ad/degerleri ATLAR. Trafik politikasi
// parse'ta temizlenir (sanitize) ama policy motorunun set_header kurallari
// (mergePolicyHeaders) ayri yoldan gelir; bos/CRLF ad tum istekleri 502
// yapmasin diye son savunma burada.
func setValid(h http.Header, set map[string]string) {
	for k, v := range set {
		if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) {
			continue
		}
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
	setValid(h, p.ResponseHeaders.Set)
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
