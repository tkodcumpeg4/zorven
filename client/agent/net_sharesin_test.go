package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/client/wgnet"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// nopTransport: kontrol/ikili mesajlari yutar (sunucu gerekmez).
type nopTransport struct{}

func (nopTransport) SendControl(context.Context, any, protocol.MessageType) error { return nil }
func (nopTransport) SendBinary(context.Context, []byte) error                     { return nil }

// readyNode, kayitli ve ilk netmap'i uygulanmis bir wg dugumu uretir.
func readyNode(t *testing.T, in []protocol.NetShareIn) *wgnet.Node {
	t.Helper()
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	key, err := wgnet.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	n := wgnet.NewNode(lg, nopTransport{}, key, "test")
	t.Cleanup(n.Close)
	reg, err := protocol.Marshal(protocol.NetRegistered{
		Type: protocol.TypeNetRegistered, NodeID: "n1", IP: "100.64.0.2", Name: "test", Kind: "device",
	}, protocol.TypeNetRegistered)
	if err != nil {
		t.Fatal(err)
	}
	n.HandleControl(context.Background(), protocol.TypeNetRegistered, reg)
	self := protocol.NetPeer{NodeID: "n1", IP: "100.64.0.2", Name: "test", Kind: "device"}
	mp, err := protocol.Marshal(protocol.NetMap{Type: protocol.TypeNetMap, Full: true, Self: self, SharesIn: in}, protocol.TypeNetMap)
	if err != nil {
		t.Fatal(err)
	}
	n.HandleControl(context.Background(), protocol.TypeNetMap, mp)
	return n
}

func TestNetSharesIn(t *testing.T) {
	a := &Agent{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := a.NetSharesIn(); !errors.Is(err, localctl.ErrNoNet) {
		t.Fatalf("dugum yokken ErrNoNet beklenirdi: %v", err)
	}

	n := readyNode(t, []protocol.NetShareIn{
		{ID: "b", Name: "web", OwnerName: "nas", OwnerIP: "100.64.0.9", Port: 8080, Proto: "tcp"},
		{ID: "a", Name: "db", OwnerName: "nas", OwnerIP: "100.64.0.9", Port: 5432, Proto: "tcp"},
		{ID: "c", Name: "db", OwnerName: "lab", OwnerIP: "100.64.0.7", Port: 5432, Proto: "tcp"},
	})
	a.netRT.set(context.Background(), n)
	got, err := a.NetSharesIn()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	// Ada, sonra sahip cihaza gore sirali: db/lab, db/nas, web.
	if want := []string{"c", "a", "b"}; len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("sira hatali: %v", ids)
	}

	// Oturum biterken dugum kayittan dusunce yine ErrNoNet.
	a.netRT.clear(n)
	if _, err := a.NetSharesIn(); !errors.Is(err, localctl.ErrNoNet) {
		t.Fatalf("temizlenince ErrNoNet beklenirdi: %v", err)
	}
}

func TestNetWaitReady(t *testing.T) {
	a := &Agent{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.NetWaitReady(context.Background()); !errors.Is(err, localctl.ErrNoNet) {
		t.Fatalf("dugum yokken ErrNoNet beklenirdi: %v", err)
	}

	// Kayitli ama henuz netmap almamis dugum: NetWaitReady hazir olana kadar bekler.
	key, err := wgnet.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cold := wgnet.NewNode(a.Log, nopTransport{}, key, "soguk")
	t.Cleanup(cold.Close)
	a.netRT.set(context.Background(), cold)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := a.NetWaitReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hazir olmayan dugumde zaman asimi beklenirdi: %v", err)
	}

	// Hazir dugum hemen doner.
	a.netRT.set(context.Background(), readyNode(t, nil))
	if err := a.NetWaitReady(context.Background()); err != nil {
		t.Fatalf("hazir dugum: %v", err)
	}
}
