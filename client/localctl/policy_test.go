package localctl

import (
	"strings"
	"testing"
)

func TestUIDAllowed(t *testing.T) {
	cases := []struct {
		peer, self uint32
		want       bool
	}{
		{1000, 1000, true}, {0, 1000, true}, {0, 0, true},
		{1001, 1000, false}, {1000, 0, false},
	}
	for _, c := range cases {
		if got := uidAllowed(c.peer, c.self); got != c.want {
			t.Errorf("uidAllowed(%d,%d)=%v", c.peer, c.self, got)
		}
	}
}

func TestSIDAllowed(t *testing.T) {
	user := "S-1-5-21-1-2-3-1001"
	other := "S-1-5-21-1-2-3-1002"
	cases := []struct {
		name   string
		peer   string
		groups []string
		self   string
		want   bool
	}{
		{"system", sidSystem, nil, user, true},
		{"self", user, nil, user, true},
		{"self lower", strings.ToLower(user), nil, user, true},
		{"other user", other, []string{"S-1-5-32-545"}, user, false},
		{"elevated admin", other, []string{"S-1-5-32-545", sidAdmins}, user, true},
		{"service: normal user", user, nil, sidSystem, false},
		{"service: admin", other, []string{sidAdmins}, sidSystem, true},
		{"empty", "", []string{sidAdmins}, user, false},
	}
	for _, c := range cases {
		if got := sidAllowed(c.peer, c.groups, c.self); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestSIDAccess(t *testing.T) {
	user := "S-1-5-21-1-2-3-1001"
	other := "S-1-5-21-1-2-3-1002"
	cases := []struct {
		name    string
		peer    string
		groups  []string
		self    string
		ops     []string
		service bool
		want    access
	}{
		{"service: system", sidSystem, nil, sidSystem, nil, true, accessFull},
		{"service: elevated admin", other, []string{sidAdmins}, sidSystem, nil, true, accessFull},
		{"service: filtered admin = normal user", other, []string{"S-1-5-32-545"}, sidSystem, nil, true, accessReadOnly},
		{"service: operator", user, nil, sidSystem, []string{user}, true, accessOperator},
		{"service: operator lower", strings.ToLower(user), nil, sidSystem, []string{user}, true, accessOperator},
		{"service: other user", other, nil, sidSystem, []string{user}, true, accessReadOnly},
		{"service: empty operators", user, nil, sidSystem, nil, true, accessReadOnly},
		{"service: empty sid", "", nil, sidSystem, []string{""}, true, accessNone},
		{"user ep: self", user, nil, user, nil, false, accessFull},
		{"user ep: other user denied", other, nil, user, []string{other}, false, accessNone},
	}
	for _, c := range cases {
		if got := sidAccess(c.peer, c.groups, c.self, c.ops, c.service); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestOpAllowed(t *testing.T) {
	cases := []struct {
		a    access
		op   string
		want bool
	}{
		{accessFull, "debug_stacks", true},
		{accessFull, "set", true},
		{accessOperator, "status", true},
		{accessOperator, "set", true},
		{accessOperator, "net_up", true},
		{accessOperator, "share_create", true},
		{accessOperator, "reload", true},
		{accessOperator, "debug_stacks", false},
		{accessOperator, "bilinmeyen", false},
		{accessReadOnly, "status", true},
		{accessReadOnly, "set", false},
		{accessReadOnly, "reload", false},
		{accessReadOnly, "share_list", false},
		{accessReadOnly, "debug_stacks", false},
		{accessNone, "status", false},
	}
	for _, c := range cases {
		if got := opAllowed(c.a, c.op); got != c.want {
			t.Errorf("opAllowed(%v,%s)=%v", c.a, c.op, got)
		}
	}
}

func TestRedactStatus(t *testing.T) {
	r := Response{OK: true, Connected: true, State: "connected", LastError: "gizli", Server: "s:443",
		Tunnels: []TunnelInfo{{ID: "x"}}, Disconnects: []DisconnectInfo{{Reason: "r"}}, Nodes: []NodeInfo{{ID: "n"}}}
	redactStatus(&r)
	if !r.Connected || r.State != "connected" {
		t.Error("baglanti durumu korunmali")
	}
	if r.LastError != "" || r.Server != "" || r.Tunnels != nil || r.Disconnects != nil || r.Nodes != nil {
		t.Errorf("hassas alanlar suzulmeli: %+v", r)
	}
}

func TestPipeName(t *testing.T) {
	a := pipeName(`C:\ProgramData\zorven\ctl\ctl.sock`)
	if a != pipeName(`c:\programdata\zorven\ctl\ctl.sock`) {
		t.Error("buyuk/kucuk harf duyarsiz olmali")
	}
	if a == pipeName(`C:\Users\x\ctl.sock`) || !strings.HasPrefix(a, `\\.\pipe\zorven-ctl-`) {
		t.Errorf("beklenmeyen ad %q", a)
	}
}

// Authenticated Users kanal ornegi olusturamamali (sahte sunucu riski); okuyup yazabilmeli.
func TestPipeAccessAuthUsersNoCreateInstance(t *testing.T) {
	if pipeAccessAuthUsers&pipeCreateInstance != 0 {
		t.Fatalf("0x%x FILE_CREATE_PIPE_INSTANCE iceriyor", pipeAccessAuthUsers)
	}
	const fileReadData, fileWriteData = uint32(0x1), uint32(0x2)
	if pipeAccessAuthUsers&fileReadData == 0 || pipeAccessAuthUsers&fileWriteData == 0 {
		t.Fatalf("0x%x okuma/yazma hakki eksik", pipeAccessAuthUsers)
	}
}
