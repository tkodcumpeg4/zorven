package rawproxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("yerel dinleme yok:", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// K3: ayni genel port numarasi iki farkli IP'de (TCP ve UDP ayri port) ayri dinleyici olur;
// kaydirmali (bridge) yuvada dinleme portu genel porttan farklidir.
func TestReloadSamePortOnTwoIPs(t *testing.T) {
	p := freePort(t)
	p2 := freePort(t)
	if ln, err := net.Listen("tcp", "127.0.0.2:0"); err != nil {
		t.Skip("127.0.0.2 kullanilamiyor:", err)
	} else {
		ln.Close()
	}
	infos := []TunnelInfo{
		{TunnelID: "a", ClientID: "c", TenantID: "t", Proto: "tcp", Port: p, BindAddr: "127.0.0.1"},
		{TunnelID: "b", ClientID: "c", TenantID: "t", Proto: "tcp", Port: p, BindAddr: "127.0.0.2"},
		{TunnelID: "c", ClientID: "c", TenantID: "t", Proto: "udp", Port: p2, BindAddr: "127.0.0.1"},
		{TunnelID: "d", ClientID: "c", TenantID: "t", Proto: "udp", Port: p2, BindAddr: "127.0.0.2"},
	}
	m := New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(context.Context) ([]TunnelInfo, error) { return infos, nil })
	m.Start(context.Background())
	defer m.closeAll()

	m.mu.Lock()
	tcp, udp := len(m.tcpLn), len(m.udpLn)
	m.mu.Unlock()
	if tcp != 2 || udp != 2 {
		t.Fatalf("iki IP'de ayri dinleyici beklenir: tcp=%d udp=%d", tcp, udp)
	}
	for _, ip := range []string{"127.0.0.1", "127.0.0.2"} {
		if l, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(p))); err == nil {
			l.Close()
			t.Fatalf("%s:%d dinleniyor olmali", ip, p)
		}
	}

	// Ikincil IP kaldirilinca yalniz onun dinleyicileri kapanir.
	infos = infos[:1]
	m.Reload()
	m.mu.Lock()
	tcp, udp = len(m.tcpLn), len(m.udpLn)
	m.mu.Unlock()
	if tcp != 1 || udp != 0 {
		t.Fatalf("kaldirmadan sonra tcp=%d udp=%d", tcp, udp)
	}
}

func TestListenPortOffset(t *testing.T) {
	pub := freePort(t)
	lp := freePort(t)
	infos := []TunnelInfo{{TunnelID: "a", ClientID: "c", TenantID: "t", Proto: "tcp", Port: pub, ListenPort: lp}}
	m := New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(context.Context) ([]TunnelInfo, error) { return infos, nil })
	m.Start(context.Background())
	defer m.closeAll()
	if l, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(lp))); err == nil {
		l.Close()
		t.Fatalf("kaydirilmis dinleme portu %d acik olmali", lp)
	}
	if l, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(pub))); err != nil {
		t.Fatalf("genel port %d dinlenmemeli: %v", pub, err)
	} else {
		l.Close()
	}
}
