package api

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

func baseCapture() *reqlog.Capture {
	return &reqlog.Capture{
		ID: "cap_1", TunnelID: "tun_1",
		Method: "POST", Path: "/api/pay", Query: "a=1",
		ReqHeaders: map[string][]string{
			"Content-Type":   {"application/json"},
			"Cookie":         {"session=abc"},
			"Content-Length": {"9"},
		},
		ReqBody: []byte(`{"x":1}`),
	}
}

func strp(s string) *string { return &s }

// Override verilmezse yakalama AYNEN kullanilmali (eski davranis).
func TestApplyReplayOverridesNil(t *testing.T) {
	c := baseCapture()
	m, p, q, h, b, tid, verr := applyReplayOverrides(c, nil)
	if verr != "" {
		t.Fatalf("beklenmeyen hata: %s", verr)
	}
	if m != "POST" || p != "/api/pay" || q != "a=1" || tid != "tun_1" {
		t.Errorf("degerler degisti: %s %s %s %s", m, p, q, tid)
	}
	if string(b) != `{"x":1}` {
		t.Errorf("govde = %q", b)
	}
	if _, ok := h["Cookie"]; !ok {
		t.Error("orijinal basliklar korunmali")
	}
}

// Yakalamanin baslik haritasi DEGISMEMELI: kopya uzerinde calisilir.
func TestApplyReplayOverridesDoesNotMutateCapture(t *testing.T) {
	c := baseCapture()
	ov := &replayOverrides{RemoveHeaders: []string{"Cookie"}}
	if _, _, _, _, _, _, verr := applyReplayOverrides(c, ov); verr != "" {
		t.Fatal(verr)
	}
	if _, ok := c.ReqHeaders["Cookie"]; !ok {
		t.Error("orijinal yakalamanin basliklari degistirildi")
	}
}

func TestApplyReplayOverridesFields(t *testing.T) {
	c := baseCapture()
	ov := &replayOverrides{
		Method:        strp("put"),
		Path:          strp("/api/v2/pay"),
		Query:         strp("?b=2"),
		Headers:       map[string]string{"X-Test": "1"},
		RemoveHeaders: []string{"cookie"},
		Body:          strp(`{"x":2}`),
		TunnelID:      strp("tun_2"),
	}
	m, p, q, h, b, tid, verr := applyReplayOverrides(c, ov)
	if verr != "" {
		t.Fatalf("beklenmeyen hata: %s", verr)
	}
	if m != "PUT" {
		t.Errorf("method buyuk harfe cevrilmedi: %s", m)
	}
	if p != "/api/v2/pay" || q != "b=2" {
		t.Errorf("path/query = %s %s ('?' kirpilmali)", p, q)
	}
	if tid != "tun_2" {
		t.Errorf("tunnel_id = %s", tid)
	}
	if string(b) != `{"x":2}` {
		t.Errorf("govde = %q", b)
	}
	if _, ok := h["Cookie"]; ok {
		t.Error("Cookie kaldirilmaliydi (buyuk/kucuk duyarsiz)")
	}
	if got := h["X-Test"]; len(got) != 1 || got[0] != "1" {
		t.Errorf("X-Test = %v", got)
	}
	// Content-Length HER ZAMAN dusurulur; govdeden yeniden hesaplanir.
	if _, ok := h["Content-Length"]; ok {
		t.Error("Content-Length dusurulmeliydi")
	}
}

// Bos govde "verilmedi" ile ayni sey degil: kullanici kasitli bosaltabilmeli.
func TestApplyReplayOverridesEmptyBodyIsIntentional(t *testing.T) {
	c := baseCapture()
	_, _, _, _, b, _, verr := applyReplayOverrides(c, &replayOverrides{Body: strp("")})
	if verr != "" {
		t.Fatal(verr)
	}
	if len(b) != 0 {
		t.Errorf("govde bosaltilmaliydi, alinan %q", b)
	}
}

func TestApplyReplayOverridesRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		ov   *replayOverrides
	}{
		{"bilinmeyen method", &replayOverrides{Method: strp("TRACE")}},
		{"goreli path", &replayOverrides{Path: strp("api/x")}},
		{"path'te satir sonu", &replayOverrides{Path: strp("/x\r\nX: y")}},
		{"query'de bosluk", &replayOverrides{Query: strp("a= 1")}},
		{"baslik adinda iki nokta", &replayOverrides{Headers: map[string]string{"X:Y": "1"}}},
		{"baslik adinda bosluk", &replayOverrides{Headers: map[string]string{"X Y": "1"}}},
		{"baslik degerinde CRLF", &replayOverrides{Headers: map[string]string{"X": "a\r\nY: b"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, _, _, _, verr := applyReplayOverrides(baseCapture(), tc.ov); verr == "" {
				t.Error("reddedilmeliydi")
			}
		})
	}
}

// Bos tunnel_id orijinali korur: panelden bos secim kazayla tuneli silmesin.
func TestApplyReplayOverridesEmptyTunnelKeepsOriginal(t *testing.T) {
	_, _, _, _, _, tid, verr := applyReplayOverrides(baseCapture(), &replayOverrides{TunnelID: strp("  ")})
	if verr != "" {
		t.Fatal(verr)
	}
	if tid != "tun_1" {
		t.Errorf("tunnel_id = %s, orijinal korunmaliydi", tid)
	}
}
