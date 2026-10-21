package netctl

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	"golang.org/x/time/rate"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type fakeUsage struct {
	mu    sync.Mutex
	state *bandwidth.TenantRuntimeState
	out   int64
	typ   bandwidth.UsageType
}

func (f *fakeUsage) GetOrCreateState(context.Context, string) (*bandwidth.TenantRuntimeState, error) {
	return f.state, nil
}

func (f *fakeUsage) Record(_ context.Context, e bandwidth.UsageEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.out += e.BytesOut
	f.typ = e.Type
	return nil
}

func TestRelayRecordsUsageOnClose(t *testing.T) {
	h := newHarness(t)
	fu := &fakeUsage{state: bandwidth.NewTenantRuntimeState("t1", "enterprise", 0, 0, 0, 0)}
	h.m.Usage = fu
	nas := h.attach(dev("t1", "nas", "nas"))
	alice := h.attach(sess("t1", "alice", "member"))
	mkShare(t, h, "t1", "nas", 22, Grantee{GranteeUser, "alice"})
	nas.drain()
	alice.drain()
	dst := protocol.IPToRelayID(netip.MustParseAddr(nas.reg.IP))
	alice.nc.Relay(dst, make([]byte, 700))
	alice.nc.Relay(dst, make([]byte, 300))
	alice.nc.Close()
	fu.mu.Lock()
	defer fu.mu.Unlock()
	if fu.out != 1000 || fu.typ != bandwidth.UsageTypeNetRelay {
		t.Fatalf("relay kaydi out=%d typ=%s", fu.out, fu.typ)
	}
}

func TestRelayThrottleDrops(t *testing.T) {
	h := newHarness(t)
	st := bandwidth.NewTenantRuntimeState("t1", "free", 0, 0, 10, 1)
	st.GetLimiter().SetLimit(rate.Limit(1000))
	st.GetLimiter().SetBurst(1000)
	h.m.Usage = &fakeUsage{state: st}
	nas := h.attach(dev("t1", "nas", "nas"))
	alice := h.attach(sess("t1", "alice", "member"))
	mkShare(t, h, "t1", "nas", 22, Grantee{GranteeUser, "alice"})
	nas.drain()
	alice.drain()
	dst := protocol.IPToRelayID(netip.MustParseAddr(nas.reg.IP))
	for i := 0; i < 10; i++ {
		alice.nc.Relay(dst, make([]byte, 500))
	}
	nas.drain()
	if len(nas.data) != 2 {
		t.Fatalf("burst 1000 B ile 2 paket gecmeli, %d gecti", len(nas.data))
	}
}
