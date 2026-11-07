package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// Bu dosya localctl "status" yanitinin 0.3.0 ile gelen CANLI alanlarini
// cozer. localctl.Response bu alanlari tasidiginda (B ajaninin isi) JSON
// gidis-donusu ile buraya dolar; eski bir ajan bu alanlari gondermezse hepsi
// sifir degerde kalir ve CLI zarifce dusuk bilgiyle devam eder.
//
// Beklenen JSON alan adlari (Response ust duzeyinde):
//
//	connected bool, state string, last_error string, server string,
//	started_at string (RFC3339), reconnects int,
//	disconnects [{at, reason}],
//	tunnels [{id, name, url, target, protocol, requests, bytes_in,
//	          bytes_out, errors, p50_ms, p95_ms}]
//
// "reload" islemi: Request{Op:"reload"}; ajan ayar dosyasini yeniden okur,
// izin tavanlarini canli uygular, sunucu/token degistiyse yeniden baglanir.

type liveTunnel struct {
	ID       string  `json:"id,omitempty"`
	Name     string  `json:"name,omitempty"`
	URL      string  `json:"url,omitempty"`
	Target   string  `json:"target,omitempty"`
	Protocol string  `json:"protocol,omitempty"`
	Requests int64   `json:"requests"`
	BytesIn  int64   `json:"bytes_in"`
	BytesOut int64   `json:"bytes_out"`
	Errors   int64   `json:"errors"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

type liveDisconnect struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
}

type liveStatus struct {
	Connected   bool             `json:"connected"`
	State       string           `json:"state,omitempty"`
	LastError   string           `json:"last_error,omitempty"`
	Server      string           `json:"server,omitempty"`
	StartedAt   string           `json:"started_at,omitempty"`
	Reconnects  int              `json:"reconnects"`
	Disconnects []liveDisconnect `json:"disconnects,omitempty"`
	Tunnels     []liveTunnel     `json:"tunnels,omitempty"`
	NodePinned  bool             `json:"node_pinned,omitempty"`
	Nodes       []liveNode       `json:"nodes,omitempty"`
}

// liveNode, sunucu dugum listesi satiri (Faz 0C-4).
type liveNode struct {
	ID       string  `json:"id"`
	Address  string  `json:"address"`
	Region   string  `json:"region,omitempty"`
	Priority int     `json:"priority"`
	Healthy  bool    `json:"healthy"`
	Current  bool    `json:"current"`
	RTTMs    float64 `json:"rtt_ms,omitempty"`
}

// printNodes, dugum listesini yazar (liste yoksa hicbir sey yazmaz; sabit
// sunucuda tek satir not dusulur).
func printNodes(l liveStatus, row func(label, value string)) {
	if l.NodePinned {
		row(i18n.T("nodes.lbl.node"), i18n.T("nodes.pinned"))
		return
	}
	if len(l.Nodes) == 0 {
		return
	}
	fmt.Println(i18n.T("nodes.lbl.list"))
	for _, n := range l.Nodes {
		mark := " "
		if n.Current {
			mark = "*"
		}
		h := i18n.T("nodes.unhealthy")
		if n.Healthy {
			h = i18n.T("nodes.healthy")
		}
		if n.RTTMs > 0 {
			h += fmt.Sprintf(" %.0f ms", n.RTTMs)
		}
		fmt.Println("  " + i18n.T("nodes.row", mark, n.Address, dash(n.Region), n.Priority, h))
	}
}

// currentNode, bagli dugumun kimligini doner (bilinmiyorsa bos).
func (l liveStatus) currentNode() string {
	for _, n := range l.Nodes {
		if n.Current {
			return n.ID
		}
	}
	return ""
}

// known: ajan canli alanlari gonderdi mi (eski ajanlarda false).
func (l liveStatus) known() bool {
	return l.State != "" || l.Connected || l.Server != "" || len(l.Tunnels) > 0
}

// uptime, started_at bilinirse calisma suresini doner.
func (l liveStatus) uptime() (time.Duration, bool) {
	if l.StartedAt == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, l.StartedAt)
	if err != nil || t.IsZero() {
		return 0, false
	}
	return time.Since(t).Round(time.Second), true
}

// decodeLive, Response'u gidis-donus ile liveStatus'a cevirir.
func decodeLive(r *localctl.Response) liveStatus {
	var l liveStatus
	if r == nil {
		return l
	}
	b, err := json.Marshal(r)
	if err != nil {
		return l
	}
	_ = json.Unmarshal(b, &l)
	return l
}
