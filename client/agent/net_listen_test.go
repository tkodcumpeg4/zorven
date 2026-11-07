package agent

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestListenRetryWaitsForRelease(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := busy.Addr().String()
	go func() {
		time.Sleep(250 * time.Millisecond)
		busy.Close()
	}()
	ln, err := listenRetry(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("yeniden dinleme basarisiz: %v", err)
	}
	ln.Close()
}

func TestListenRetryRespectsContext(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := listenRetry(ctx, "tcp", busy.Addr().String()); err == nil {
		t.Fatal("hata bekleniyordu")
	}
	if time.Since(start) > time.Second {
		t.Fatal("ctx iptali dikkate alinmadi")
	}
}
