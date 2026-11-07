package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/client/config"
)

// Kimliksiz kurulan Windows servisi token belirene kadar bekler; ctx bitince
// bos ayarla doner (bekleme dongusu askida kalmaz).
func TestWaitForTokenCancel(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	if cfg := waitForToken(ctx, lg, 10*time.Millisecond); cfg.Token != "" {
		t.Fatalf("token gelmemeliydi: %+v", cfg)
	}
}

// Token ayar dosyasina yazilinca bekleyen servis onu alir.
func TestWaitForTokenPicksUp(t *testing.T) {
	isolateHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	p, err := config.Path()
	if err != nil {
		t.Skip(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = config.SaveFile(p, config.Config{Token: "zrv_live_abc"})
	}()
	if cfg := waitForToken(ctx, lg, 10*time.Millisecond); cfg.Token != "zrv_live_abc" {
		t.Fatalf("token alinmadi: %+v", cfg)
	}
}
