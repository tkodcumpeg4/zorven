package protocol

// UDP tunel olcumu: sunucu <-> istemci gecikme ve kayip yoklamasi.

// FeatureUDPProbe, istemcinin udp_probe mesajlarini yankiladigini ve periyodik
// udp_agent_stats gonderdigini bildirir. Eski istemci/sunucuda pazarlanmaz ve
// olcum yapilmaz (panelde "istemci surumu desteklemiyor" gorunur).
const FeatureUDPProbe = "udp_probe"

// UDPProbe, sunucunun UDP tunel yoklamasi. Istemci ayni icerikle UDPProbeEcho
// doner; TS sunucu saatindeki gonderim ani (unix ns) oldugundan RTT yalnizca
// sunucu saatiyle hesaplanir, istemci saati kullanilmaz.
type UDPProbe struct {
	Type     MessageType `json:"type"`
	TunnelID string      `json:"tunnel_id"`
	Seq      uint64      `json:"seq"`
	TS       int64       `json:"ts"`
}

// UDPProbeEcho, UDPProbe'un aynen yankisi.
type UDPProbeEcho struct {
	Type     MessageType `json:"type"`
	TunnelID string      `json:"tunnel_id"`
	Seq      uint64      `json:"seq"`
	TS       int64       `json:"ts"`
}

// UDPAgentStats, istemcinin son bildirimden bu yana UDP tunel basina dusurdugu
// datagram sayisi (fark, birikimli degil). Yalnizca sifirdan buyukse gonderilir.
type UDPAgentStats struct {
	Type    MessageType      `json:"type"`
	Dropped map[string]int64 `json:"dropped"` // tunnel_id -> adet
}
