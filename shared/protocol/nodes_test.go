package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Dugum listesi alanlari geriye uyumlu olmali: eski sunucunun HelloAck'i
// (alansiz) yeni yapiya, yeni HelloAck eski bir yapiya sorunsuz ayrisir.
func TestHelloAckNodesBackwardCompat(t *testing.T) {
	old := []byte(`{"type":"hello_ack","client_id":"c","session_id":"s","heartbeat_interval_s":20,"tunnels":[]}`)
	var ack HelloAck
	if err := json.Unmarshal(old, &ack); err != nil {
		t.Fatal(err)
	}
	if len(ack.Nodes) != 0 || ack.AssignedNode != "" || ack.NodeID != "" {
		t.Fatalf("eski ack'te dugum alani olmamali: %+v", ack)
	}

	// Alansiz ack JSON'unda dugum anahtarlari uretilmemeli (omitempty).
	b, _ := json.Marshal(HelloAck{Type: TypeHelloAck})
	if strings.Contains(string(b), "nodes") || strings.Contains(string(b), "assigned_node") || strings.Contains(string(b), "node_id") {
		t.Fatalf("bos alanlar yazilmamali: %s", b)
	}

	// Yeni ack, eski ajanin bildigi alt kumeye ayrisir.
	newer, _ := json.Marshal(HelloAck{Type: TypeHelloAck, ClientID: "c", NodeID: "n1", AssignedNode: "n1",
		Nodes: []NodeHint{{NodeID: "n1", Address: "a:443", Healthy: true}}})
	var legacy struct {
		Type     MessageType `json:"type"`
		ClientID string      `json:"client_id"`
	}
	if err := json.Unmarshal(newer, &legacy); err != nil || legacy.ClientID != "c" {
		t.Fatalf("eski ajan ayristiramadi: %v %+v", err, legacy)
	}
	var back HelloAck
	if err := json.Unmarshal(newer, &back); err != nil || len(back.Nodes) != 1 || !back.Nodes[0].Healthy {
		t.Fatalf("gidis-donus: %v %+v", err, back)
	}
}

// Hello.PrevNode bos ise yazilmaz (eski sunucu icin ek alan yok).
func TestHelloPrevNodeOmitEmpty(t *testing.T) {
	b, _ := json.Marshal(Hello{Type: TypeHello})
	if strings.Contains(string(b), "prev_node") {
		t.Fatalf("bos prev_node yazilmamali: %s", b)
	}
}

func TestNormalizeNodeAddress(t *testing.T) {
	for in, want := range map[string]string{
		"https://edge1.zorven.app:8443": "edge1.zorven.app:8443",
		"http://10.0.0.1:80/x?y=1":      "10.0.0.1:80",
		"wss://a.b:443/":                "a.b:443",
		"a.b:443":                       "a.b:443",
		"":                              "",
	} {
		if got := NormalizeNodeAddress(in); got != want {
			t.Errorf("%q -> %q, beklenen %q", in, got, want)
		}
	}
}
