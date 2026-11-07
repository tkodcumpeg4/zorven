//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// Servis kipi: masaustu, ayni makinedeki CLI ajanina/servise localctl IPC ile
// baglanir (CLI'deki `zorven share` / `zorven access` yolu). Gercek bir yerel
// kontrol soketi uzerinde sahte denetleyiciyle denenir.

// ipcCtl, localctl.Controller + NetController + NetStatuser.
type ipcCtl struct{ *fakeNet }

func (ipcCtl) Current() localctl.Caps                   { return localctl.Caps{} }
func (ipcCtl) Set(_, _ *bool) localctl.Caps             { return localctl.Caps{} }
func (c ipcCtl) NetPaths() ([]localctl.PathInfo, error) { return c.paths, nil }

// plainCtl, Zorven Net'i olmayan (eski / --no-net) ajan.
type plainCtl struct{}

func (plainCtl) Current() localctl.Caps       { return localctl.Caps{} }
func (plainCtl) Set(_, _ *bool) localctl.Caps { return localctl.Caps{} }

func serveCtl(t *testing.T, ctl localctl.Controller, service bool) {
	t.Helper()
	dir, err := os.MkdirTemp("", "zv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ep := localctl.Endpoint{Path: filepath.Join(dir, "d", "ctl.sock"), Service: service}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := localctl.Serve(ctx, ep, ctl, "t", nil); err != nil {
		t.Fatal(err)
	}
	old := findCtlAgent
	findCtlAgent = func() (*localctl.Endpoint, *localctl.Response, error) { return &ep, &localctl.Response{}, nil }
	t.Cleanup(func() { findCtlAgent = old })
}

func TestServiceModeShareAndAccess(t *testing.T) {
	f := &fakeNet{
		shares: []localctl.ShareInfo{{ID: "s0", Name: "web", Port: 80, Proto: "tcp", Allow: []string{"100.64.0.2"}}},
		paths:  []localctl.PathInfo{{Name: "nas", IP: "100.64.0.2", Online: true, Path: "relay"}},
	}
	serveCtl(t, ipcCtl{f}, true)
	a := &App{cfg: defaultConfig()}

	v := a.NetShareState()
	if !v.OK || v.Mode != netModeService || v.CanList || !v.PersistForced {
		t.Fatalf("servis kipi durumu: %+v", v)
	}
	if len(v.Shares) != 1 || v.Shares[0].Name != "web" || v.Shares[0].Peers != 1 || len(v.Devices) != 1 || v.Devices[0] != "nas" {
		t.Fatalf("liste IPC'den gelmedi: %+v", v)
	}

	// Olusturma: istek ajana aynen gider; servis ajaninda her zaman kalici, masaustu kaydi tutmaz.
	r := a.NetShareCreate(NetShareRequest{Port: 5432, Name: "db", To: "ali@x.com, group:everyone", Expires: "4h"})
	if !r.OK || r.Share == nil || r.Share.Name != "db" {
		t.Fatalf("create: %+v", r)
	}
	if len(f.created) != 1 {
		t.Fatalf("ajana ulasmadi: %+v", f.created)
	}
	c := f.created[0]
	if c.Port != 5432 || c.Proto != "tcp" || c.ExpiresSec != 14400 || len(c.To) != 2 || c.To[1].Type != "group" || !c.Persist {
		t.Errorf("IPC istegi: %+v", c)
	}
	if len(a.nt.shares) != 0 {
		t.Errorf("servis kipinde gecici kayit tutulmamali: %+v", a.nt.shares)
	}
	if v = a.NetShareState(); len(v.Shares) != 2 {
		t.Errorf("yeni paylasim listede olmali: %+v", v.Shares)
	}

	// Silme.
	if r = a.NetShareDelete("db"); !r.OK || r.Share == nil || r.Share.Port != 5432 {
		t.Fatalf("delete: %+v", r)
	}
	if r = a.NetShareDelete("db"); r.OK || r.Error == "" {
		t.Errorf("olmayan paylasim: %+v", r)
	}

	// Erisim: ac, kayit tut, kapat.
	r = a.NetAccessOpen("nas-db", 15432)
	if !r.OK || r.Local != "127.0.0.1:15432" || f.opened["127.0.0.1:15432"] != "nas-db" {
		t.Fatalf("open: %+v %v", r, f.opened)
	}
	if rows := a.nt.bindRows(netModeService); len(rows) != 1 || rows[0].local != "127.0.0.1:15432" {
		t.Errorf("kayit: %+v", rows)
	}
	if r = a.NetAccessClose("127.0.0.1:15432"); !r.OK || len(f.opened) != 0 || len(a.nt.bindRows(netModeService)) != 0 {
		t.Fatalf("close: %+v", r)
	}
	// Hedef bossa ajan hatasi: IPC hata metni aynen kullaniciya gider.
	f.openErr = errors.New("erisilebilir paylasim bulunamadi: yok")
	if r = a.NetAccessOpen("yok", 0); r.OK || r.Error != "erisilebilir paylasim bulunamadi: yok" {
		t.Errorf("hata aktarimi: %+v", r)
	}
}

func TestServiceModeForegroundAgentNotForcedPersistent(t *testing.T) {
	f := &fakeNet{}
	serveCtl(t, ipcCtl{f}, false) // kullanici CLI ajani (servis degil)
	a := &App{cfg: defaultConfig()}
	if v := a.NetShareState(); !v.OK || v.PersistForced {
		t.Fatalf("on plan ajan: kalicilik zorunlu olmamali: %+v", v)
	}
	if r := a.NetShareCreate(NetShareRequest{Port: 80, To: "nas"}); !r.OK || f.created[0].Persist {
		t.Errorf("on plan ajanda varsayilan gecici: %+v %+v", r, f.created)
	}
	if r := a.NetShareCreate(NetShareRequest{Port: 81, To: "nas", Persist: true}); !r.OK || !f.created[1].Persist {
		t.Errorf("--persist istegi aktarilmali: %+v", f.created)
	}
}

func TestServiceModeWithoutNet(t *testing.T) {
	serveCtl(t, plainCtl{}, false)
	a := &App{cfg: defaultConfig()}
	v := a.NetShareState()
	if v.OK || v.Reason != nsReasonNoNet || v.Mode != netModeService {
		t.Fatalf("Zorven Net'siz ajan: %+v", v)
	}
	if r := a.NetShareCreate(NetShareRequest{Port: 80, To: "nas"}); r.OK || r.Error != tm("ns.err.nonet") {
		t.Errorf("create: %+v", r)
	}
	if r := a.NetAccessOpen("db", 0); r.OK || r.Error != tm("ns.err.nonet") {
		t.Errorf("open: %+v", r)
	}
}
