package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// fakeNet, localctl.NetController'in sahtesi (+ erisilebilir liste ve esler).
type fakeNet struct {
	mu      sync.Mutex
	shares  []localctl.ShareInfo
	in      []protocol.NetShareIn
	paths   []localctl.PathInfo
	opened  map[string]string // yerel adres -> hedef
	created []localctl.ShareSpec
	deleted []string
	// listErrs: ShareList'in sirayla donecegi hatalar (bitince shares doner).
	listErrs []error
	createFn func(sp localctl.ShareSpec) (localctl.ShareInfo, error)
	openErr  error
	closeErr error
	opens    []string // AccessOpen cagrilari "hedef@adres"
}

func (f *fakeNet) ShareCreate(_ context.Context, sp localctl.ShareSpec) (localctl.ShareInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, sp)
	if f.createFn != nil {
		return f.createFn(sp)
	}
	si := localctl.ShareInfo{ID: fmt.Sprintf("s%d", len(f.created)), Name: sp.Name, Port: sp.Port, Proto: sp.Proto, To: sp.To, Persistent: sp.Persist}
	if si.Name == "" {
		si.Name = fmt.Sprintf("dev-%d", sp.Port)
	}
	if sp.ExpiresSec > 0 {
		si.ExpiresUnix = time.Now().Add(time.Duration(sp.ExpiresSec) * time.Second).Unix()
	}
	f.shares = append(f.shares, si)
	return si, nil
}

func (f *fakeNet) ShareDelete(_ context.Context, idOrName string) (localctl.ShareInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, idOrName)
	for i, s := range f.shares {
		if s.ID == idOrName || s.Name == idOrName {
			f.shares = append(f.shares[:i], f.shares[i+1:]...)
			return s, nil
		}
	}
	return localctl.ShareInfo{}, errors.New("paylasim bulunamadi: " + idOrName)
}

func (f *fakeNet) ShareList() ([]localctl.ShareInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.listErrs) > 0 {
		err := f.listErrs[0]
		f.listErrs = f.listErrs[1:]
		return nil, err
	}
	return append([]localctl.ShareInfo(nil), f.shares...), nil
}

func (f *fakeNet) AccessOpen(_ context.Context, target, local string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens = append(f.opens, target+"@"+local)
	if f.openErr != nil {
		return "", f.openErr
	}
	if f.opened == nil {
		f.opened = map[string]string{}
	}
	// "127.0.0.1:" -> paylasimin portu (ajanin davranisi).
	addr := local
	if strings.HasSuffix(local, ":") {
		addr = local + "5432"
	}
	f.opened[addr] = target
	return addr, nil
}

func (f *fakeNet) AccessClose(local string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closeErr != nil {
		return f.closeErr
	}
	if _, ok := f.opened[local]; !ok {
		return errors.New("bu adreste IPC ile acilmis erisim dinleyicisi yok: " + local)
	}
	delete(f.opened, local)
	return nil
}

// embeddedHook, testte arka ucu sahte denetleyiciyle (gomulu kip) degistirir.
func embeddedHook(t *testing.T, f *fakeNet, listIn bool) {
	t.Helper()
	old := netBackendHook
	netBackendHook = func(*App) (*netBackend, string, string) {
		be := &netBackend{ctl: f, mode: netModeEmbedded, paths: func() ([]localctl.PathInfo, error) { return f.paths, nil }}
		if listIn {
			be.listIn = func() ([]protocol.NetShareIn, error) { return f.in, nil }
		}
		return be, "", ""
	}
	t.Cleanup(func() { netBackendHook = old })
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseGrantee(t *testing.T) {
	cases := map[string]localctl.Grantee{
		"user:ali@x.com":    {Type: "user", ID: "ali@x.com"},
		"user:u_123":        {Type: "user", ID: "u_123"},
		"group:role:admin":  {Type: "group", ID: "role:admin"},
		"group:everyone":    {Type: "group", ID: "everyone"},
		"device:nas":        {Type: "device", ID: "nas"},
		"everyone":          {Type: "group", ID: "everyone"},
		"role:admin":        {Type: "group", ID: "role:admin"},
		"ali@x.com":         {Type: "user", ID: "ali@x.com"},
		"  ali@firma.com  ": {Type: "user", ID: "ali@firma.com"},
		"nas":               {Type: "device", ID: "nas"},
	}
	for in, want := range cases {
		if got := parseGrantee(in); got != want {
			t.Errorf("parseGrantee(%q) = %+v, beklenen %+v", in, got, want)
		}
	}
}

func TestParseGrantees(t *testing.T) {
	got := parseGrantees("ali@x.com, group:everyone;device:nas  user:u1\nnas2,,")
	want := []localctl.Grantee{
		{Type: "user", ID: "ali@x.com"}, {Type: "group", ID: "everyone"}, {Type: "device", ID: "nas"},
		{Type: "user", ID: "u1"}, {Type: "device", ID: "nas2"},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v != %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] %+v != %+v", i, got[i], want[i])
		}
	}
	if g := parseGrantees("  ,; \n"); len(g) != 0 {
		t.Errorf("bos giris alici uretmemeli: %+v", g)
	}
}

