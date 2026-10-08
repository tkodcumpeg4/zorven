package rawproxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

type fakeGate struct {
	enabled map[string]bool
	grants  map[string]time.Time // tunnel|ip -> bitis
	blocked atomic.Int64
}

func (g *fakeGate) DoorEnabled(tid string) bool { return g.enabled[tid] }
func (g *fakeGate) HasGrant(tid, ip string) (time.Time, bool) {
	exp, ok := g.grants[tid+"|"+ip]
	if !ok || !exp.After(time.Now()) {
		return time.Time{}, false
	}
	return exp, true
}
func (g *fakeGate) NoteBlocked(_, _, _ string) { g.blocked.Add(1) }

type ruleStore struct {
	store.Store
	rules []store.IPAllowlistRule
}

func (r *ruleStore) ListAllActiveIPRules(context.Context) ([]store.IPAllowlistRule, error) {
	return r.rules, nil
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newDoorMgr(t *testing.T, gate DoorGate, cidrs ...string) *Manager {
	t.Helper()
	var eng *ipfilter.Engine
	if len(cidrs) > 0 {
		rs := &ruleStore{}
		for _, c := range cidrs {
			rs.rules = append(rs.rules, store.IPAllowlistRule{ID: "r" + c, TenantID: "ten", CIDR: c, Enabled: true})
		}
		eng = ipfilter.NewEngine(rs, nil)
		if err := eng.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	m := New(tunnel.NewHub(), eng, quietLog(), nil)
	if gate != nil {
		m.Door = gate
	}
	return m
}

var ti1 = TunnelInfo{TunnelID: "tun", TenantID: "ten", ClientID: "cli", Proto: "tcp", Port: 10001}

func TestAdmitDoorMatrix(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Minute)
	ctx := context.Background()

	// Kapi kapali, statik liste yok: acik (eski davranis).
	g := &fakeGate{}
	if ok, exp := newDoorMgr(t, g).admit(ctx, ti1, "203.0.113.7"); !ok || !exp.IsZero() {
		t.Fatal("kapi kapaliyken acik olmali")
	}
	// Kapi kapali, statik liste var: listedekiler gecer, digerleri reddedilir; grant etkisiz.
	g = &fakeGate{grants: map[string]time.Time{"tun|198.51.100.1": future}}
	m := newDoorMgr(t, g, "203.0.113.0/24")
	if ok, _ := m.admit(ctx, ti1, "203.0.113.7"); !ok {
		t.Fatal("listedeki IP gecmeli")
	}
	if ok, _ := m.admit(ctx, ti1, "198.51.100.1"); ok {
		t.Fatal("kapi kapaliyken grant sayilmamali")
	}
	if g.blocked.Load() != 0 {
		t.Fatal("kapi kapaliyken engelleme sayaci artmamali")
	}

	// Kapi acik, statik liste yok: grant'siz varsayilan RED.
	g = &fakeGate{enabled: map[string]bool{"tun": true}, grants: map[string]time.Time{
		"tun|198.51.100.1": future, "tun|198.51.100.2": past}}
	m = newDoorMgr(t, g)
	if ok, _ := m.admit(ctx, ti1, "203.0.113.7"); ok {
		t.Fatal("grant'siz IP reddedilmeli")
	}
	if g.blocked.Load() != 1 {
		t.Fatalf("engellenen sayilmali: %d", g.blocked.Load())
	}
	ok, exp := m.admit(ctx, ti1, "198.51.100.1")
	if !ok || !exp.Equal(future) {
		t.Fatal("grant'li IP gecmeli ve bitisi donmeli")
	}
	if ok, _ := m.admit(ctx, ti1, "198.51.100.2"); ok {
		t.Fatal("suresi dolmus grant gecmemeli")
	}

	// Kapi acik + statik liste: listedeki grant'siz gecer (bitis yok), digeri grant ile gecer.
	g = &fakeGate{enabled: map[string]bool{"tun": true}, grants: map[string]time.Time{"tun|198.51.100.1": future}}
	m = newDoorMgr(t, g, "203.0.113.0/24")
	if ok, exp := m.admit(ctx, ti1, "203.0.113.7"); !ok || !exp.IsZero() {
		t.Fatal("statik listedeki IP grant'siz gecmeli")
	}
	if ok, _ := m.admit(ctx, ti1, "198.51.100.1"); !ok {
		t.Fatal("grant'li IP statik liste disinda da gecmeli")
	}
	if ok, _ := m.admit(ctx, ti1, "192.0.2.9"); ok {
		t.Fatal("ne listede ne grant'te: red")
	}
}

// TCP baglantisi: grant'siz reddedilir (yonetici dinleyicisiz dogrudan bridgeTCP).
func TestBridgeTCPDeniedWithoutGrant(t *testing.T) {
	g := &fakeGate{enabled: map[string]bool{"tun": true}}
	m := newDoorMgr(t, g)
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { m.bridgeTCP(&addrConn{Conn: server, remote: "203.0.113.7:5555"}, ti1); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reddedilen baglanti hemen donmeli")
	}
	if g.blocked.Load() != 1 {
		t.Fatalf("engelleme sayilmadi: %d", g.blocked.Load())
	}
}

type addrConn struct {
	net.Conn
	remote string
}

func (a *addrConn) RemoteAddr() net.Addr {
	ap, _ := net.ResolveTCPAddr("tcp", a.remote)
	return ap
}

// UDP: yeni flow yalnizca grant'li kaynaktan degerlendirilir; reddedilen sayilir.
func TestUDPOpenFlowGrant(t *testing.T) {
	future := time.Now().Add(time.Hour)
	g := &fakeGate{enabled: map[string]bool{"tun": true}, grants: map[string]time.Time{"tun|198.51.100.1": future}}
	m := newDoorMgr(t, g)
	ul := &udpListener{m: m, flows: map[string]*udpFlow{}}
	ti := ti1
	ti.Proto = "udp"
	ul.setInfo(ti)

	deniedSrc := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 4000}
	if f := ul.openFlow(ul.info(), deniedSrc, deniedSrc.String()); f != nil {
		t.Fatal("grant'siz kaynak flow acmamali")
	}
	if g.blocked.Load() != 1 {
		t.Fatalf("UDP engelleme sayilmadi: %d", g.blocked.Load())
	}
	// Grant'li kaynak admit'i gecer (istemci cevrimdisi oldugundan flow yine nil, sayac artmaz).
	okSrc := &net.UDPAddr{IP: net.ParseIP("198.51.100.1"), Port: 4001}
	ul.openFlow(ul.info(), okSrc, okSrc.String())
	if g.blocked.Load() != 1 {
		t.Fatal("grant'li kaynak engellenmemeli")
	}
}

func TestConnRegistryCloseAll(t *testing.T) {
	var r connRegistry
	var n atomic.Int64
	r.add("tun", "203.0.113.7", func() { n.Add(1) })
	rm := r.add("tun", "::ffff:203.0.113.7", func() { n.Add(10) })
	r.add("tun", "203.0.113.8", func() { n.Add(100) })
	rm() // kayit silinen kapatilmaz
	if c := r.closeAll("tun", "203.0.113.7"); c != 1 || n.Load() != 1 {
		t.Fatalf("1 kapatici bekleniyordu: c=%d n=%d", c, n.Load())
	}
	if c := r.closeAll("tun", "203.0.113.7"); c != 0 {
		t.Fatal("ikinci cagri bos olmali")
	}
}
