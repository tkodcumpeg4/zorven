package agent

import (
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// inputResult, tek bir girdi olayinin uygulanma sonucu.
type inputResult struct {
	State  string // protocol.InputState*
	Code   int    // platform hata kodu (Windows GetLastError); 0 => yok
	Detail string
}

// merge, iki sonucun KOTU olanini doner (bir olay birden cok SendInput'a
// donusebilir; biri basarisizsa olay basarisiz sayilir).
func (r inputResult) merge(o inputResult) inputResult {
	if r.State == protocol.InputStateOK || r.State == "" {
		if o.State == "" {
			return r
		}
		return o
	}
	return r
}

// inputStatusRepeat, basarisizlik suruyorken ayni durumun yeniden bildirilme
// en kisa araligi.
const inputStatusRepeat = 2 * time.Second

// inputStatusGate, girdi durumu bildirimini kisar: her fare olayi icin mesaj
// gondermek tunel kontrol kanalini bogardi.
//
// Kural: oturumun ilk girdisinde bildir; sonra yalnizca durum degisince; durum
// basarisizsa (ok degil) ayrica en fazla inputStatusRepeat aralikla tekrarla
// (panel acilisinda mesaji kacirmis olabilir). Tek goroutine kullanir.
type inputStatusGate struct {
	started bool
	last    string
	lastAt  time.Time
}

// Should, bu sonucun bildirilmesi gerekip gerekmedigini soyler.
func (g *inputStatusGate) Should(res inputResult, now time.Time) bool {
	if res.State == "" {
		return false
	}
	if !g.started || res.State != g.last {
		g.started, g.last, g.lastAt = true, res.State, now
		return true
	}
	if res.State != protocol.InputStateOK && now.Sub(g.lastAt) >= inputStatusRepeat {
		g.lastAt = now
		return true
	}
	return false
}
