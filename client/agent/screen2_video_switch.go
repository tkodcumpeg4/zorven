package agent

import (
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Karo <-> video otomatik gecisi (saf durum makinesi; zaman disaridan verilir).
//
// Olcu: bir yakalama turunda degisen alan orani (karo farkindan kirli karo
// orani; degisim yok turu 0). Kurallar (histerezis):
//   - karo modunda: oran >= videoEnterRatio olan turlar KESINTISIZ
//     videoEnterAfter suredir surerse video'ya gec. Aradaki tek bir sakin/orta
//     tur sayaci sifirlar.
//   - video modunda: oran < videoExitRatio olan turlar KESINTISIZ videoExitAfter
//     (2 sn) suredir surerse karoya don. Giris ve cikis esikleri farkli
//     (0.30 / 0.10) oldugu icin arada kalan icerik modu degistirmez.
//   - gectikten sonra en az videoMinDwell o modda kalinir (salinim korumasi).
//   - tercih tiles/video ise otomatik karar yok: hep o mod.
const (
	videoEnterRatio = 0.30
	videoExitRatio  = 0.10
	videoEnterAfter = 500 * time.Millisecond
	videoExitAfter  = 2 * time.Second
	videoMinDwell   = 1500 * time.Millisecond
)

// videoSwitch, otomatik mod karari. Goroutine-guvenli DEGIL (yalniz yakalama
// goroutine'i kullanir).
type videoSwitch struct {
	video     bool      // su an video mi
	since     time.Time // moda girildigi an
	movingFor time.Time // karo modunda: hareket serisinin basi (sifir => seri yok)
	calmFrom  time.Time // video modunda: sakin serisinin basi
}

// step, bir turun degisim oranini isler ve (yeni) modu doner (video=true).
// pref: protocol.Screen2Video{Auto,Tiles,On}. changed: bu cagrida mod degisti.
func (s *videoSwitch) step(now time.Time, ratio float64, pref string) (video, changed bool) {
	was := s.video
	switch pref {
	case protocol.Screen2VideoOn:
		s.video = true
	case protocol.Screen2VideoTiles:
		s.video = false
	default:
		s.stepAuto(now, ratio)
	}
	if s.video != was {
		s.since = now
		s.movingFor, s.calmFrom = time.Time{}, time.Time{}
	}
	return s.video, s.video != was
}

func (s *videoSwitch) stepAuto(now time.Time, ratio float64) {
	dwell := !s.since.IsZero() && now.Sub(s.since) < videoMinDwell
	if !s.video {
		if ratio < videoEnterRatio {
			s.movingFor = time.Time{}
			return
		}
		if s.movingFor.IsZero() {
			s.movingFor = now
		}
		if now.Sub(s.movingFor) >= videoEnterAfter && !dwell {
			s.video = true
		}
		return
	}
	if ratio >= videoExitRatio {
		s.calmFrom = time.Time{}
		return
	}
	if s.calmFrom.IsZero() {
		s.calmFrom = now
	}
	if now.Sub(s.calmFrom) >= videoExitAfter && !dwell {
		s.video = false
	}
}

// dirtyRatio, kirli karo bayraklarinin orani (0..1).
func dirtyRatio(dirty []bool) float64 {
	if len(dirty) == 0 {
		return 0
	}
	n := 0
	for _, d := range dirty {
		if d {
			n++
		}
	}
	return float64(n) / float64(len(dirty))
}

// --- bit hizi uyarlamasi -------------------------------------------------------

const (
	videoMinBitrate = 300_000
	videoMaxBitrate = 8_000_000
	// videoBitsPerPixel, baslangic hedefi: w*h*fps*bpp (ekran icerigi icin).
	videoBitsPerPixel = 0.07
)

// initialVideoBitrate, cozunurluk ve fps'e gore baslangic bit hizi.
func initialVideoBitrate(w, h, fps int) int {
	return clampInt(int(float64(w)*float64(h)*float64(max(fps, 1))*videoBitsPerPixel), videoMinBitrate, videoMaxBitrate)
}

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

// videoRate, sunucu geri bildiriminden (screen2_feedback) bit hizini ayarlar:
// tikanma varsa carpimsal azaltma (x0.75, en cok 500 ms'de bir), uzun sure temizse
// kucuk artis (x1.1, en cok 2 sn'de bir) tavana kadar. Goroutine-guvenli DEGIL.
type videoRate struct {
	bps, ceil int
	lastCut   time.Time
	lastUp    time.Time
}

func newVideoRate(w, h, fps int) *videoRate {
	b := initialVideoBitrate(w, h, fps)
	return &videoRate{bps: b, ceil: b}
}

// reset, cozunurluk/fps degisince baslangic hedefini yeniden hesaplar.
func (r *videoRate) reset(w, h, fps int) {
	r.bps = initialVideoBitrate(w, h, fps)
	r.ceil = r.bps
}

// observe, geri bildirim ozetini isler; bit hizi degistiyse true doner.
func (r *videoRate) observe(now time.Time, congested bool) (changed bool) {
	switch {
	case congested:
		if now.Sub(r.lastCut) < 500*time.Millisecond {
			return false
		}
		r.lastCut, r.lastUp = now, now
		nb := clampInt(r.bps*3/4, videoMinBitrate, videoMaxBitrate)
		changed = nb != r.bps
		r.bps = nb
	default:
		if now.Sub(r.lastUp) < 2*time.Second || now.Sub(r.lastCut) < 2*time.Second {
			return false
		}
		r.lastUp = now
		nb := clampInt(r.bps*11/10, videoMinBitrate, min(r.ceil, videoMaxBitrate))
		changed = nb != r.bps
		r.bps = nb
	}
	return changed
}

// feedbackCongested, geri bildirim mesajinin herhangi bir katmani tikali mi
// (bekleyen veri yasi buyuk ya da izleyici icin kare atlandi). Teslim fps'i
// bilerek olculmez: video fps'i icerigin hareketine bagli, tikanma olcusu degil.
func feedbackCongested(m protocol.Screen2Feedback) bool {
	for _, l := range m.Layers {
		if l.QueueMs >= 300 || l.Skipped > 0 {
			return true
		}
	}
	return false
}
