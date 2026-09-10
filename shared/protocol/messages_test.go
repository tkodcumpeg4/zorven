package protocol

import (
	"encoding/json"
	"testing"
)

func TestWindowUpdateRoundTrip(t *testing.T) {
	data, err := Marshal(WindowUpdate{
		Type: TypeWindowUpdate, ReqID: 42, Increment: 65536,
	}, TypeWindowUpdate)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got WindowUpdate
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ReqID != 42 || got.Increment != 65536 || got.Type != TypeWindowUpdate {
		t.Fatalf("beklenmeyen: %+v", got)
	}
}

// Pencere en az bir tam cerceve almali; aksi halde istemci hic ilerleyemez.
func TestInitialWindowFitsAFrame(t *testing.T) {
	if InitialWindowBytes < BodyChunkSize {
		t.Fatalf("InitialWindowBytes (%d) en az BodyChunkSize (%d) olmali",
			InitialWindowBytes, BodyChunkSize)
	}
}

// Hello, yetenekleri tasiyabilmeli (eski istemcilerde alan bos gelir).
func TestHelloCarriesFeatures(t *testing.T) {
	data, err := Marshal(Hello{
		Type: TypeHello, ClientVersion: "0.1.0", Platform: "windows/amd64",
		Features: []string{FeatureFlowControl},
	}, TypeHello)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Hello
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Features) != 1 || got.Features[0] != FeatureFlowControl {
		t.Fatalf("features tasinmadi: %+v", got)
	}
}

// Eski istemci uyumlulugu: Features gonderilmezse JSON'da alan HIC olmamali
// (omitempty), boylece eski sunucular da mesaji sorunsuz cozer.
func TestHelloOmitsEmptyFeatures(t *testing.T) {
	data, err := Marshal(Hello{
		Type: TypeHello, ClientVersion: "0.1.0", Platform: "linux/amd64",
	}, TypeHello)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["features"]; ok {
		t.Fatalf("bos features JSON'a yazilmamaliydi: %s", data)
	}
}

// HelloAck de yetenek tasiyabilmeli (sunucunun ETKINLESTIRDIKLERI).
func TestHelloAckCarriesFeatures(t *testing.T) {
	data, err := Marshal(HelloAck{
		Type: TypeHelloAck, ClientID: "cli_x", SessionID: "ses_y",
		HeartbeatIntervalS: 30,
		Features:           []string{FeatureFlowControl},
	}, TypeHelloAck)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got HelloAck
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Features) != 1 || got.Features[0] != FeatureFlowControl {
		t.Fatalf("ack features tasinmadi: %+v", got)
	}
}
