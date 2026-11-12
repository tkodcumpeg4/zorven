package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

// Kopuk ajan operator alarmi (2026-11-07 olayi, docs/incidents/2026-11-07-ajan-dugum-ipucu.md).
//
// Daha once bagli olup `After` suredir gorulmeyen ajanlar her turda toplu degerlendirilir.
// Her kiracinin owner'larina kendi ajanlari icin kopus basina bir e-posta gider
// (agent_offline_owner.go). Ayrica yeni kopanlar ve geri donenler TEK bir e-postayla
// platform operatorune (ZORVEN_OPS_ALERT_EMAIL, istege bagli) bildirilir ve her durumda
// WARN olarak loglanir. Durum bellektedir: sunucu yeniden
// baslarsa hala kopuk olanlar bir kez daha bildirilir (bilincli; kayit tutmaya degmez).

type offlineLister interface {
	ListRecentlyOfflineClients(ctx context.Context, within, olderThan time.Duration) ([]pgstore.OfflineClient, error)
}

// AgentOfflineWatch ayarlari.
type AgentOfflineWatch struct {
	Emails []string      // bos: yalniz log
	After  time.Duration // bu kadar suredir gorulmeyen ajan kopuk sayilir (<=0: 10 dk)
	Within time.Duration // bundan eski gorulenler (uzun suredir kapali) dahil edilmez (<=0: 24 sa)
	// Cluster: true ise baglilik cluster_sessions'tan (HasSession) okunur; false ise
	// (standalone) LocalConnected ile bellekteki Hub'dan.
	Cluster        bool
	LocalConnected func(clientID string) bool

	mu      sync.Mutex
	offline map[string]pgstore.OfflineClient // ajan kimligi -> son bildirilen kayit
}

// connected, adayin su an bagli olup olmadigini soyler.
func (w *AgentOfflineWatch) connected(c pgstore.OfflineClient) bool {
	if w.Cluster {
		return c.HasSession
	}
	return w.LocalConnected != nil && w.LocalConnected(c.ID)
}

// offlineDiff, onceki ve simdiki kopuk kumelerinden yeni kopanlari ve geri donenleri cikarir.
func offlineDiff(prev map[string]pgstore.OfflineClient, cur []pgstore.OfflineClient) (down, up []pgstore.OfflineClient, next map[string]pgstore.OfflineClient) {
	next = make(map[string]pgstore.OfflineClient, len(cur))
	for _, c := range cur {
		next[c.ID] = c
		if _, ok := prev[c.ID]; !ok {
			down = append(down, c)
		}
	}
	for id, c := range prev {
		if _, ok := next[id]; !ok {
			up = append(up, c)
		}
	}
	sort.Slice(up, func(i, j int) bool { return up[i].ID < up[j].ID })
	return down, up, next
}

// RunAgentOfflineWatch, periyodik degerlendiriciyi calistirir (kume genelinde tek dugumde: JobGate).
func (s *Server) RunAgentOfflineWatch(ctx context.Context, w *AgentOfflineWatch, interval time.Duration) {
	if w == nil {
		return
	}
	lister, ok := s.Store.(offlineLister)
	if !ok {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.evaluateAgentOffline(ctx, lister, w)
		}
	}
}

func (s *Server) evaluateAgentOffline(ctx context.Context, lister offlineLister, w *AgentOfflineWatch) {
	if s.JobGate != nil && !s.JobGate() {
		return
	}
	after, within := w.After, w.Within
	if after <= 0 {
		after = 10 * time.Minute
	}
	if within <= 0 {
		within = 24 * time.Hour
	}
	cands, err := lister.ListRecentlyOfflineClients(ctx, within, after)
	if err != nil {
		s.logger().Warn("kopuk ajan alarmi: liste alinamadi", "hata", err)
		return
	}
	var cur []pgstore.OfflineClient
	for _, c := range cands {
		if !w.connected(c) {
			cur = append(cur, c)
		}
	}
	// Kiraci owner'larina: kopus basina bir kez (kalici isaret; yeniden baslatmada tekrar gitmez).
	s.notifyTenantOwnersOffline(ctx, cur, after)

	w.mu.Lock()
	down, up, next := offlineDiff(w.offline, cur)
	w.offline = next
	w.mu.Unlock()
	if len(down) == 0 && len(up) == 0 {
		return
	}
	for _, c := range down {
		s.logger().Warn("ajan kopuk", "client", c.ID, "tenant", c.TenantSlug, "surum", c.Version,
			"son_gorulme", c.LastSeenAt.UTC().Format(time.RFC3339))
	}
	for _, c := range up {
		s.logger().Info("ajan geri baglandi", "client", c.ID, "tenant", c.TenantSlug)
	}
	s.sendAgentOfflineEmail(w.Emails, down, up, len(next), after)
}

// agentOfflineMail, operator e-postasinin konu ve govdesini uretir (operator ici, Turkce).
func agentOfflineMail(down, up []pgstore.OfflineClient, stillOffline int, after time.Duration, panel string) (string, string) {
	subject := fmt.Sprintf("[Zorven] %d ajan koptu, %d geri dondu (toplam kopuk: %d)", len(down), len(up), stillOffline)
	var b strings.Builder
	if len(down) > 0 {
		fmt.Fprintf(&b, "Yeni kopan ajanlar (%s'den uzun suredir gorulmuyor):\n", after.Round(time.Minute))
		for _, c := range down {
			fmt.Fprintf(&b, "- %s  kiraci=%s  surum=%s  son gorulme=%s UTC  (%s)\n",
				c.Name, c.TenantSlug, c.Version, c.LastSeenAt.UTC().Format("2006-01-02 15:04"), c.ID)
		}
		b.WriteString("\n")
	}
	if len(up) > 0 {
		b.WriteString("Geri donen ajanlar:\n")
		for _, c := range up {
			fmt.Fprintf(&b, "- %s  kiraci=%s  (%s)\n", c.Name, c.TenantSlug, c.ID)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Su an kopuk (son 24 saatte gorulmus): %d\n", stillOffline)
	b.WriteString("Toplu bir kopus genelde sunucu yeniden baslatmasindan ya da istemci surumune ozgu bir hatadan gelir;\n")
	b.WriteString("bkz. docs/incidents/2026-11-07-ajan-dugum-ipucu.md ve scripts/agent-sessions.sh.\n")
	if panel != "" {
		fmt.Fprintf(&b, "\nPanel: https://%s\n", panel)
	}
	return subject, b.String()
}

func (s *Server) sendAgentOfflineEmail(to []string, down, up []pgstore.OfflineClient, stillOffline int, after time.Duration) {
	if s.MailSender == nil || s.MailDomain == "" || len(to) == 0 {
		return
	}
	subject, body := agentOfflineMail(down, up, stillOffline, after, "panel."+s.platformDomainOr("zorven.app"))
	from := "alerts@" + s.MailDomain
	for _, addr := range to {
		if _, err := s.MailSender.Send(from, addr, subject, body, "", "", nil); err != nil {
			s.logger().Warn("kopuk ajan alarmi e-postasi gonderilemedi", "to", addr, "hata", err)
		}
	}
}
