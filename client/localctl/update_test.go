package localctl

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type updCtl struct {
	fake
	called chan struct{}
}

func (u *updCtl) TriggerUpdate() { close(u.called) }

// "update" islemi Updater uygulayan ajanda tetiklenir, uygulamayanda hata doner.
func TestUpdateOp(t *testing.T) {
	dir := t.TempDir()
	ep := Endpoint{Path: filepath.Join(dir, "u.sock")}
	ctl := &updCtl{called: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, ep, ctl, "0.0.1", nil) }()
	deadline := time.Now().Add(2 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if _, err = Call(ep, Request{Op: "update"}); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("update cagrisi: %v", err)
	}
	select {
	case <-ctl.called:
	case <-time.After(2 * time.Second):
		t.Fatal("TriggerUpdate cagrilmadi")
	}
}
