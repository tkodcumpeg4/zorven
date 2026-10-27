package localctl

import (
	"context"
	"errors"
	"testing"
)

type tunFake struct {
	fake
	active bool
}

func (t *tunFake) NetUp(context.Context) (TunInfo, error) {
	if t.active {
		return TunInfo{Active: true}, nil
	}
	t.active = true
	return TunInfo{Active: true, Interface: "zorven0", IP: "100.64.0.10", ServerTun: true, Peers: []PathInfo{{Name: "nas", IP: "100.64.0.20", Path: "direct", RTTMs: 4}}}, nil
}
func (t *tunFake) NetDown(context.Context) (TunInfo, error) {
	t.active = false
	return TunInfo{}, nil
}
func (t *tunFake) NetTunStatus() (TunInfo, error) {
	if !t.active {
		return TunInfo{ServerTun: true}, nil
	}
	return TunInfo{Active: true, Interface: "zorven0"}, nil
}

type tunFail struct{ fake }

func (*tunFail) NetUp(context.Context) (TunInfo, error) {
	return TunInfo{}, errors.New("TUN arayuzu olusturulamadi: yetki yok")
}
func (*tunFail) NetDown(context.Context) (TunInfo, error) { return TunInfo{}, nil }
func (*tunFail) NetTunStatus() (TunInfo, error)           { return TunInfo{}, nil }

func TestTunOpsRoundTrip(t *testing.T) {
	ep := serveTemp(t, &tunFake{}, true)
	r, err := Call(ep, Request{Op: "net_status"})
	if err != nil || r.Tun == nil || r.Tun.Active || !r.Tun.ServerTun || !r.Tun.Service {
		t.Fatalf("status: %v %+v", err, r.Tun)
	}
	r, err = Call(ep, Request{Op: "net_up"})
	if err != nil || !r.Tun.Active || r.Tun.IP != "100.64.0.10" || len(r.Tun.Peers) != 1 || r.Tun.Peers[0].Path != "direct" {
		t.Fatalf("up: %v %+v", err, r.Tun)
	}
	if r, err = Call(ep, Request{Op: "net_status"}); err != nil || !r.Tun.Active {
		t.Fatalf("status(up): %v %+v", err, r.Tun)
	}
	if r, err = Call(ep, Request{Op: "net_down"}); err != nil || r.Tun.Active {
		t.Fatalf("down: %v %+v", err, r.Tun)
	}
}

func TestTunOpsErrorsAndUnsupported(t *testing.T) {
	ep := serveTemp(t, &tunFail{}, false)
	if _, err := Call(ep, Request{Op: "net_up"}); err == nil || err.Error() != "TUN arayuzu olusturulamadi: yetki yok" {
		t.Fatalf("hata aktarimi: %v", err)
	}
	// TunController uygulamayan eski ajan: "bilinmeyen islem".
	old := serveTemp(t, &fake{}, false)
	_, err := Call(old, Request{Op: "net_up"})
	if !IsUnsupported(err) {
		t.Fatalf("eski ajan desteklenmiyor olarak donmeli: %v", err)
	}
}
