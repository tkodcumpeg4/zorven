package api

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestPersistTermBufferAndReattach(t *testing.T) {
	pt := &persistTerm{id: "term_x", detachedAt: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan protocol.TerminalOutput, 4)
	exit := make(chan protocol.TerminalExit, 1)
	go pt.pump(ctx, out, exit)

	// Bagli sekme yokken gelen cikti tamponda birikir.
	out <- protocol.TerminalOutput{Data: []byte("merhaba ")}
	out <- protocol.TerminalOutput{Data: []byte("dunya")}
	waitFor(t, func() bool { pt.mu.Lock(); defer pt.mu.Unlock(); return len(pt.buf) == 13 })

	replay, data, _, exited := pt.attach()
	if string(replay) != "merhaba dunya" || exited != nil {
		t.Fatalf("tekrar oynatma = %q, exited=%v", replay, exited)
	}
	out <- protocol.TerminalOutput{Data: []byte("!")}
	select {
	case b := <-data:
		if string(b) != "!" {
			t.Fatalf("canli = %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("canli cikti gelmedi")
	}

	// Ikinci sekme baglaninca ilk kanal kapanir.
	_, data2, _, _ := pt.attach()
	if _, ok := <-data; ok {
		t.Fatal("eski kanal kapanmaliydi")
	}
	pt.detach(data) // eski kanalla ayrilma yeni baglantiyi bozmamali
	pt.mu.Lock()
	still := pt.sub == data2
	pt.mu.Unlock()
	if !still {
		t.Fatal("eski sekmenin ayrilmasi yeni baglantiyi kopardi")
	}
	pt.detach(data2)

	// Kabuk biterse sonraki baglanma exit'i gorur.
	exit <- protocol.TerminalExit{Code: 3}
	waitFor(t, func() bool { pt.mu.Lock(); defer pt.mu.Unlock(); return pt.exited != nil })
	if _, _, _, ex := pt.attach(); ex == nil || ex.Code != 3 {
		t.Fatalf("exit = %v", ex)
	}
}

func TestPersistTermBufferCap(t *testing.T) {
	pt := &persistTerm{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan protocol.TerminalOutput, 1)
	go pt.pump(ctx, out, make(chan protocol.TerminalExit))
	chunk := bytes.Repeat([]byte("a"), 64<<10)
	for i := 0; i < 6; i++ {
		out <- protocol.TerminalOutput{Data: chunk}
	}
	out <- protocol.TerminalOutput{Data: []byte("SON")}
	waitFor(t, func() bool {
		pt.mu.Lock()
		defer pt.mu.Unlock()
		return bytes.HasSuffix(pt.buf, []byte("SON"))
	})
	pt.mu.Lock()
	n := len(pt.buf)
	pt.mu.Unlock()
	if n > termBufferMax {
		t.Fatalf("tampon %d > %d", n, termBufferMax)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("kosul saglanmadi")
}
