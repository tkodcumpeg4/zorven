package localctl

import "context"

// 0.3.0 gozlemlenebilirlik + yeniden yukleme sozlesmesi.
//
// "status" islemi, mevcut alanlara (caps, pid, version, service) EK olarak
// baglanti durumunu, yeniden baglanma sayacini, son kopus nedenlerini ve tunel
// basina olcumleri doner. "reload" ajanin baslatildigi ayar dosyasini yeniden
// okur ve izinleri/sunucu/token'i canli uygular.

// DisconnectInfo, tek bir baglanti kopusu kaydi.
type DisconnectInfo struct {
	At     string `json:"at"` // RFC3339
	Reason string `json:"reason"`
}

// TunnelInfo, bir tunelin ozeti ve olcumleri.
type TunnelInfo struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Target   string  `json:"target"`
	Protocol string  `json:"protocol"`
	Requests int64   `json:"requests"`
	BytesIn  int64   `json:"bytes_in"`  // sunucudan yerel servise
	BytesOut int64   `json:"bytes_out"` // yerel servisten sunucuya
	Errors   int64   `json:"errors"`
	P50MS    float64 `json:"p50_ms"`
	P95MS    float64 `json:"p95_ms"`
}

// NodeInfo, sunucu dugum listesindeki bir satir (Faz 0C-4).
type NodeInfo struct {
	ID       string  `json:"id"`
	Address  string  `json:"address"`
	Region   string  `json:"region,omitempty"`
	Priority int     `json:"priority"`
	Healthy  bool    `json:"healthy"`
	Current  bool    `json:"current"`
	RTTMs    float64 `json:"rtt_ms,omitempty"`
}

// StatusInfo, "status" yanitinin gozlemlenebilirlik kismi.
type StatusInfo struct {
	Connected   bool
	State       string
	LastError   string
	Server      string
	StartedAt   string // RFC3339
	Reconnects  int
	Disconnects []DisconnectInfo // en son 10, eskiden yeniye
	Tunnels     []TunnelInfo
	// NodePinned: sunucu sabit, dugum listesi kullanilmiyor. Nodes bos olabilir.
	NodePinned bool
	Nodes      []NodeInfo
	// Deploy: Zorven Apps izni ajan baslarken acikti.
	Deploy bool
}

// StatusReporter, Controller'in istege bagli uzantisi: "status" yanitini
// baglanti durumuyla zenginlestirir. Uygulamayan denetleyicide alanlar bos kalir.
type StatusReporter interface {
	StatusInfo() StatusInfo
}

// ReloadInfo, "reload" sonucu.
type ReloadInfo struct {
	// Changed, uygulanan degisikliklerin kisa adlari (or. "terminal", "screen",
	// "deploy", "server", "token").
	Changed []string `json:"changed"`
	// Reconnecting: sunucu adresi veya token degistigi icin yeniden baglaniliyor.
	Reconnecting bool `json:"reconnecting"`
}

// Reloader, Controller'in istege bagli uzantisi: "reload" islemi.
type Reloader interface {
	Reload(ctx context.Context) (ReloadInfo, error)
}

func (r *Response) fillStatus(s StatusInfo) {
	r.Connected = s.Connected
	r.State = s.State
	r.LastError = s.LastError
	r.Server = s.Server
	r.StartedAt = s.StartedAt
	r.Reconnects = s.Reconnects
	r.Disconnects = s.Disconnects
	r.Tunnels = s.Tunnels
	r.NodePinned = s.NodePinned
	r.Deploy = s.Deploy
	r.Nodes = s.Nodes
}
