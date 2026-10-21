package wgnet

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestKeyPersistAndPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "wg.key")
	k1, err := LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateKey(p)
	if err != nil || k1 != k2 {
		t.Fatalf("anahtar kalici olmali: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("izin %v", st.Mode().Perm())
		}
	}
	if k1.PublicB64() == "" || k1.Public() == [32]byte{} {
		t.Fatal("acik anahtar uretilmedi")
	}
	if err := os.WriteFile(p, []byte("bozuk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(p); err == nil {
		t.Fatal("bozuk anahtar dosyasi kabul edildi")
	}
}

func TestResolveShare(t *testing.T) {
	st := protocol.NewNetState()
	st.SharesIn["a"] = protocol.NetShareIn{ID: "a", Name: "db", OwnerName: "nas", Port: 5432, Proto: "tcp"}
	st.SharesIn["b"] = protocol.NetShareIn{ID: "b", Name: "web", OwnerName: "nas", Port: 80, Proto: "tcp"}
	if s, err := ResolveShare(st, "db"); err != nil || s.ID != "a" {
		t.Fatalf("ad: %v %v", s, err)
	}
	if s, err := ResolveShare(st, "nas:80"); err != nil || s.ID != "b" {
		t.Fatalf("cihaz:port: %v %v", s, err)
	}
	if _, err := ResolveShare(st, "nas"); err == nil {
		t.Fatal("belirsiz hedef reddedilmeli")
	}
	if _, err := ResolveShare(st, "yok"); err == nil {
		t.Fatal("bulunamayan hedef")
	}
	if _, err := ResolveShare(st, "nas:99999"); err == nil {
		t.Fatal("gecersiz port")
	}
}

func TestBindDropsWhenQueueFull(t *testing.T) {
	b := newRelayBind()
	ep, _ := b.ParseEndpoint("100.64.1.5:1")
	pkt := [][]byte{make([]byte, 100)}
	for i := 0; i < bindQueue+50; i++ {
		_ = b.Send(pkt, ep)
	}
	if b.Dropped.Load() != 50 {
		t.Fatalf("dusen=%d, beklenen 50", b.Dropped.Load())
	}
}
