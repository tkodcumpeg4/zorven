package visitorauth

import (
	"encoding/json"
	"time"
)

// Ziyaretci oturumu (OAuth _zva_session ve Basic _zvb_session) suresi tunel
// basina ayarlanir: access_config icinde session_ttl_minutes ve session_sliding.
//
// Cerez host-kapsamli kalir (Domain yok). Imzali cerez durumsuzdur; sure
// politikasi her istekte TUNELIN GUNCEL ayarina gore degerlendirilir, boylece
// TTL kisaltilinca eski uzun omurlu cerezler de kisalir.

const (
	// DefaultSessionTTL, ayar yokken ve IatMs'i olmayan eski cerezler icin sure.
	DefaultSessionTTL = 12 * time.Hour
	MinSessionMinutes = 15
	MaxSessionMinutes = 30 * 24 * 60
	// MaxAbsoluteLifetime, kayan yenilemede girisin ustundeki mutlak tavan
	// (TTL bundan buyukse TTL tavandir: tek bir sabit pencere hicbir zaman kisaltilmaz).
	MaxAbsoluteLifetime = 7 * 24 * time.Hour
)

// SessionPolicy, bir tunelin ziyaretci oturumu ayarlari.
type SessionPolicy struct {
	TTLMinutes int  // 0 = varsayilan (12 saat)
	Sliding    bool // kayan yenileme
}

// ValidSessionMinutes, 0 (varsayilan) veya [15 dk, 30 gun] araligi mi.
func ValidSessionMinutes(m int) bool {
	return m == 0 || (m >= MinSessionMinutes && m <= MaxSessionMinutes)
}

// PolicyFromConfig, access_config JSON'undan politikayi okur. Gecersiz/aralik
// disi deger varsayilana (0) duser (fail-safe: asla belirsiz uzun oturum yok).
func PolicyFromConfig(raw []byte) SessionPolicy {
	var c struct {
		TTL     int  `json:"session_ttl_minutes"`
		Sliding bool `json:"session_sliding"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &c)
	}
	if !ValidSessionMinutes(c.TTL) {
		c.TTL = 0
	}
	return SessionPolicy{TTLMinutes: c.TTL, Sliding: c.Sliding}
}

// TTL, etkin oturum suresi.
func (p SessionPolicy) TTL() time.Duration {
	if p.TTLMinutes <= 0 {
		return DefaultSessionTTL
	}
	return time.Duration(p.TTLMinutes) * time.Minute
}

// AbsoluteCap, ilk girisin ustundeki mutlak tavan: max(7 gun, TTL).
func (p SessionPolicy) AbsoluteCap() time.Duration {
	if t := p.TTL(); t > MaxAbsoluteLifetime {
		return t
	}
	return MaxAbsoluteLifetime
}

// Renewal, kayan yenileme ile yeniden imzalanacak cerezin yeni zamanlari.
type Renewal struct {
	Iat  time.Time // yeni verilis (iptal karsilastirmasi bunu kullanir)
	Exp  time.Time
	Orig time.Time // ilk giris (degismez)
}

// SessionState, gecerli bir cerezin degerlendirme sonucu.
type SessionState struct {
	Issued  time.Time
	Renewal *Renewal // nil = yenileme gerekmiyor
}

// Evaluate, cerez zaman alanlarini (exp unix sn; iatMs/origMs unix ms, 0=yok)
// politikaya gore degerlendirir.
//
//   - iatMs yoksa (eski cerez): verilis = exp - 12 saat, omur = min(12 saat, TTL).
//   - iatMs varsa: omur = tunelin GUNCEL TTL'i (kisaltma eski cerezlere de uygulanir).
//   - Orig + AbsoluteCap asildiysa gecersiz (kayan yenileme sonsuz uzatamaz).
//   - Kayan yenileme aciksa ve kalan sure TTL/2'den azsa yeni zamanlar doner.
func (p SessionPolicy) Evaluate(now time.Time, exp, iatMs, origMs int64) (SessionState, bool) {
	ttl := p.TTL()
	life := ttl
	var issued time.Time
	if iatMs > 0 {
		issued = time.UnixMilli(iatMs)
	} else {
		issued = time.Unix(exp, 0).Add(-DefaultSessionTTL)
		if DefaultSessionTTL < life {
			life = DefaultSessionTTL
		}
	}
	end := issued.Add(life)
	if e := time.Unix(exp, 0); e.Before(end) {
		end = e
	}
	if !now.Before(end) {
		return SessionState{}, false
	}
	orig := issued
	if origMs > 0 {
		orig = time.UnixMilli(origMs)
	}
	hardEnd := orig.Add(p.AbsoluteCap())
	if !now.Before(hardEnd) {
		return SessionState{}, false
	}
	st := SessionState{Issued: issued}
	if p.Sliding && end.Sub(now) < ttl/2 {
		newExp := now.Add(ttl)
		if newExp.After(hardEnd) {
			newExp = hardEnd
		}
		if newExp.After(end) {
			st.Renewal = &Renewal{Iat: now, Exp: newExp, Orig: orig}
		}
	}
	return st, true
}
