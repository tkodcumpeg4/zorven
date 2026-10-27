package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// W3 alanlari geriye uyumludur: eski sunucu/istemci bunlari gondermez/yok sayar.
func TestTunFieldsBackwardCompatible(t *testing.T) {
	// Eski sunucunun netmap'i: ACL/DNS yok -> durum degismez.
	st := NewNetState()
	st.ACL = []NetACLRule{{Src: []string{"100.64.1.1"}, Proto: "tcp", Ports: "22"}}
	var old NetMap
	if err := json.Unmarshal([]byte(`{"type":"net_map","epoch":3,"full":false,"self":{"node_id":"n","wg_pub":"p","ip":"100.64.1.2","name":"a","kind":"device","online":true}}`), &old); err != nil {
		t.Fatal(err)
	}
	st.Apply(old)
	if len(st.ACL) != 1 {
		t.Fatal("ACLSet olmayan delta ACL'i degistirmemeli")
	}
	// ACLSet=true ve bos liste ACL'i bosaltir.
	st.Apply(NetMap{ACLSet: true})
	if len(st.ACL) != 0 {
		t.Fatal("ACLSet + bos liste ACL'i temizlemeli")
	}
	// DNS gelince saklanir; tam netmap (Full) ACL/DNS'i sifirlar.
	st.Apply(NetMap{DNS: &NetDNS{Resolver: "100.64.0.1", Zone: "zorven.internal", Suffix: "ofis"}})
	if st.DNS == nil || st.DNS.Suffix != "ofis" {
		t.Fatalf("dns: %+v", st.DNS)
	}
	st.Apply(NetMap{Full: true})
	if st.DNS != nil || st.ACL != nil {
		t.Fatal("tam netmap Mod B bilgisini sifirlamali")
	}
	// Eski istemciye giden JSON'da yeni alanlar yer almaz (omitempty).
	b, _ := json.Marshal(NetRegistered{Type: TypeNetRegistered, NodeID: "n", IP: "100.64.1.2"})
	for _, k := range []string{`"tun"`, `"pool"`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("NetRegistered %s icermemeli: %s", k, b)
		}
	}
	b, _ = json.Marshal(NetMap{Type: TypeNetMap})
	for _, k := range []string{`"acl`, `"dns"`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("NetMap %s icermemeli: %s", k, b)
		}
	}
	// Yeni alanli kayit eski istemcide sorunsuz ayristirilir (bilinmeyen alan yok sayilir).
	var nr NetRegistered
	if err := json.Unmarshal([]byte(`{"type":"net_registered","node_id":"n","ip":"100.64.1.2","tun":true,"pool":"10.99.0.0/16","future":1}`), &nr); err != nil || !nr.Tun || nr.Pool != "10.99.0.0/16" {
		t.Fatalf("%v %+v", err, nr)
	}
	if FeatureWGTun != "wg_tun" || TypeNetMode != "net_mode" {
		t.Fatal("protokol sabitleri degismemeli")
	}
}
