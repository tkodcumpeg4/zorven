package agent

import (
	"reflect"
	"testing"
)

func TestPickSessions(t *testing.T) {
	cases := []struct {
		name    string
		console uint32
		list    []sessInfo
		want    []uint32
	}{
		{"yalniz konsol", 1, []sessInfo{{0, false}, {1, true}}, []uint32{1}},
		{"konsol once, sonra RDP", 1, []sessInfo{{0, false}, {1, true}, {2, true}}, []uint32{1, 2}},
		{"konsolda kullanici yok, RDP aktif", wtsNoSession, []sessInfo{{0, false}, {2, true}}, []uint32{2}},
		{"baglantisi kopuk oturum alinmaz", 1, []sessInfo{{1, true}, {3, false}}, []uint32{1}},
		{"dinleyici oturumu hic", 1, []sessInfo{{1, true}, {65536, true}, {65537, true}}, []uint32{1}},
		{"oturum 0 asla", 0, []sessInfo{{0, true}}, nil},
		{"tekrar yok", 2, []sessInfo{{2, true}, {2, true}}, []uint32{2}},
	}
	for _, c := range cases {
		got := pickSessions(c.console, c.list)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
