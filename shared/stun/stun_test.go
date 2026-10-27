package stun

import (
	"net/netip"
	"testing"
)

func TestBindingRoundTripV4V6(t *testing.T) {
	for _, s := range []string{"203.0.113.9:54321", "[2001:db8::7]:4242"} {
		src := netip.MustParseAddrPort(s)
		tx := NewTxID()
		req := NewBindingRequest(tx)
		got, ok := ParseBindingRequest(req)
		if !ok || got != tx {
			t.Fatalf("istek cozulemedi: %v", ok)
		}
		resp := NewBindingResponse(tx, src)
		ap, ok := ParseBindingResponse(resp, tx)
		if !ok || ap != src {
			t.Fatalf("yanit: %v %v (beklenen %v)", ap, ok, src)
		}
		// Amplifikasyon siniri: yanit istegin en fazla ~2.2 kati.
		if len(resp) > 3*len(req) {
			t.Fatalf("yanit cok buyuk: %d", len(resp))
		}
	}
}

func TestRejectsWrongTxAndJunk(t *testing.T) {
	tx := NewTxID()
	resp := NewBindingResponse(tx, netip.MustParseAddrPort("1.2.3.4:5"))
	if _, ok := ParseBindingResponse(resp, NewTxID()); ok {
		t.Fatal("yanlis txid kabul edildi")
	}
	for _, junk := range [][]byte{nil, {1, 2, 3}, make([]byte, 20), append(NewBindingRequest(tx), 1, 2, 3)} {
		if _, ok := ParseBindingRequest(junk); ok {
			t.Fatalf("cop istek kabul edildi: %v", junk)
		}
	}
	// WireGuard el sikisma paketi (tip 1 + 3 sifir) STUN sayilmamali.
	wg := make([]byte, 148)
	wg[0] = 1
	if IsSTUN(wg) {
		t.Fatal("WireGuard paketi STUN sanildi")
	}
	// Buyuk istek reddedilir.
	big := NewBindingRequest(tx)
	big = append(big, make([]byte, MaxRequestSize)...)
	big[2], big[3] = 0, byte(len(big)-HeaderSize)
	if _, ok := ParseBindingRequest(big); ok {
		t.Fatal("buyuk istek kabul edildi")
	}
}

func FuzzParseResponse(f *testing.F) {
	tx := NewTxID()
	f.Add(NewBindingResponse(tx, netip.MustParseAddrPort("1.2.3.4:5")))
	f.Add([]byte{0x01, 0x01, 0, 4, 0x21, 0x12, 0xa4, 0x42})
	f.Fuzz(func(t *testing.T, b []byte) {
		ParseBindingResponse(b, tx)
		ParseBindingRequest(b)
	})
}
