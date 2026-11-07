package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/api"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// agentOfflineWatchFromEnv, kopuk ajan operator alarmini ortamdan kurar:
//
//	ZORVEN_OFFLINE_ALERT_MIN  kopuk sayilma esigi, dakika (varsayilan 10; 0/off: kapali)
//	ZORVEN_OPS_ALERT_EMAIL    virgulle ayrilmis alici(lar); bossa yalniz WARN log
//
// Kume kipinde baglilik cluster_sessions'tan, standalone'da bu dugumun Hub'indan okunur.
func agentOfflineWatchFromEnv(cluster bool, hub *tunnel.Hub) *api.AgentOfflineWatch {
	after := 10 * time.Minute
	if v := strings.TrimSpace(os.Getenv("ZORVEN_OFFLINE_ALERT_MIN")); v != "" {
		if strings.EqualFold(v, "off") {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			n = 10
		}
		if n == 0 {
			return nil
		}
		after = time.Duration(n) * time.Minute
	}
	var emails []string
	for _, e := range strings.Split(os.Getenv("ZORVEN_OPS_ALERT_EMAIL"), ",") {
		if e = strings.TrimSpace(e); e != "" && strings.Contains(e, "@") {
			emails = append(emails, e)
		}
	}
	w := &api.AgentOfflineWatch{Emails: emails, After: after, Cluster: cluster}
	if hub != nil {
		w.LocalConnected = func(id string) bool { _, ok := hub.Get(id); return ok }
	}
	return w
}
