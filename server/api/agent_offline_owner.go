package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

// Kopuk ajan bildirimi: kiracinin owner'larina, yalniz KENDI ajanlari icin, kopus basina
// bir kez (clients.offline_notified_at; ajan yeniden baglaninca temizlenir, sunucu
// yeniden baslasa da tekrar gitmez). Dil: owner'in kayitli tercihi, yoksa tr.

type ownerNotifier interface {
	TenantOwners(ctx context.Context, tenantID string) ([]pgstore.TenantOwner, error)
	MarkClientsOfflineNotified(ctx context.Context, ids []string) error
}

func (s *Server) notifyTenantOwnersOffline(ctx context.Context, offline []pgstore.OfflineClient, after time.Duration) {
	n, ok := s.Store.(ownerNotifier)
	if !ok || s.MailSender == nil || s.MailDomain == "" {
		return
	}
	order, byTenant := groupUnnotifiedByTenant(offline)
	from := "alerts@" + s.MailDomain
	domain := s.platformDomainOr("zorven.app")
	for _, tid := range order {
		clients := byTenant[tid]
		owners, err := n.TenantOwners(ctx, tid)
		if err != nil {
			s.logger().Warn("kopuk ajan bildirimi: owner'lar okunamadi", "tenant", tid, "hata", err)
			continue
		}
		sent := 0
		for _, o := range owners {
			subject, body := agentOfflineOwnerMail(s.ownerMailLang(ctx, o.UserID), clients, after, domain)
			if _, err := s.MailSender.Send(from, o.Email, subject, body, "", "", nil); err != nil {
				s.logger().Warn("kopuk ajan bildirimi gonderilemedi", "tenant", tid, "hata", err)
				continue
			}
			sent++
		}
		// Owner yoksa da isaretlenir (her dakika yeniden denenmesin); gonderim tamamen
		// basarisizsa isaretlenmez, bir sonraki turda yeniden denenir.
		if sent == 0 && len(owners) > 0 {
			continue
		}
		ids := make([]string, 0, len(clients))
		for _, c := range clients {
			ids = append(ids, c.ID)
		}
		if err := n.MarkClientsOfflineNotified(ctx, ids); err != nil {
			s.logger().Warn("kopuk ajan bildirimi isaretlenemedi", "tenant", tid, "hata", err)
		}
		s.logger().Info("kopuk ajan bildirimi owner'lara gonderildi", "tenant", tid, "ajan", len(clients), "alici", sent)
	}
}

// ownerMailLang, owner'in kayitli dil tercihi (duyuru tercihi); yoksa tr.
func (s *Server) ownerMailLang(ctx context.Context, userID string) string {
	if us, ok := s.Store.(store.UpdatesStore); ok && userID != "" {
		if pref, err := us.GetAnnouncementPref(ctx, userID); err == nil {
			if l := normMailLang(pref.Lang); l != "" {
				return l
			}
		}
	}
	return "tr"
}

type agentOfflineStrings struct {
	SubjectOne  string // %[1]s ajan adi
	SubjectMany string // %[1]d ajan sayisi
	Intro       string // %[1]d dakika
	Item        string // %[1]s ad, %[2]s surum, %[3]s son gorulme
	Steps       string // %[1]s platform alan adi
}

const offlineRecovery = "   Linux (root): rm -f /etc/zorven/nodes.json /root/.config/zorven/nodes.json && zorven service restart && zorven update\r\n" +
	"   Windows (admin): Remove-Item -Force C:\\ProgramData\\zorven\\nodes.json -ErrorAction SilentlyContinue; zorven service restart; zorven update\r\n"

var agentOfflineTexts = map[string]agentOfflineStrings{
	"tr": {
		SubjectOne:  "[Zorven] Ajan bağlantısı koptu: %[1]s",
		SubjectMany: "[Zorven] %[1]d ajanın bağlantısı koptu",
		Intro:       "Aşağıdaki Zorven ajanı %[1]d dakikadan uzun süredir sunucuya bağlı değil. Bu ajan üzerinden yayınlanan tüneller şu an erişilemez.\r\n\r\n",
		Item:        "- %[1]s (sürüm %[2]s, son görülme %[3]s UTC)\r\n",
		Steps: "\r\nNe yapmalı:\r\n" +
			"1. Ajanın çalıştığı makinenin açık ve internete bağlı olduğunu kontrol edin.\r\n" +
			"2. O makinede `zorven status` komutunu çalıştırın.\r\n" +
			"3. Günlükte \"doesn't contain any IP SANs\" görüyorsanız (istemci 0.3.4-0.3.6), bir kez şunu çalıştırın:\r\n" +
			offlineRecovery +
			"\r\nAjan yeniden bağlandığında ayrıca bildirim gönderilmez; tekrar koparsa yeniden haber veririz.\r\n" +
			"Panel: https://panel.%[1]s/clients\r\n\r\n" +
			"Bu e-postayı kuruluşunuzun sahibi (owner) olduğunuz için aldınız.\r\n",
	},
	"en": {
		SubjectOne:  "[Zorven] Agent disconnected: %[1]s",
		SubjectMany: "[Zorven] %[1]d agents disconnected",
		Intro:       "The Zorven agent below has not been connected to the server for more than %[1]d minutes. Tunnels published through it are currently unreachable.\r\n\r\n",
		Item:        "- %[1]s (version %[2]s, last seen %[3]s UTC)\r\n",
		Steps: "\r\nWhat to do:\r\n" +
			"1. Check that the machine running the agent is on and connected to the internet.\r\n" +
			"2. Run `zorven status` on that machine.\r\n" +
			"3. If the log shows \"doesn't contain any IP SANs\" (client 0.3.4-0.3.6), run this once:\r\n" +
			offlineRecovery +
			"\r\nNo separate notice is sent when the agent reconnects; we will tell you again if it disconnects again.\r\n" +
			"Dashboard: https://panel.%[1]s/clients\r\n\r\n" +
			"You received this email because you are an owner of your organization.\r\n",
	},
}

// agentOfflineOwnerMail, kiraci owner'ina giden kopuk ajan e-postasini uretir.
func agentOfflineOwnerMail(lang string, clients []pgstore.OfflineClient, after time.Duration, domain string) (subject, body string) {
	t := agentOfflineTexts[mailLangOrTR(lang)]
	if len(clients) == 1 {
		subject = fmt.Sprintf(t.SubjectOne, clients[0].Name)
	} else {
		subject = fmt.Sprintf(t.SubjectMany, len(clients))
	}
	var b strings.Builder
	fmt.Fprintf(&b, t.Intro, int(after.Round(time.Minute)/time.Minute))
	for _, c := range clients {
		v := c.Version
		if v == "" {
			v = "?"
		}
		fmt.Fprintf(&b, t.Item, c.Name, v, c.LastSeenAt.UTC().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(&b, t.Steps, domain)
	return subject, b.String()
}

// groupUnnotifiedByTenant, henuz bildirilmemis kopuk ajanlari kiraciya gore gruplar
// (kiraci sirasi ilk gorulme sirasidir).
func groupUnnotifiedByTenant(offline []pgstore.OfflineClient) ([]string, map[string][]pgstore.OfflineClient) {
	byTenant := map[string][]pgstore.OfflineClient{}
	var order []string
	for _, c := range offline {
		if c.OfflineNotified {
			continue
		}
		if _, seen := byTenant[c.TenantID]; !seen {
			order = append(order, c.TenantID)
		}
		byTenant[c.TenantID] = append(byTenant[c.TenantID], c)
	}
	return order, byTenant
}
