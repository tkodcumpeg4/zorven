package localctl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type netFake struct {
	fake
	nmu    sync.Mutex
	shares map[string]ShareInfo
	last   ShareSpec
	opened map[string]string
}

func (n *netFake) ShareCreate(_ context.Context, sp ShareSpec) (ShareInfo, error) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	if sp.Port == 13 {
		return ShareInfo{}, errors.New("port yasak (policy)")
	}
	n.last = sp
	si := ShareInfo{ID: "s1", Name: sp.Name, Port: sp.Port, Proto: "tcp", To: sp.To, Persistent: sp.Persist}
	if n.shares == nil {
		n.shares = map[string]ShareInfo{}
	}
	n.shares[si.ID] = si
	return si, nil
}
func (n *netFake) ShareDelete(_ context.Context, id string) (ShareInfo, error) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	for k, s := range n.shares {
		if k == id || s.Name == id {
			delete(n.shares, k)
			return s, nil
		}
	}
	return ShareInfo{}, errors.New("paylasim bulunamadi: " + id)
}
func (n *netFake) ShareList() ([]ShareInfo, error) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	var o []ShareInfo
	for _, s := range n.shares {
		o = append(o, s)
	}
	return o, nil
}
func (n *netFake) AccessOpen(_ context.Context, t, l string) (string, error) {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	if n.opened == nil {
		n.opened = map[string]string{}
	}
	n.opened[l] = t
	return l, nil
}
func (n *netFake) AccessClose(l string) error {
	n.nmu.Lock()
	defer n.nmu.Unlock()
	if _, ok := n.opened[l]; !ok {
		return errors.New("yok")
	}
	delete(n.opened, l)
	return nil
}

func serveTemp(t *testing.T, ctl Controller, service bool) Endpoint {
	t.Helper()
	dir, err := os.MkdirTemp("", "zv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ep := Endpoint{Path: filepath.Join(dir, "d", "ctl.sock"), Service: service}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := Serve(ctx, ep, ctl, "t", nil); err != nil {
		t.Fatal(err)
	}
	return ep
}

func TestShareOpsRoundTrip(t *testing.T) {
	nf := &netFake{}
	ep := serveTemp(t, nf, false)
	to := []Grantee{{Type: "user", ID: "ali@x.com"}}
	r, err := Call(ep, Request{Op: "share_create", Share: &ShareSpec{Name: "db", Port: 5432, To: to}})
	if err != nil || len(r.Shares) != 1 || r.Shares[0].ID != "s1" || r.Shares[0].Persistent {
		t.Fatalf("create: %v %+v", err, r)
	}
	r, err = Call(ep, Request{Op: "share_list"})
	if err != nil || len(r.Shares) != 1 || r.Shares[0].Name != "db" {
		t.Fatalf("list: %v %+v", err, r)
	}
	r, err = Call(ep, Request{Op: "share_delete", ShareID: "db"})
	if err != nil || r.Shares[0].ID != "s1" {
		t.Fatalf("delete: %v %+v", err, r)
	}
	if _, err = Call(ep, Request{Op: "share_delete", ShareID: "db"}); err == nil {
		t.Error("olmayan paylasim silme hata vermeliydi")
	}
	if _, err = Call(ep, Request{Op: "share_create", Share: &ShareSpec{Port: 13, To: to}}); err == nil || err.Error() != "port yasak (policy)" {
		t.Errorf("hata aktarimi: %v", err)
	}
	if _, err = Call(ep, Request{Op: "share_create"}); err == nil {
		t.Error("paylasim bilgisi eksik hata vermeliydi")
	}
	r, err = Call(ep, Request{Op: "access_open", Target: "db", Local: "127.0.0.1:15432"})
	if err != nil || r.Local != "127.0.0.1:15432" {
		t.Fatalf("access_open: %v %+v", err, r)
	}
	if _, err = Call(ep, Request{Op: "access_close", Local: "127.0.0.1:15432"}); err != nil {
		t.Fatalf("access_close: %v", err)
	}
	if _, err = Call(ep, Request{Op: "access_open"}); err == nil {
		t.Error("eksik hedef hata vermeliydi")
	}
}

func TestSharePersistDefaultsByEndpoint(t *testing.T) {
	to := []Grantee{{Type: "device", ID: "nas"}}
	fg := &netFake{}
	_, err := Call(serveTemp(t, fg, false), Request{Op: "share_create", Share: &ShareSpec{Port: 1, To: to}})
	if err != nil || fg.last.Persist {
		t.Errorf("on plan ajan varsayilan gecici olmali: %v %+v", err, fg.last)
	}
	svc := &netFake{}
	_, err = Call(serveTemp(t, svc, true), Request{Op: "share_create", Share: &ShareSpec{Port: 1, To: to}})
	if err != nil || !svc.last.Persist {
		t.Errorf("servis ajan varsayilan kalici olmali: %v %+v", err, svc.last)
	}
}

func TestShareOpsWithoutNetController(t *testing.T) {
	ep := serveTemp(t, &fake{}, false) // NetController degil
	_, err := Call(ep, Request{Op: "share_list"})
	if err == nil || err.Error() != ErrNoNet.Error() {
		t.Fatalf("beklenen ErrNoNet, gelen %v", err)
	}
}

func TestIsUnsupported(t *testing.T) {
	if !IsUnsupported(errors.New("bilinmeyen islem")) || IsUnsupported(nil) || IsUnsupported(errors.New("x")) {
		t.Error("IsUnsupported yanlis")
	}
}
