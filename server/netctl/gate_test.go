package netctl

import (
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Z2: Hooks.Gate paylasim ACL'ine ek kapi koyar; kapi kapaninca Recompute ile
// peer netmap'ten cikar ve relay gorunurlugu kapanir (oturum ortasi iptal).
func TestGateRevokesOpenAccessOnRecompute(t *testing.T) {
	h := newHarness(t)
	var open atomic.Bool
	open.Store(true)
	var gotOwner atomic.Value
	h.m.Hooks.Gate = func(tenantID, ownerClientID string, sub Subject) bool {
		gotOwner.Store(ownerClientID)
		return open.Load()
	}
	nas := h.attach(dev("t1", "nas", "nas"))
	alice := h.attach(sess("t1", "alice", "member"))
	mkShare(t, h, "t1", "nas", 5432, Grantee{GranteeUser, "alice"})
	nas.drain()
	alice.drain()
	if !alice.hasPeer(nas.pub) || !nas.hasPeer(alice.pub) {
		t.Fatal("kapi aciksa erisim olmali")
	}
	if v, _ := gotOwner.Load().(string); v != "nas" {
		t.Fatalf("kapiya sahip cihaz verilmedi: %q", v)
	}
	open.Store(false)
	h.m.Recompute("t1")
	nas.drain()
	alice.drain()
	if alice.hasPeer(nas.pub) || len(alice.state.SharesIn) != 0 || nas.hasPeer(alice.pub) {
		t.Fatal("kapi kapaninca acik erisim kesilmeli")
	}
	alice.nc.Relay(protocol.IPToRelayID(netip.MustParseAddr(nas.reg.IP)), []byte("x"))
	nas.drain()
	if len(nas.data) != 0 {
		t.Fatal("kapi kapaliyken relay calismamali")
	}
	open.Store(true)
	h.m.Recompute("t1")
	nas.drain()
	alice.drain()
	if !alice.hasPeer(nas.pub) {
		t.Fatal("kapi acilinca erisim geri gelmeli")
	}
	if len(h.m.TenantIDs()) != 1 {
		t.Fatalf("TenantIDs: %v", h.m.TenantIDs())
	}
}
