package agent

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestInputStatusGate(t *testing.T) {
	var g inputStatusGate
	t0 := time.Unix(1000, 0)
	ok := inputResult{State: protocol.InputStateOK}
	blocked := inputResult{State: protocol.InputStateBlocked, Code: 5}

	if !g.Should(ok, t0) {
		t.Fatal("ilk girdi bildirilmeli")
	}
	for i := 1; i < 100; i++ {
		if g.Should(ok, t0.Add(time.Duration(i)*time.Second)) {
			t.Fatal("ayni basarili durum tekrar bildirilmemeli")
		}
	}
	now := t0.Add(200 * time.Second)
	if !g.Should(blocked, now) {
		t.Fatal("durum degisince bildirilmeli")
	}
	if g.Should(blocked, now.Add(500*time.Millisecond)) {
		t.Fatal("2 sn dolmadan tekrar bildirilmemeli")
	}
	if !g.Should(blocked, now.Add(2*time.Second)) {
		t.Fatal("basarisizlik surerken 2 sn sonra tekrar bildirilmeli")
	}
	if !g.Should(ok, now.Add(2500*time.Millisecond)) {
		t.Fatal("duzelince bildirilmeli")
	}
	if g.Should(inputResult{}, now) {
		t.Fatal("bos durum bildirilmemeli")
	}
}

func TestInputResultMerge(t *testing.T) {
	ok := inputResult{State: protocol.InputStateOK}
	bad := inputResult{State: protocol.InputStateBlocked, Code: 5}
	if got := ok.merge(bad); got.State != protocol.InputStateBlocked {
		t.Fatalf("ok+bad = %v", got)
	}
	if got := bad.merge(ok); got.State != protocol.InputStateBlocked {
		t.Fatalf("bad+ok = %v", got)
	}
}
