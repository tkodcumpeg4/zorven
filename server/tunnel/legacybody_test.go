package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestAgentLacksBodyError(t *testing.T) {
	for v, want := range map[string]bool{
		"": true, "0.3.7": true, "v0.3.7": true, "0.2.35": true, "0.3.7-rc1": true,
		"0.3.8": false, "0.3.9": false, "0.4.0": false, "1.0.0": false, "v0.3.8-rc1": false,
		"dev": false, "garip": false, "0.3": false,
	} {
		if got := agentLacksBodyError(v); got != want {
			t.Errorf("%q: %v, beklenen %v", v, got, want)
		}
	}
}

func TestDeclaredLength(t *testing.T) {
	cases := []struct {
		h    map[string][]string
		want int64
	}{
		{nil, -1},
		{map[string][]string{"Content-Length": {"10"}}, 10},
		{map[string][]string{"content-length": {" 7 "}}, 7},
		{map[string][]string{"Content-Length": {"10", "10"}}, 10},
		{map[string][]string{"Content-Length": {"10", "11"}}, -1},
		{map[string][]string{"Content-Length": {"x"}}, -1},
		{map[string][]string{"Content-Length": {"-1"}}, -1},
	}
	for _, c := range cases {
		if got := declaredLength(c.h); got != c.want {
			t.Errorf("%v: %d, beklenen %d", c.h, got, c.want)
		}
	}
}

// legacySession: el sikismasiz test oturumu; ajan surumu verilen degerle.
func legacySession(t *testing.T, version string) *Session {
	t.Helper()
	old := legacyBodyStall
	legacyBodyStall = 80 * time.Millisecond
	t.Cleanup(func() { legacyBodyStall = old })
	s := NewSession("ses_l", "cli_l", "l", "127.0.0.1:1", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Version = version
	s.legacyBodyStall = agentLacksBodyError(version)
	s.sendHook = func(context.Context, any, protocol.MessageType) error { return nil }
	return s
}

// startBody: ajan basligi + ilk govde parcasini yollamis gibi davranir.
func startBody(t *testing.T, s *Session, hdr map[string][]string, first []byte) *Exchange {
	t.Helper()
	e, err := s.newExchange()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := protocol.Marshal(protocol.HTTPResponse{Type: protocol.TypeHTTPResponse, ReqID: e.reqID,
		Status: 200, Headers: hdr, HasBody: true}, protocol.TypeHTTPResponse)
	if err := s.handleControl(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	<-e.Head()
	if len(first) > 0 {
		s.routeBodyFrame(protocol.BodyFrame{FrameType: protocol.FrameResponseBody, ReqID: e.reqID, Payload: first})
	}
	buf := make([]byte, len(first))
	if _, err := io.ReadFull(e, buf); err != nil {
		t.Fatal(err)
	}
	return e
}

// readWithin, Read'in d icinde donup donmedigi ve hatasi.
func readWithin(e *Exchange, d time.Duration) (error, bool) {
	ch := make(chan error, 1)
	go func() {
		_, err := e.Read(make([]byte, 16))
		ch <- err
	}()
	select {
	case err := <-ch:
		return err, true
	case <-time.After(d):
		return nil, false
	}
}

// B5: eski ajan Content-Length'li govdeyi yarida birakip sustuysa okuma
// legacyBodyStall sonra ErrBodyStalled ile biter (ziyaretci asili kalmaz).
func TestLegacyAgentStalledBodyCut(t *testing.T) {
	s := legacySession(t, "0.3.7")
	e := startBody(t, s, map[string][]string{"Content-Length": {"100"}}, []byte("yarim"))
	err, done := readWithin(e, 2*time.Second)
	if !done || !errors.Is(err, ErrBodyStalled) {
		t.Fatalf("ErrBodyStalled bekleniyordu: %v (dondu=%v)", err, done)
	}
}

// Veri aktikca sessizlik sayaci sifirlanir: yavas ama ilerleyen govde kesilmez.
func TestLegacyAgentSlowBodyNotCut(t *testing.T) {
	s := legacySession(t, "0.3.7")
	e := startBody(t, s, map[string][]string{"Content-Length": {"100"}}, []byte("a"))
	for i := 0; i < 5; i++ {
		time.Sleep(40 * time.Millisecond) // < legacyBodyStall
		s.routeBodyFrame(protocol.BodyFrame{FrameType: protocol.FrameResponseBody, ReqID: e.reqID, Payload: []byte("b")})
		if n, err := e.Read(make([]byte, 1)); err != nil || n != 1 {
			t.Fatalf("yavas govde kesildi: %v", err)
		}
	}
}

// SSE/uzun yoklama (Content-Length yok), yeni ajan, "dev" surum ve uzunluga
// ulasmis govde ASLA kesilmez.
func TestLegacyStallGuardNotApplied(t *testing.T) {
	cases := []struct {
		name, ver string
		hdr       map[string][]string
		first     []byte
	}{
		{"sse", "0.3.7", map[string][]string{"Content-Type": {"text/event-stream"}}, []byte("data: x\n\n")},
		{"yeni ajan", "0.3.8", map[string][]string{"Content-Length": {"100"}}, []byte("yarim")},
		{"dev", "dev", map[string][]string{"Content-Length": {"100"}}, []byte("yarim")},
		{"tamamlandi", "0.3.7", map[string][]string{"Content-Length": {"5"}}, []byte("tamam")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := legacySession(t, c.ver)
			e := startBody(t, s, c.hdr, c.first)
			if err, done := readWithin(e, 300*time.Millisecond); done {
				t.Fatalf("okuma kesilmemeliydi: %v", err)
			}
			e.closeBody(nil) // bekleyen okumayi birak
		})
	}
}
