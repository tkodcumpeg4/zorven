package pgstore

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// NetStore (migration 0077) yazma/okuma ve silme turu.
func TestNetStoreRoundTrip(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	ns := s.Net()
	tid := store.DefaultTenantID

	n := netctl.NodeRec{ID: "nod_1", TenantID: tid, Kind: netctl.KindDevice, ClientID: "cli_x", Name: "nas",
		IP: netip.MustParseAddr("100.64.1.1"), WGPub: "pub1", CreatedAt: time.Now()}
	if err := ns.PutNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	n.WGPub = "pub2"
	n.LastSeen = time.Now()
	if err := ns.PutNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	nodes, err := ns.ListNodes(ctx, tid)
	if err != nil || len(nodes) != 1 || nodes[0].WGPub != "pub2" || nodes[0].IP.String() != "100.64.1.1" {
		t.Fatalf("nodes=%+v err=%v", nodes, err)
	}

	exp := time.Now().Add(time.Hour).UTC()
	sh := netctl.Share{ID: "shr_1", TenantID: tid, OwnerClientID: "cli_x", Name: "db", Proto: "tcp", Port: 5432,
		Grantees: []netctl.Grantee{{Type: "user", ID: "u1"}}, ExpiresAt: &exp, CreatedBy: "user:u1", CreatedAt: time.Now().UTC()}
	if err := ns.PutShare(ctx, sh); err != nil {
		t.Fatal(err)
	}
	sh.Grantees = append(sh.Grantees, netctl.Grantee{Type: "group", ID: "everyone"})
	if err := ns.PutShare(ctx, sh); err != nil {
		t.Fatal(err)
	}
	shares, err := ns.ListShares(ctx, tid)
	if err != nil || len(shares) != 1 || len(shares[0].Grantees) != 2 || shares[0].ExpiresAt == nil {
		t.Fatalf("shares=%+v err=%v", shares, err)
	}
	if err := ns.DeleteShare(ctx, tid, "shr_1"); err != nil {
		t.Fatal(err)
	}
	if err := ns.DeleteNode(ctx, tid, "nod_1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := ns.ListShares(ctx, tid); len(l) != 0 {
		t.Fatal("silinmedi")
	}
}
