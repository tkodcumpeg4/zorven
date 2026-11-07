package netctl

import "testing"

// Sahibi olan cihaz (ajan), sahibine acilan paylasimi gorur; sahibi olmayan gormez.
func TestDeviceCarriesOwnerUser(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(dev("t1", "nas", "nas"))
	mine := dev("t1", "lap", "lap")
	mine.UserID = "alice"
	lap := h.attach(mine)
	pc := h.attach(dev("t1", "pc", "pc"))
	mkShare(t, h, "t1", "nas", 5432, Grantee{GranteeUser, "alice"})
	for _, f := range []*fake{nas, lap, pc} {
		f.drain()
	}
	if len(lap.state.SharesIn) != 1 || !lap.hasPeer(nas.pub) {
		t.Fatalf("alice'in cihazi kullanici paylasimini gormeli: %+v", lap.state)
	}
	if len(pc.state.SharesIn) != 0 || pc.hasPeer(nas.pub) {
		t.Fatal("sahipsiz cihaz gormemeli")
	}
}

// Dizinde uye olmayan sahip kimligi cihaza tasinmaz.
func TestDeviceNonMemberOwnerIgnored(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(dev("t1", "nas", "nas"))
	id := dev("t1", "lap", "lap")
	id.UserID = "yabanci"
	lap := h.attach(id)
	mkShare(t, h, "t1", "nas", 5432, Grantee{GranteeUser, "bob"}, Grantee{GranteeGroup, "role:owner"})
	nas.drain()
	lap.drain()
	if len(lap.state.SharesIn) != 0 {
		t.Fatal("uye olmayan sahibin kimligi tasinmamali")
	}
}
