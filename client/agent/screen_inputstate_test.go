package agent

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestInputTrackerReleasesHeld(t *testing.T) {
	var tr inputTracker
	tr.Observe(protocol.ScreenInput{Kind: "keydown", Code: "ControlLeft", Key: "Control"})
	tr.Observe(protocol.ScreenInput{Kind: "keydown", Code: "KeyC", Key: "c", Repeat: false})
	tr.Observe(protocol.ScreenInput{Kind: "keydown", Code: "KeyC", Key: "c", Repeat: true}) // tekrar: cift sayilmaz
	tr.Observe(protocol.ScreenInput{Kind: "mousedown", Button: 0, X: 0.1, Y: 0.2})
	tr.Observe(protocol.ScreenInput{Kind: "mousemove", X: 0.5, Y: 0.6})
	tr.Observe(protocol.ScreenInput{Kind: "keyup", Code: "ControlLeft", Key: "Control"})
	if tr.Held() != 2 {
		t.Fatalf("held=%d, want 2 (KeyC + sol dugme)", tr.Held())
	}
	rel := tr.ReleaseAll()
	if len(rel) != 2 {
		t.Fatalf("release=%v", rel)
	}
	if rel[0].Kind != "mouseup" || rel[0].X != 0.5 || rel[0].Y != 0.6 {
		t.Fatalf("mouseup son konumu tasimali: %+v", rel[0])
	}
	if rel[1].Kind != "keyup" || rel[1].Code != "KeyC" {
		t.Fatalf("keyup: %+v", rel[1])
	}
	if tr.Held() != 0 || len(tr.ReleaseAll()) != 0 {
		t.Fatal("ReleaseAll sonrasi bos olmali")
	}
}

func TestInputTrackerReleaseOrderModifiersLast(t *testing.T) {
	var tr inputTracker
	tr.Observe(protocol.ScreenInput{Kind: "keydown", Code: "ShiftLeft", Key: "Shift"})
	tr.Observe(protocol.ScreenInput{Kind: "keydown", Code: "KeyA", Key: "A"})
	rel := tr.ReleaseAll()
	if len(rel) != 2 || rel[0].Code != "KeyA" || rel[1].Code != "ShiftLeft" {
		t.Fatalf("birakma sirasi ters olmali: %+v", rel)
	}
}

func TestInputTrackerKeyWithoutCode(t *testing.T) {
	var tr inputTracker
	tr.Observe(protocol.ScreenInput{Kind: "keydown", Key: "ğ"})
	rel := tr.ReleaseAll()
	if len(rel) != 1 || rel[0].Key != "ğ" || rel[0].Kind != "keyup" {
		t.Fatalf("%+v", rel)
	}
}

func mv(x float64) protocol.ScreenInput { return protocol.ScreenInput{Kind: "mousemove", X: x} }

func TestInputQueueNeverDropsCritical(t *testing.T) {
	q := newInputQueue(4, 16)
	// 4 kritik + tasma: hepsi kabul edilmeli (hardCap'e kadar)
	for i := 0; i < 10; i++ {
		if !q.Push(protocol.ScreenInput{Kind: "keyup", Code: "K"}) {
			t.Fatalf("keyup %d reddedildi", i)
		}
	}
	if q.Len() != 10 {
		t.Fatalf("len=%d", q.Len())
	}
}

func TestInputQueueDropsOnlyMousemove(t *testing.T) {
	q := newInputQueue(3, 100)
	q.Push(protocol.ScreenInput{Kind: "keydown", Code: "A"})
	q.Push(mv(0.1))
	q.Push(protocol.ScreenInput{Kind: "keydown", Code: "B"})
	// dolu (3): yeni mousemove dusurulur
	if q.Push(mv(0.2)) {
		t.Fatal("dolu kuyrukta mousemove dusurulmeli")
	}
	// kritik olay: bekleyen mousemove atilip yer acilir
	if !q.Push(protocol.ScreenInput{Kind: "mouseup", Button: 0}) {
		t.Fatal("mouseup kabul edilmeli")
	}
	var kinds []string
	for {
		ev, ok := q.Pop()
		if !ok {
			break
		}
		kinds = append(kinds, ev.Kind)
	}
	want := []string{"keydown", "keydown", "mouseup"}
	if len(kinds) != 3 || kinds[0] != want[0] || kinds[1] != want[1] || kinds[2] != want[2] {
		t.Fatalf("got %v want %v", kinds, want)
	}
}