func TestBuildShareSpec(t *testing.T) {
	sp, err := buildShareSpec(NetShareRequest{Port: 5432, Name: " db ", To: "ali@x.com, group:everyone", Expires: "24h", Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Name != "db" || sp.Port != 5432 || sp.Proto != "tcp" || !sp.Persist || sp.ExpiresSec != 86400 || len(sp.To) != 2 {
		t.Fatalf("beklenmeyen istek: %+v", sp)
	}
	if sp, err = buildShareSpec(NetShareRequest{Port: 25565, Proto: "UDP", To: "device:nas", Expires: "1m"}); err != nil || sp.Proto != "udp" || sp.ExpiresSec != 60 || sp.Persist {
		t.Fatalf("udp / 1m: %+v %v", sp, err)
	}
	if sp, err = buildShareSpec(NetShareRequest{Port: 80, To: "nas"}); err != nil || sp.ExpiresSec != 0 {
		t.Fatalf("suresiz: %+v %v", sp, err)
	}

	for _, p := range []int{0, -1, 65536, 70000} {
		if _, err := buildShareSpec(NetShareRequest{Port: p, To: "nas"}); err == nil || err.Error() != tm("app.badPort") {
			t.Errorf("port %d reddedilmeliydi: %v", p, err)
		}
	}
	if _, err := buildShareSpec(NetShareRequest{Port: 80, Proto: "icmp", To: "nas"}); err == nil || !strings.Contains(err.Error(), "icmp") {
		t.Errorf("gecersiz protokol: %v", err)
	}
	if _, err := buildShareSpec(NetShareRequest{Port: 80, To: " , "}); err == nil || err.Error() != tm("ns.err.to") {
		t.Errorf("alicisiz istek reddedilmeli: %v", err)
	}
	for _, e := range []string{"30s", "59s", "abc", "-1h", "24"} {
		if _, err := buildShareSpec(NetShareRequest{Port: 80, To: "nas", Expires: e}); err == nil || !strings.Contains(err.Error(), e) {
			t.Errorf("sure %q reddedilmeliydi: %v", e, err)
		}
	}
}

func TestAccessLocalAddr(t *testing.T) {
	cases := []struct {
		target string
		local  int
		want   string
	}{
		{"db", 0, "127.0.0.1:"},
		{"db:5432", 0, "127.0.0.1:5432"},
		{"db", 15432, "127.0.0.1:15432"},
		{"db:5432", 15432, "127.0.0.1:15432"}, // --local hedefteki porttan once gelir
		{"db:abc", 0, "127.0.0.1:"},
		{"db:0", 0, "127.0.0.1:"},
	}
	for _, c := range cases {
		got, err := accessLocalAddr(c.target, c.local)
		if err != nil || got != c.want {
			t.Errorf("accessLocalAddr(%q,%d) = %q, %v; beklenen %q", c.target, c.local, got, err, c.want)
		}
	}
	for _, l := range []int{-1, 65536} {
		if _, err := accessLocalAddr("db", l); err == nil {
			t.Errorf("yerel port %d reddedilmeliydi", l)
		}
	}
}

func TestNetShareStateNoBackend(t *testing.T) {
	old := findCtlAgent
	findCtlAgent = func() (*localctl.Endpoint, *localctl.Response, error) { return nil, nil, nil }
	t.Cleanup(func() { findCtlAgent = old })

	a := &App{cfg: defaultConfig()}
	v := a.NetShareState()
	if v.OK || v.Mode != netModeNone || v.Reason != nsReasonConnect || v.Shares == nil || v.Access == nil || v.Binds == nil {
		t.Fatalf("ajan yokken 'connect' beklenirdi: %+v", v)
	}
	// Eylemler acik hata doner, panik olmaz.
	if r := a.NetShareCreate(NetShareRequest{Port: 80, To: "nas"}); r.OK || r.Error != tm("ns.err.connect") {
		t.Errorf("create: %+v", r)
	}
	if r := a.NetAccessOpen("db", 0); r.OK || r.Error != tm("ns.err.connect") {
		t.Errorf("open: %+v", r)
	}

	// Ajan var ama konusulamiyor (izin): denied + yukseltme bayragi.
	findCtlAgent = func() (*localctl.Endpoint, *localctl.Response, error) {
		return nil, nil, errors.New("pipe: Access is denied.")
	}
	v = a.NetShareState()
	if v.OK || v.Reason != nsReasonDenied || !v.NeedElevation {
		t.Fatalf("izin reddi: %+v", v)
	}
	findCtlAgent = func() (*localctl.Endpoint, *localctl.Response, error) {
		return nil, nil, errors.New("beklenmeyen")
	}
	if v = a.NetShareState(); v.Reason != nsReasonError || v.Detail != "beklenmeyen" || v.NeedElevation {
		t.Fatalf("genel hata: %+v", v)
	}
}

func TestNetBackendSelection(t *testing.T) {
	old := findCtlAgent
	t.Cleanup(func() { findCtlAgent = old })
	svc := &localctl.Endpoint{Path: "/tmp/x.sock", Service: true}
	findCtlAgent = func() (*localctl.Endpoint, *localctl.Response, error) { return svc, &localctl.Response{}, nil }

	a := &App{cfg: defaultConfig()}
	// Gomulu ajan canliysa o secilir.
	a.ag = &agent.Agent{}
	a.status = agent.Status{State: agent.StateConnected}
	be, _, _ := a.netBackendNow()
	if be == nil || be.mode != netModeEmbedded || be.listIn == nil || be.service {
		t.Fatalf("gomulu bekleniyordu: %+v", be)
	}
	// Gomulu ajan oldu (fatal) ise ayni makinedeki servise dusulur.
	a.status = agent.Status{State: agent.StateFatal}
	be, _, _ = a.netBackendNow()
	if be == nil || be.mode != netModeService || !be.service || be.listIn != nil || be.paths == nil {
		t.Fatalf("servis bekleniyordu: %+v", be)
	}
	// Ajan yok, servis yok: neden connect.
	a.ag = nil
	findCtlAgent = func() (*localctl.Endpoint, *localctl.Response, error) { return nil, nil, nil }
	if be, reason, _ := a.netBackendNow(); be != nil || reason != nsReasonConnect {
		t.Fatalf("be=%v reason=%q", be, reason)
	}
}

func TestNetShareStateRows(t *testing.T) {
	f := &fakeNet{
		shares: []localctl.ShareInfo{
			{ID: "s1", Name: "db", Port: 5432, Proto: "tcp", To: []localctl.Grantee{{Type: "user", ID: "ali@x.com"}}, Allow: []string{"100.64.0.5"}, ExpiresUnix: 1893456000, Path: "direct", Persistent: true},
			{ID: "s2", Name: "web", Port: 8080, Proto: "tcp", To: []localctl.Grantee{{Type: "group", ID: "everyone"}}},
			{ID: "s3", Name: "panel-kurdu", Port: 9000, Proto: "udp", Allow: []string{"100.64.0.5", "100.64.0.6"}},
		},
		in: []protocol.NetShareIn{
			{ID: "i1", Name: "nas-db", OwnerName: "nas", OwnerIP: "100.64.0.9", Port: 5432, Proto: "tcp"},
			{ID: "i2", Name: "cam", OwnerName: "lab", OwnerIP: "100.64.0.7", Port: 554, Proto: "udp", ExpiresUnix: 1893456000},
		},
		paths: []localctl.PathInfo{
			{Name: "nas", IP: "100.64.0.9", Online: true, Path: "direct", RTTMs: 12},
			{Name: "lab", IP: "100.64.0.7", Online: false, Path: "relay"},
		},
	}
	embeddedHook(t, f, true)
	a := &App{cfg: defaultConfig()}
	a.nt.addBind("nas-db", "127.0.0.1:15432", netModeEmbedded)
	a.nt.addBind("zzz", "127.0.0.1:2", netModeEmbedded)
	a.nt.setBindErr("127.0.0.1:2", "hata")

	v := a.NetShareState()
	if !v.OK || v.Mode != netModeEmbedded || !v.CanList || v.PersistForced {
		t.Fatalf("durum: %+v", v)
	}
	if len(v.Shares) != 3 {
		t.Fatalf("paylasim sayisi: %+v", v.Shares)
	}
	s := v.Shares[0]
	if s.Life != "persistent" || len(s.To) != 1 || s.To[0] != "user:ali@x.com" || s.Peers != 1 || s.Path != "direct" || s.ExpiresUnix != 1893456000 {
		t.Errorf("satir 0: %+v", s)
	}
	if v.Shares[1].Life != "temporary" {
		t.Errorf("bu ajanin kurdugu gecici paylasim: %+v", v.Shares[1])
	}
	if v.Shares[2].Life != "" || v.Shares[2].Peers != 2 || v.Shares[2].Proto != "udp" {
		t.Errorf("kaynagi bilinmeyen paylasim: %+v", v.Shares[2])
	}
	if len(v.Access) != 2 {
		t.Fatalf("erisim sayisi: %+v", v.Access)
	}
	nas := v.Access[0]
	if nas.Owner != "nas" || !nas.Online || nas.Path != "direct" || nas.RTTMs != 12 || len(nas.Bound) != 1 || nas.Bound[0] != "127.0.0.1:15432" {
		t.Errorf("nas satiri: %+v", nas)
	}
	if cam := v.Access[1]; cam.Online || cam.Path != "relay" || len(cam.Bound) != 0 || cam.ExpiresUnix == 0 {
		t.Errorf("cam satiri: %+v", cam)
	}
	if len(v.Binds) != 2 || v.Binds[0].Target != "nas-db" || v.Binds[1].Error != "hata" {
		t.Errorf("erisimler: %+v", v.Binds)
	}
	if len(v.Devices) != 2 || v.Devices[0] != "nas" {
		t.Errorf("cihazlar: %+v", v.Devices)
	}

	// Liste sunulmayan kip (servis IPC'si) CanList=false doner, erisilebilir bos kalir.
	embeddedHook(t, f, false)
	if v = a.NetShareState(); !v.OK || v.CanList || len(v.Access) != 0 {
		t.Errorf("listesiz kip: %+v", v)
	}
}

func TestNetShareStateReasons(t *testing.T) {
	f := &fakeNet{}
	embeddedHook(t, f, true)
	a := &App{cfg: defaultConfig()}

	// Baglanti yeni kuruldu: dugum henuz hazir degil.
	a.connAt = time.Now()
	f.listErrs = []error{localctl.ErrNoNet}
	if v := a.NetShareState(); v.OK || v.Reason != nsReasonPreparing {
		t.Errorf("preparing: %+v", v)
	}
	// Uzun suredir bagli ama Zorven Net yok.
	a.connAt = time.Now().Add(-time.Minute)
	f.listErrs = []error{localctl.ErrNoNet}
	if v := a.NetShareState(); v.OK || v.Reason != nsReasonNoNet {
		t.Errorf("nonet: %+v", v)
	}
	// IPC'den ErrNoNet metni (errors.New ile sarili) de taninir.
	f.listErrs = []error{errors.New(localctl.ErrNoNet.Error())}
	if v := a.NetShareState(); v.Reason != nsReasonNoNet {
		t.Errorf("nonet metni: %+v", v)
	}
	f.listErrs = []error{errors.New("bilinmeyen islem")}
	if v := a.NetShareState(); v.Reason != nsReasonOld {
		t.Errorf("old: %+v", v)
	}
	f.listErrs = []error{errors.New("yetkisiz: baglanti reddedildi")}
	if v := a.NetShareState(); v.Reason != nsReasonDenied || !v.NeedElevation {
		t.Errorf("denied: %+v", v)
	}
	f.listErrs = []error{errors.New("tuhaf hata")}
	if v := a.NetShareState(); v.Reason != nsReasonError || v.Detail != "tuhaf hata" {
		t.Errorf("error: %+v", v)
	}
	// Hata gecince normale doner; baglanti kopukken bile kayitli erisimler listelenir.
	a.nt.addBind("db", "127.0.0.1:1", netModeEmbedded)
	f.listErrs = []error{localctl.ErrNoNet}
	if v := a.NetShareState(); v.OK || len(v.Binds) != 1 {
		t.Errorf("kayitli erisimler gorunmeli: %+v", v)
	}
	if v := a.NetShareState(); !v.OK {
		t.Errorf("normale donmeli: %+v", v)
	}
}

func TestNetShareCreateDeleteTracking(t *testing.T) {
	f := &fakeNet{}
	embeddedHook(t, f, true)
	a := &App{cfg: defaultConfig()}

	// Gecici paylasim: ajana tam istek gider ve yeniden kurulmak uzere kaydedilir.
	r := a.NetShareCreate(NetShareRequest{Port: 5432, Name: "db", To: "ali@x.com", Expires: "2h"})
	if !r.OK || r.Share == nil || r.Share.Name != "db" {
		t.Fatalf("create: %+v", r)
	}
	if len(f.created) != 1 {
		t.Fatalf("ajan cagrisi: %+v", f.created)
	}
	c := f.created[0]
	if c.Name != "db" || c.Port != 5432 || c.Proto != "tcp" || c.Persist || c.ExpiresSec != 7200 || len(c.To) != 1 || c.To[0].Type != "user" {
		t.Errorf("ajana giden istek: %+v", c)
	}
	if len(a.nt.shares) != 1 || a.nt.shares[0].expiresAt.IsZero() {
		t.Fatalf("gecici paylasim kaydedilmeli: %+v", a.nt.shares)
	}
	// Adi bos istek: ajanin turettigi ad saklanir.
	if r = a.NetShareCreate(NetShareRequest{Port: 8080, To: "nas"}); !r.OK || r.Share.Name != "dev-8080" {
		t.Fatalf("adsiz create: %+v", r)
	}
	if got := a.nt.shares[1].spec.Name; got != "dev-8080" {
		t.Errorf("turetilen ad saklanmadi: %q", got)
	}
	// Kalici paylasim sunucuda kalir: kaydedilmez.
	if r = a.NetShareCreate(NetShareRequest{Port: 9000, To: "nas", Persist: true}); !r.OK {
		t.Fatalf("kalici create: %+v", r)
	}
	if len(a.nt.shares) != 2 {
		t.Errorf("kalici paylasim kaydedilmemeli: %d", len(a.nt.shares))
	}
	// Ayni port+proto tekrar kurulursa kayit degisir, cogalmaz.
	a.NetShareCreate(NetShareRequest{Port: 5432, Name: "db2", To: "ali@x.com"})
	if len(a.nt.shares) != 2 {
		t.Errorf("ayni port tek kayit olmali: %d", len(a.nt.shares))
	}

	// Silme: ajandan ve kayittan dusurur.
	if r = a.NetShareDelete("dev-8080"); !r.OK {
		t.Fatalf("delete: %+v", r)
	}
	for _, w := range a.nt.shares {
		if w.spec.Port == 8080 {
			t.Errorf("silinen paylasim kayitta kaldi")
		}
	}
	if r = a.NetShareDelete("yok"); r.OK || !strings.Contains(r.Error, "yok") {
		t.Errorf("olmayan paylasim: %+v", r)
	}
	if r = a.NetShareDelete("  "); r.OK || r.Error != tm("ns.err.share") {
		t.Errorf("bos kimlik: %+v", r)
	}

	// Dogrulama hatasi ajana gitmeden doner.
	n := len(f.created)
	if r = a.NetShareCreate(NetShareRequest{Port: 0, To: "nas"}); r.OK || len(f.created) != n {
		t.Errorf("gecersiz port ajana gitti: %+v", r)
	}
	// Ajan hatasi cevrilir.
	f.createFn = func(localctl.ShareSpec) (localctl.ShareInfo, error) { return localctl.ShareInfo{}, localctl.ErrNoNet }
	if r = a.NetShareCreate(NetShareRequest{Port: 1234, To: "nas"}); r.OK || r.Error != tm("ns.err.nonet") {
		t.Errorf("ErrNoNet cevirisi: %+v", r)
	}
	f.createFn = func(localctl.ShareSpec) (localctl.ShareInfo, error) {
		return localctl.ShareInfo{}, errors.New("bu port zaten paylasiliyor (port_shared)")
	}
	if r = a.NetShareCreate(NetShareRequest{Port: 1234, To: "nas"}); r.OK || !strings.Contains(r.Error, "port_shared") {
		t.Errorf("sunucu hatasi oldugu gibi donmeli: %+v", r)
	}
}

func TestNetAccessOpenClose(t *testing.T) {
	f := &fakeNet{}
	embeddedHook(t, f, true)
	a := &App{cfg: defaultConfig()}

	r := a.NetAccessOpen(" db ", 15432)
	if !r.OK || r.Local != "127.0.0.1:15432" || f.opens[0] != "db@127.0.0.1:15432" {
		t.Fatalf("open: %+v %v", r, f.opens)
	}
	// Yerel port bos: ajan paylasimin portunu kullanir, donen adres kaydedilir.
	if r = a.NetAccessOpen("nas-db", 0); !r.OK || r.Local != "127.0.0.1:5432" || f.opens[1] != "nas-db@127.0.0.1:" {
		t.Fatalf("varsayilan port: %+v %v", r, f.opens)
	}
	if rows := a.nt.bindRows(netModeEmbedded); len(rows) != 2 || rows[1].target != "nas-db" || rows[1].local != "127.0.0.1:5432" {
		t.Fatalf("kayitlar: %+v", rows)
	}
	// Ayni adrese tekrar acmak kaydi cogaltmaz.
	a.NetAccessOpen("db2", 15432)
	if rows := a.nt.bindRows(netModeEmbedded); len(rows) != 2 || rows[0].target != "db2" {
		t.Fatalf("ayni adres tek kayit olmali: %+v", rows)
	}
	// Dogrulama.
	if r = a.NetAccessOpen("  ", 0); r.OK || r.Error != tm("ns.err.target") {
		t.Errorf("bos hedef: %+v", r)
	}
	if r = a.NetAccessOpen("db", -5); r.OK || r.Error != tm("ns.err.local") {
		t.Errorf("gecersiz yerel port: %+v", r)
	}
	// Ajan hatasi: kayit olusmaz.
	f.openErr = errors.New("dinlenemedi: listen tcp 127.0.0.1:80: bind: address already in use")
	if r = a.NetAccessOpen("x", 80); r.OK || !strings.Contains(r.Error, "address already in use") || len(a.nt.bindRows(netModeEmbedded)) != 2 {
		t.Errorf("acma hatasi: %+v", r)
	}
	f.openErr = nil

	// Kapatma.
	if r = a.NetAccessClose("127.0.0.1:15432"); !r.OK || len(a.nt.bindRows(netModeEmbedded)) != 1 {
		t.Fatalf("close: %+v", r)
	}
	// Dinleyici zaten kapanmis (ajan yeniden baglandi): kayit dusurulur, basari doner.
	f.opened = map[string]string{}
	if r = a.NetAccessClose("127.0.0.1:5432"); !r.OK || len(a.nt.bindRows(netModeEmbedded)) != 0 {
		t.Fatalf("bayat kayit: %+v rows=%+v", r, a.nt.bindRows(netModeEmbedded))
	}
	// Baska hata: kayit korunur.
	a.NetAccessOpen("db", 7000)
	f.closeErr = errors.New("yetkisiz: reddedildi")
	if r = a.NetAccessClose("127.0.0.1:7000"); r.OK || !strings.Contains(r.Error, "yetkisiz") || len(a.nt.bindRows(netModeEmbedded)) != 1 {
		t.Fatalf("kapatma hatasi: %+v", r)
	}
	if r = a.NetAccessClose(" "); r.OK {
		t.Errorf("bos adres: %+v", r)
	}
}

// Servis kipinde acilan erisimler gomulu kipte listelenmez ve gomulu ajan
// (yeniden) baglaninca onda yeniden acilmaz; tersi de gecerli.
func TestBindModesAreSeparate(t *testing.T) {
	oldPoll := netReadyPoll
	netReadyPoll = time.Millisecond
	t.Cleanup(func() { netReadyPoll = oldPoll })

	var nt netTrack
	nt.addBind("hizmet-db", "127.0.0.1:1", netModeService)
	nt.addBind("gomulu-db", "127.0.0.1:2", netModeEmbedded)
	if rows := nt.bindRows(netModeEmbedded); len(rows) != 1 || rows[0].target != "gomulu-db" {
		t.Fatalf("gomulu liste: %+v", rows)
	}
	if rows := nt.bindRows(netModeService); len(rows) != 1 || rows[0].target != "hizmet-db" {
		t.Fatalf("servis liste: %+v", rows)
	}
	f := &fakeNet{}
	nt.reapply(context.Background(), f, func() bool { return true }, testLogger())
	if len(f.opens) != 1 || f.opens[0] != "gomulu-db@127.0.0.1:2" {
		t.Fatalf("yalniz gomulu erisim yeniden acilmali: %v", f.opens)
	}
	// Ayni yerel adres baska kipte yeniden acilirsa kayit kip degistirir, cogalmaz.
	nt.addBind("x", "127.0.0.1:2", netModeService)
	if len(nt.binds) != 2 || len(nt.bindRows(netModeEmbedded)) != 0 {
		t.Fatalf("kayit tasinmali: %+v", nt.bindRows(netModeService))
	}
}

func TestObservePrunesVanishedShares(t *testing.T) {
	var nt netTrack
	nt.addShare(localctl.ShareSpec{Name: "a", Port: 1, Proto: "tcp"}, time.Time{})
	nt.addShare(localctl.ShareSpec{Name: "b", Port: 2, Proto: "tcp"}, time.Time{})
	nt.addShare(localctl.ShareSpec{Name: "c", Port: 3}, time.Time{}) // proto bos = tcp

	// a ve c goruldu; b henuz kurulmadi (listede yok) ama dusurulmemeli.
	nt.observe([]localctl.ShareInfo{{Port: 1, Proto: "tcp"}, {Port: 3, Proto: "tcp"}})
	if len(nt.shares) != 3 {
		t.Fatalf("hic gorulmeyen kayit dusurulmemeli: %d", len(nt.shares))
	}
	// a ayni baglantida kayboldu: baska yerden silinmis, kayittan dusur.
	nt.observe([]localctl.ShareInfo{{Port: 3, Proto: "tcp"}})
	if len(nt.shares) != 2 {
		t.Fatalf("kaybolan kayit dusurulmeli: %d", len(nt.shares))
	}
	// Yeniden baglanma: epoch artar, listede olmamasi silinmis sayilmaz.
	nt.bumpEpoch()
	nt.observe(nil)
	if len(nt.shares) != 2 {
		t.Fatalf("yeni baglantida eksiklik dusurmemeli: %d", len(nt.shares))
	}
}

func TestReapplyRecreatesTemporaryShares(t *testing.T) {
	oldPoll := netReadyPoll
	netReadyPoll = time.Millisecond
	t.Cleanup(func() { netReadyPoll = oldPoll })

	f := &fakeNet{
		// Dugum henuz hazir degil (iki kez), sonra sunucuda yalniz "kalan" duruyor.
		listErrs: []error{localctl.ErrNoNet, localctl.ErrNoNet},
		shares:   []localctl.ShareInfo{{ID: "x", Name: "kalan", Port: 2, Proto: "tcp"}},
	}
	var nt netTrack
	nt.addShare(localctl.ShareSpec{Name: "kayip", Port: 1, Proto: "tcp", To: []localctl.Grantee{{Type: "user", ID: "ali@x.com"}}}, time.Time{})
	nt.addShare(localctl.ShareSpec{Name: "kalan", Port: 2, Proto: "tcp"}, time.Time{})
	nt.addShare(localctl.ShareSpec{Name: "biten", Port: 3, Proto: "tcp"}, time.Now().Add(-time.Minute))
	nt.addShare(localctl.ShareSpec{Name: "sureli", Port: 4, Proto: "udp", ExpiresSec: 3600}, time.Now().Add(90*time.Second))
	nt.addBind("db", "127.0.0.1:15432", netModeEmbedded)

	nt.reapply(context.Background(), f, func() bool { return true }, testLogger())

	if len(f.created) != 2 {
		t.Fatalf("yalniz eksik ve suresi dolmamis paylasimlar kurulmali: %+v", f.created)
	}
	got := map[string]localctl.ShareSpec{}
	for _, c := range f.created {
		got[c.Name] = c
	}
	if k, ok := got["kayip"]; !ok || k.Port != 1 || len(k.To) != 1 {
		t.Errorf("kayip yeniden kurulmadi: %+v", got)
	}
	if s, ok := got["sureli"]; !ok || s.ExpiresSec < 80 || s.ExpiresSec > 90 || s.Proto != "udp" {
		t.Errorf("kalan sure hesabi: %+v", got["sureli"])
	}
	// Suresi dolan kayit dusurulur.
	for _, w := range nt.shares {
		if w.spec.Name == "biten" {
			t.Errorf("suresi dolan kayit silinmeliydi")
		}
	}
	if len(f.opens) != 1 || f.opens[0] != "db@127.0.0.1:15432" {
		t.Errorf("yerel erisim yeniden acilmadi: %v", f.opens)
	}
}

// readyFake, netmap gelmeden once bos liste donen, NetWaitReady'den sonra gercek
// listeyi veren gomulu ajan taklidi (dugum kaydi netmap'ten once yapilir).
type readyFake struct {
	*fakeNet
	waited bool
	waitFn func(ctx context.Context) error
}

func (r *readyFake) NetWaitReady(ctx context.Context) error {
	r.mu.Lock()
	r.waited = true
	fn := r.waitFn
	r.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (r *readyFake) ShareList() ([]localctl.ShareInfo, error) {
	r.mu.Lock()
	waited := r.waited
	r.mu.Unlock()
	if !waited {
		return nil, nil // netmap henuz uygulanmadi: kendi paylasimlarimiz gorunmuyor
	}
	return r.fakeNet.ShareList()
}

func TestReapplyWaitsForNetmapBeforeDeciding(t *testing.T) {
	f := &readyFake{fakeNet: &fakeNet{
		// Sunucuda kalmis (yeni baglanti eskinin yerini aldi) gecici paylasim.
		shares: []localctl.ShareInfo{{ID: "x", Name: "db", Port: 5432, Proto: "tcp"}},
	}}
	var nt netTrack
	nt.addShare(localctl.ShareSpec{Name: "db", Port: 5432, Proto: "tcp"}, time.Time{})
	nt.reapply(context.Background(), f, func() bool { return true }, testLogger())
	if !f.waited {
		t.Fatal("netmap beklenmedi")
	}
	if len(f.created) != 0 {
		t.Fatalf("sunucuda duran paylasim yeniden kurulmamali (cakisma): %+v", f.created)
	}

	// Dugum hazir olmazsa (zaman asimi) hicbir sey kurulmaz.
	f2 := &readyFake{fakeNet: &fakeNet{}, waitFn: func(ctx context.Context) error { return context.DeadlineExceeded }}
	var nt2 netTrack
	nt2.addShare(localctl.ShareSpec{Name: "x", Port: 1}, time.Time{})
	nt2.addBind("db", "127.0.0.1:1", netModeEmbedded)
	nt2.reapply(context.Background(), f2, func() bool { return true }, testLogger())
	if len(f2.created) != 0 || len(f2.opens) != 0 {
		t.Fatalf("hazir olmayan dugumde islem yapilmamali: %+v %v", f2.created, f2.opens)
	}
}

func TestReapplyFailuresAreRecorded(t *testing.T) {
	oldRetry := netBindRetry
	netBindRetry = time.Millisecond
	t.Cleanup(func() { netBindRetry = oldRetry })

	f := &fakeNet{
		openErr: errors.New("dinlenemedi: adres kullanimda"),
		createFn: func(localctl.ShareSpec) (localctl.ShareInfo, error) {
			return localctl.ShareInfo{}, errors.New("bu adda bir paylasim zaten var (name_taken)")
		},
	}
	var nt netTrack
	nt.addShare(localctl.ShareSpec{Name: "db", Port: 5432, Proto: "tcp"}, time.Time{})
	nt.addBind("db", "127.0.0.1:15432", netModeEmbedded)
	nt.reapply(context.Background(), f, func() bool { return true }, testLogger())

	if w := nt.warnings(); len(w) != 1 || !strings.Contains(w[0], "name_taken") || !strings.Contains(w[0], "db (5432/tcp)") {
		t.Errorf("paylasim uyarisi: %v", w)
	}
	if len(f.opens) != 5 {
		t.Errorf("yerel erisim 5 kez denenmeli: %d", len(f.opens))
	}
	if rows := nt.bindRows(netModeEmbedded); len(rows) != 1 || !strings.Contains(rows[0].err, "adres kullanimda") {
		t.Errorf("erisim hatasi kaydedilmedi: %+v", rows)
	}
	// Basarili denemede hata temizlenir.
	f.openErr = nil
	nt.reapply(context.Background(), f, func() bool { return true }, testLogger())
	if rows := nt.bindRows(netModeEmbedded); rows[0].err != "" {
		t.Errorf("hata temizlenmeli: %+v", rows)
	}
}

func TestReapplyStopsWhenAgentGone(t *testing.T) {
	oldPoll := netReadyPoll
	netReadyPoll = time.Millisecond
	t.Cleanup(func() { netReadyPoll = oldPoll })

	// Dugum hic hazir olmaz; ajan degisince beklemeden birakir.
	f := &fakeNet{listErrs: []error{localctl.ErrNoNet, localctl.ErrNoNet, localctl.ErrNoNet, localctl.ErrNoNet, localctl.ErrNoNet}}
	var nt netTrack
	nt.addShare(localctl.ShareSpec{Name: "db", Port: 5432}, time.Time{})
	calls := 0
	nt.reapply(context.Background(), f, func() bool { calls++; return calls < 2 }, testLogger())
	if len(f.created) != 0 {
		t.Errorf("ajan degisti: paylasim kurulmamali: %+v", f.created)
	}

	// Baglam bitince de birakir.
	f2 := &fakeNet{listErrs: []error{localctl.ErrNoNet, localctl.ErrNoNet, localctl.ErrNoNet}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { nt.reapply(ctx, f2, func() bool { return true }, testLogger()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("iptal edilen baglamda reapply donmedi")
	}

	// Kayit yoksa agi hic yoklamaz.
	var empty netTrack
	f3 := &fakeNet{listErrs: []error{errors.New("cagrilmamali")}}
	empty.reapply(context.Background(), f3, func() bool { return true }, nil)
	if len(f3.listErrs) != 1 {
		t.Errorf("kayit yokken ShareList cagrilmamali")
	}
}

// Baglanti her (yeniden) kurulusunda epoch artar; ayni durumun tekrar duyurulmasi
// (uzak oturum sayisi degisince) artirmaz.
func TestPublishConnectedBumpsEpochOnlyOnTransition(t *testing.T) {
	a := &App{cfg: defaultConfig(), log: testLogger(), prevState: agent.StateStopped}
	epoch := func() uint64 {
		a.nt.mu.Lock()
		defer a.nt.mu.Unlock()
		return a.nt.epoch
	}
	a.publish(agent.Status{State: agent.StateConnecting})
	if epoch() != 0 {
		t.Fatalf("baglanirken epoch artmamali: %d", epoch())
	}
	a.publish(agent.Status{State: agent.StateConnected})
	if epoch() != 1 {
		t.Fatalf("baglaninca epoch 1 olmali: %d", epoch())
	}
	a.publish(agent.Status{State: agent.StateConnected, TerminalActive: 1})
	if epoch() != 1 {
		t.Fatalf("ayni durum tekrari epoch artirmamali: %d", epoch())
	}
	a.publish(agent.Status{State: agent.StateReconnecting})
	a.publish(agent.Status{State: agent.StateConnected})
	if epoch() != 2 {
		t.Fatalf("yeniden baglaninca epoch 2 olmali: %d", epoch())
	}
}

func TestDisconnectClearsNetTracking(t *testing.T) {
	a := &App{cfg: defaultConfig(), log: testLogger()}
	a.nt.addShare(localctl.ShareSpec{Name: "db", Port: 5432}, time.Time{})
	a.nt.addBind("db", "127.0.0.1:1", netModeEmbedded)
	a.Disconnect()
	if len(a.nt.shares) != 0 || len(a.nt.binds) != 0 {
		t.Errorf("baglanti kesilince kayitlar silinmeli: %+v %+v", a.nt.shares, a.nt.binds)
	}
}

func TestNetErrText(t *testing.T) {
	if got := netErrText(errors.New("bilinmeyen islem")); got != tm("ns.err.old") {
		t.Errorf("old: %q", got)
	}
	if got := netErrText(fmt.Errorf("sarili: %w", localctl.ErrNoNet)); got != tm("ns.err.nonet") {
		t.Errorf("nonet: %q", got)
	}
	if got := netErrText(errors.New("yetkisiz: x")); !strings.Contains(got, "yetkisiz: x") || got == "yetkisiz: x" {
		t.Errorf("denied: %q", got)
	}
	if got := netErrText(errors.New("ham hata")); got != "ham hata" {
		t.Errorf("ham: %q", got)
	}
}

func TestShareMessagesInAllLanguages(t *testing.T) {
	for _, k := range []string{"ns.err.proto", "ns.err.to", "ns.err.expires", "ns.err.local", "ns.err.target", "ns.err.share",
		"ns.err.noinfo", "ns.err.old", "ns.err.nonet", "ns.err.denied", "ns.err.connect", "ns.err.unreach"} {
		m, ok := dmessages[k]
		if !ok {
			t.Errorf("%s dmessages icinde yok", k)
			continue
		}
		for i, s := range m {
			if i < 2 && strings.TrimSpace(s) == "" { // acikta ja/zh sutunu bos
				t.Errorf("%s: dil %d bos", k, i)
			}
		}
	}
}
