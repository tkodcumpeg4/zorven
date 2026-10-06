package api

import (
	"context"
	"fmt"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.4 — metrik uyarı değerlendiricisi.
//
// RunAlertEvaluator, periyodik olarak etkin uyarıları değerlendirir. Her uyarı
// için son window_min dakikadaki 5xx oranını hesaplar; eşik aşıldıysa (ve en az
// min_requests istek varsa) durumu 'firing'e çeker ve bir kez e-posta gönderir.
// Oran düşünce 'ok'a döner. 'firing' iken tekrar bildirmez (spam önleme).
//
// main.go bunu bir goroutine olarak başlatır; MailSender nil ise e-posta atılmaz
// ama durum yine de izlenir (panelde rozet olarak görünür).
func (s *Server) RunAlertEvaluator(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluateAlerts(ctx)
		}
	}
}

func (s *Server) evaluateAlerts(ctx context.Context) {
	alerts, err := s.Store.ListEnabledAlerts(ctx)
	if err != nil {
		s.logger().Warn("uyari degerlendirici: etkin uyarilar alinamadi", "hata", err)
		return
	}
	now := time.Now()
	for _, a := range alerts {
		since := now.Add(-time.Duration(a.WindowMin) * time.Minute)
		total, errors, err := s.Store.TunnelRequestStats(ctx, a.TenantID, a.TunnelID, since)
		if err != nil {
			s.logger().Warn("uyari degerlendirici: istatistik alinamadi", "tunnel", a.TunnelID, "hata", err)
			continue
		}

		breach := false
		if total >= int64(a.MinRequests) && total > 0 {
			rate := float64(errors) / float64(total) * 100
			breach = rate >= float64(a.ErrorRatePct)
		}

		rate := 0.0
		if total > 0 {
			rate = float64(errors) / float64(total) * 100
		}

		switch {
		case breach && a.State != "firing":
			notified := s.sendAlertEmail(a, total, errors, rate)
			if err := s.Store.UpdateAlertState(ctx, a.TunnelID, "firing", notified); err != nil {
				s.logger().Warn("uyari durumu guncellenemedi", "tunnel", a.TunnelID, "hata", err)
			} else {
				s.logger().Warn("metrik uyarisi TETIKLENDI", "tunnel", a.TunnelID,
					"total", total, "errors", errors, "esik", a.ErrorRatePct, "email_gonderildi", notified)
			}
		case !breach && a.State == "firing":
			if err := s.Store.UpdateAlertState(ctx, a.TunnelID, "ok", false); err != nil {
				s.logger().Warn("uyari durumu guncellenemedi", "tunnel", a.TunnelID, "hata", err)
			} else {
				s.logger().Info("metrik uyarisi COZULDU", "tunnel", a.TunnelID)
			}
		}
	}
}

// sendAlertEmail, uyarı e-postasını gönderir; başarılıysa true döner. Mail altyapısı
// veya alıcı yoksa false (sessizce atlar — durum yine de 'firing' olur).
func (s *Server) sendAlertEmail(a store.TunnelAlert, total, errors int64, rate float64) bool {
	if s.MailSender == nil || a.NotifyEmail == "" || s.MailDomain == "" {
		return false
	}
	from := "alerts@" + s.MailDomain
	subject := fmt.Sprintf("[Zorven] Uyarı: %s hata oranı %%%.0f", a.TunnelID, rate)
	body := fmt.Sprintf(
		"Zorven metrik uyarısı tetiklendi.\r\n\r\n"+
			"Tünel: %s\r\n"+
			"Son %d dakika: %d istek, %d hata (5xx)\r\n"+
			"Hata oranı: %%%.1f (eşik: %%%d)\r\n\r\n"+
			"Panelden inceleyin: https://panel.%s\r\n",
		a.TunnelID, a.WindowMin, total, errors, rate, a.ErrorRatePct, s.platformDomainOr("zorven.app"))
	if _, err := s.MailSender.Send(from, a.NotifyEmail, subject, body, "", "", nil); err != nil {
		s.logger().Warn("uyari e-postasi gonderilemedi", "tunnel", a.TunnelID, "to", a.NotifyEmail, "hata", err)
		return false
	}
	return true
}

// platformDomainOr, PlatformDomain bos ise verilen varsayilani doner.
func (s *Server) platformDomainOr(def string) string {
	if s.PlatformDomain != "" {
		return s.PlatformDomain
	}
	return def
}