func TestInputQueueMergesMoves(t *testing.T) {
	q := newInputQueue(8, 16)
	q.Push(mv(0.1))
	q.Push(mv(0.2))
	q.Push(mv(0.3))
	if q.Len() != 1 {
		t.Fatalf("ardisik mousemove birlesmeli, len=%d", q.Len())
	}
	ev, _ := q.Pop()
	if ev.X != 0.3 {
		t.Fatalf("son konum kazanmali: %v", ev.X)
	}
}

func TestInputQueueSignal(t *testing.T) {
	q := newInputQueue(8, 16)
	q.Push(mv(1))
	select {
	case <-q.sig:
	default:
		t.Fatal("sinyal beklenir")
	}
}

func TestCoalesceQ(t *testing.T) {
	q := newInputQueue(8, 16)
	q.Push(mv(0.1))
	q.Push(protocol.ScreenInput{Kind: "wheel", DeltaY: 1})
	q.Push(mv(0.9))
	first, _ := q.Pop()
	got := coalesceQ(q, first)
	if len(got) != 2 || got[0].Kind != "mousemove" || got[1].Kind != "wheel" {
		t.Fatalf("%+v", got)
	}
	rest, _ := q.Pop()
	if rest.X != 0.9 {
		t.Fatalf("%+v", rest)
	}
}

func TestIsAltGrChar(t *testing.T) {
	cases := []struct {
		name string
		msg  protocol.ScreenInput
		want bool
	}{
		{"almanca AltGr+Q", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "@", Code: "KeyQ"}, true},
		{"turkce AltGr+Q", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "@", Code: "KeyQ"}, true},
		{"euro AltGr+E", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "€", Code: "KeyE"}, true},
		{"gercek Ctrl+Alt+T", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "t", Code: "KeyT"}, false},
		{"Ctrl+Alt+Shift+T", protocol.ScreenInput{Ctrl: true, Alt: true, Shift: true, Key: "T", Code: "KeyT"}, false},
		{"Ctrl+Alt+1", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "1", Code: "Digit1"}, false},
		{"yalniz Ctrl", protocol.ScreenInput{Ctrl: true, Key: "@", Code: "KeyQ"}, false},
		{"Meta ile", protocol.ScreenInput{Ctrl: true, Alt: true, Meta: true, Key: "@", Code: "KeyQ"}, false},
		{"ozel tus", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "Delete", Code: "Delete"}, false},
		{"Ctrl+Alt+virgul", protocol.ScreenInput{Ctrl: true, Alt: true, Key: ",", Code: "Comma"}, false},
		{"AltGr+7 suslu", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "{", Code: "Digit7"}, true},
		{"kod yok", protocol.ScreenInput{Ctrl: true, Alt: true, Key: "@"}, false},
	}
	for _, c := range cases {
		if got := isAltGrChar(c.msg); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestTextUTF16Surrogates(t *testing.T) {
	got := textUTF16("a😀", 0)
	if len(got) != 3 || got[0] != 'a' || got[1] != 0xD83D || got[2] != 0xDE00 {
		t.Fatalf("%x", got)
	}
	// kesme rune sinirinda: "ğ" 2 bayt; 1 bayta kesince bos kalmali
	if got := textUTF16("ğ", 1); len(got) != 0 {
		t.Fatalf("yarim rune kalmamali: %x", got)
	}
	if got := textUTF16("ağb", 3); len(got) != 2 {
		t.Fatalf("%x", got)
	}
}

func TestHeldMods(t *testing.T) {
	var h heldMods
	if h.Held(modClassCtrl) {
		t.Fatal("baslangicta bos")
	}
	h.Down(modClassCtrl, 0xA2)
	h.Down(modClassCtrl, 0xA3)
	h.Up(modClassCtrl, 0xA2)
	if !h.Held(modClassCtrl) {
		t.Fatal("sag ctrl hala basili")
	}
	h.Up(modClassCtrl, 0xA3)
	if h.Held(modClassCtrl) || h.Held(modClassAlt) {
		t.Fatal("birakildi")
	}
	h.Down(modClassAlt, 0xA4)
	h.Reset()
	if h.Held(modClassAlt) {
		t.Fatal("reset")
	}
}

func TestDesktopGuard(t *testing.T) {
	var g desktopGuard
	t0 := time.Unix(100, 0)
	if !g.Due(t0) {
		t.Fatal("ilk cagri due")
	}
	if g.Due(t0.Add(time.Second)) {
		t.Fatal("2 sn dolmadi")
	}
	if !g.Due(t0.Add(2 * time.Second)) {
		t.Fatal("2 sn doldu")
	}
}
