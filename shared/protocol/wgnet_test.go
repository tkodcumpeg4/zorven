package protocol

import (
	"encoding/json"
	"net/netip"
	"testing"
)

func TestFrameWGRoundTrip(t *testing.T) {
	ip := netip.MustParseAddr("100.64.1.7")
	in := BodyFrame{FrameType: FrameWG, ReqID: IPToRelayID(ip), Seq: 42, Payload: []byte("sifreli-paket")}
	out, err := DecodeBodyFrame(in.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.FrameType != FrameWG || out.Seq != 42 || string(out.Payload) != "sifreli-paket" {
		t.Fatalf("%+v", out)
	}
	if RelayIDToIP(out.ReqID) != ip {
		t.Fatalf("ip=%v", RelayIDToIP(out.ReqID))
	}
	if FrameWG != 8 {
		t.Fatal("FrameWG tasarimda 8")
	}
}

func TestNetMessagesRoundTrip(t *testing.T) {
	msgs := []struct {
		t MessageType
		v any
	}{
		{TypeNetRegister, NetRegister{Type: TypeNetRegister, WGPub: "abc", Name: "x"}},
		{TypeNetRegistered, NetRegistered{Type: TypeNetRegistered, NodeID: "n", IP: "100.64.1.1", MTU: 1380}},
		{TypeNetMap, NetMap{Type: TypeNetMap, Full: true, Peers: []NetPeer{{WGPub: "p", IP: "100.64.1.2"}},
			SharesIn: []NetShareIn{{ID: "s", Port: 80, Proto: "tcp"}}, SharesOut: []NetShareOut{{ID: "o", Allow: []string{"100.64.1.2"}}}}},
		{TypeNetShareCreate, NetShareCreate{Type: TypeNetShareCreate, ReqID: "r", Port: 5432, To: []NetGrantee{{Type: "user", ID: "u"}}}},
		{TypeNetShareDelete, NetShareDelete{Type: TypeNetShareDelete, ReqID: "r", ShareID: "s"}},
		{TypeNetShareResult, NetShareResult{Type: TypeNetShareResult, ReqID: "r", OK: true, ShareID: "s"}},
		{TypeNetError, NetError{Type: TypeNetError, Code: "c"}},
	}
	for _, m := range msgs {
		b, err := Marshal(m.v, m.t)
		if err != nil {
			t.Fatalf("%s: %v", m.t, err)
		}
		if pt, err := PeekType(b); err != nil || pt != m.t {
			t.Fatalf("%s: peek %v %v", m.t, pt, err)
		}
		// Bilinmeyen alan toleransi (ileri uyumluluk).
		var generic map[string]any
		_ = json.Unmarshal(b, &generic)
		generic["yeni_alan"] = 1
		b2, _ := json.Marshal(generic)
		var nm NetMap
		if err := json.Unmarshal(b2, &nm); err != nil {
			t.Fatalf("%s: bilinmeyen alan reddedildi: %v", m.t, err)
		}
	}
}

func TestOldHelloHasNoWGFeatures(t *testing.T) {
	var h Hello
	if err := json.Unmarshal([]byte(`{"type":"hello","client_version":"0.2.0","platform":"linux/amd64"}`), &h); err != nil {
		t.Fatal(err)
	}
	for _, f := range h.Features {
		if f == FeatureWGNet || f == FeatureWGRelay {
			t.Fatal("eski istemci wg ilan etmemeli")
		}
	}
}

func TestNetStateDelta(t *testing.T) {
	s := NewNetState()
	s.Apply(NetMap{Full: true, Epoch: 1, Peers: []NetPeer{{WGPub: "a", IP: "1.1.1.1"}, {WGPub: "b", IP: "1.1.1.2"}},
		SharesIn: []NetShareIn{{ID: "s1"}}, SharesOut: []NetShareOut{{ID: "o1"}}})
	s.Apply(NetMap{Epoch: 2, RemovedPeers: []string{"a"}, RemovedIn: []string{"s1"},
		Peers: []NetPeer{{WGPub: "c", IP: "1.1.1.3"}}, SharesIn: []NetShareIn{{ID: "s2"}}})
	if len(s.Peers) != 2 || s.Peers["a"].IP != "" || s.Peers["c"].IP == "" {
		t.Fatalf("peers=%v", s.Peers)
	}
	if len(s.SharesIn) != 1 || len(s.SharesOut) != 1 || s.Epoch != 2 {
		t.Fatalf("durum: %+v", s)
	}
	s.Apply(NetMap{Full: true, Epoch: 3})
	if len(s.Peers) != 0 || len(s.SharesIn) != 0 {
		t.Fatal("tam harita durumu degistirmeli")
	}
}
