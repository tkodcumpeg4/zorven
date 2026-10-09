package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Sunucudan gelen, ajanin listesinde olmayan kabuk reddedilir ve hicbir surec baslatilmaz.
func TestTerminalOpenRejectsUnknownShell(t *testing.T) {
	got := make(chan protocol.TerminalExit, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var m protocol.TerminalExit
		_ = json.Unmarshal(data, &m)
		got <- m
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cs := newClientSession(conn, "", log)
	tm := newTerminalManager(cs, shellSet{
		List:    []protocol.ShellInfo{{ID: "sh", Path: "/bin/sh"}},
		Default: "sh",
	}, false, false)

	for _, bad := range []string{"/bin/bash", "../../etc/passwd", "evil"} {
		tm.open(ctx, protocol.TerminalOpen{Type: protocol.TypeTerminalOpen, SessionID: "s1", Cols: 80, Rows: 24, Shell: bad})
		select {
		case ex := <-got:
			if ex.Type != protocol.TypeTerminalExit || ex.Code == 0 || !strings.Contains(ex.Message, "kabuk") {
				t.Fatalf("%q icin reddetme beklenirdi: %+v", bad, ex)
			}
		case <-ctx.Done():
			t.Fatal("exit mesaji gelmedi")
		}
		if tm.count() != 0 {
			t.Fatal("oturum acilmamali")
		}
		break // sunucu yalnizca ilk mesaji okur; digerleri find() testinde kapsanir
	}
}

// hello kabuk listesini ve varsayilani tasir.
func TestHelloCarriesShells(t *testing.T) {
	h, _ := captureHello(t, &Agent{})
	if len(h.Shells) == 0 {
		t.Skip("bu makinede kabuk bulunamadi")
	}
	found := false
	for _, sh := range h.Shells {
		if sh.ID == h.DefaultShell {
			found = true
		}
		if sh.ID == "" || sh.Path == "" {
			t.Fatalf("eksik kabuk kaydi: %+v", sh)
		}
	}
	if !found {
		t.Fatalf("varsayilan %q listede yok", h.DefaultShell)
	}
}
