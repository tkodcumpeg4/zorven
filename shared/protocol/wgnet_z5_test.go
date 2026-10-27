package protocol

import (
	"encoding/json"
	"testing"
)

// Z5: eski istemci/sunucu uyumu ve yeni alanlarin yuvarlak donusu.
func TestZ5ProtocolAdditions(t *testing.T) {
	if FeatureWGALog != "wg_alog" || TypeNetAccessLog != "net_access_log" {
		t.Fatal("sabitler degismemeli (kablo uyumlulugu)")
	}
	// Eski sunucunun NetRegistered'inda alog yok: false.
	var nr NetRegistered
	if err := json.Unmarshal([]byte(`{"type":"net_registered","node_id":"n","ip":"100.64.0.2","tun":true}`), &nr); err != nil || nr.ALog {
		t.Fatalf("eski yanit: %+v %v", nr, err)
	}
	// Kayitsiz NetDNS (eski sunucu) Records tasimaz; alan omitempty.
	b, _ := json.Marshal(NetDNS{Resolver: "100.64.0.1", Zone: "zorven.internal"})
	if string(b) != `{"resolver":"100.64.0.1","zone":"zorven.internal"}` {
		t.Fatalf("NetDNS wire: %s", b)
	}
	// Yeni NetMap eski istemciye gider: bilinmeyen alanlar yok sayilir (Records).
	st := NewNetState()
	st.Apply(NetMap{DNS: &NetDNS{Resolver: "r", Zone: "z", Records: []NetDNSRecord{{Name: "db.internal", IP: "100.64.0.9"}}}})
	if st.DNS == nil || len(st.DNS.Records) != 1 {
		t.Fatal("kayitlar uygulanmali")
	}
	// Erisim gunlugu mesaji yuvarlak donus; icerik alani yok.
	in := NetAccessLog{Type: TypeNetAccessLog, WindowSec: 30, Truncated: 2, Entries: []NetAccessEntry{
		{SrcIP: "100.64.0.3", Proto: "tcp", Port: 22, Decision: AccessAllow, Rule: "pol:p:1", Flows: 1, Packets: 5, Bytes: 600},
	}}
	raw, err := Marshal(in, TypeNetAccessLog)
	if err != nil {
		t.Fatal(err)
	}
	var out NetAccessLog
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Entries) != 1 || out.Entries[0].Bytes != 600 || out.Truncated != 2 {
		t.Fatalf("yuvarlak donus: %+v %v", out, err)
	}
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	for _, e := range generic["entries"].([]any) {
		for k := range e.(map[string]any) {
			switch k {
			case "src_ip", "proto", "port", "decision", "rule", "flows", "packets", "bytes":
			default:
				t.Fatalf("beklenmeyen (icerik?) alan: %s", k)
			}
		}
	}
}
