package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type wireMsg struct {
	typ  websocket.MessageType
	data []byte
}

// wireRig: ajanin gonderdiklerini toplayan sahte sunucu + screenManager.
type wireRig struct {
	cs  *clientSession
	sm  *screenManager
	got chan wireMsg
	ctx context.Context
}

func newWireRig(t *testing.T) *wireRig {
	t.Helper()
	got := make(chan wireMsg, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(protocol.WSReadLimit)
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			got <- wireMsg{typ, data}
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	cs := newClientSession(conn, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &wireRig{cs: cs, sm: newScreenManager(cs), got: got, ctx: ctx}
}

func (r *wireRig) next(t *testing.T) wireMsg {
	t.Helper()
	select {
	case m := <-r.got:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("mesaj gelmedi")
		return wireMsg{}
	}
}

func (r *wireRig) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-r.got:
		t.Fatalf("mesaj gelmemeliydi: %s", m.data)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSendClipImageChunksAndReassembles(t *testing.T) {
	r := newWireRig(t)
	ss := &screenSession{id: "s1", streamID: 42}
	png := encPNG(t, testImage(700, 400, true)) // birden cok parca
	for len(png) < protocol.ClipImageChunk*2+10 {
		// Gurultu ekleyip PNG'yi parcalanacak kadar buyut (IHDR bozulmaz: sona eklenir).
		png = append(png, bytes.Repeat([]byte{7}, protocol.ClipImageChunk)...)
	}
	r.sm.sendClipImage(r.ctx, ss, png)
	var asm protocol.ClipImageAssembler
	var got []byte
	n := 0
	for got == nil {
		m := r.next(t)
		if m.typ != websocket.MessageBinary {
			t.Fatalf("ikili bekleniyordu")
		}
		f, err := protocol.DecodeBodyFrame(m.data)
		if err != nil || f.FrameType != protocol.FrameClipImage || f.ReqID != 42 {
			t.Fatalf("cerceve: %+v %v", f, err)
		}
		if len(m.data) > protocol.WSReadLimit {
			t.Fatal("cerceve WS okuma sinirini asiyor")
		}
		out, done, err := asm.Add(f.Seq, f.EOF, f.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			got = out
		}
		n++
	}
	if n < 3 || !bytes.Equal(got, png) {
		t.Fatalf("parca=%d esit=%v", n, bytes.Equal(got, png))
	}
}

func TestSendClipImageRejectsInvalid(t *testing.T) {
	r := newWireRig(t)
	r.sm.sendClipImage(r.ctx, &screenSession{id: "s1", streamID: 1}, []byte("png degil"))
	r.none(t)
}

func TestClipImageChunkAppliesToClipboard(t *testing.T) {
	r := newWireRig(t)
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w := newClipboardWatcher(fc, func(string) {})
	w.enableImages(func([]byte) {})
	w.SetEnabled(true)
	ss := &screenSession{id: "s1", streamID: 9, clip: w}
	r.sm.mu.Lock()
	r.sm.sessions["s1"] = ss
	r.sm.mu.Unlock()

	png := encPNG(t, testImage(30, 20, true))
	parts := protocol.SplitClipImage(png)
	for i, p := range parts {
		r.sm.clipImageChunk(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: 9, Seq: uint32(i), EOF: i == len(parts)-1, Payload: p})
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		ok := bytes.Equal(fc.png, png)
		w.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.mu.Lock()
	ok := bytes.Equal(fc.png, png)
	w.mu.Unlock()
	if !ok {
		t.Fatal("gorsel panoya yazilmadi")
	}
	// Bilinmeyen akis ve bozuk icerik panoya yazilmaz.
	fc.png = nil
	r.sm.clipImageChunk(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: 77, Seq: 0, EOF: true, Payload: png})
	r.sm.clipImageChunk(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: 9, Seq: 0, EOF: true, Payload: []byte("png degil png degil png degil png degil")})
	time.Sleep(100 * time.Millisecond)
	if fc.png != nil {
		t.Fatal("gecersiz/bilinmeyen akis panoya yazilmamali")
	}
}

func TestScreenInfoGatedByFeature(t *testing.T) {
	r := newWireRig(t)
	r.sm.sendScreenInfo(r.ctx, "s1", 1, 3) // bayrak yok: eski sunucu hic gormez
	r.none(t)
	r.cs.screenInfo = true
	r.sm.sendScreenInfo(r.ctx, "s1", 1, 3)
	var m protocol.ScreenInfo
	if err := json.Unmarshal(r.next(t).data, &m); err != nil || m.Type != protocol.TypeScreenInfo || m.Displays != 3 || m.Display != 1 || m.SessionID != "s1" {
		t.Fatalf("%+v %v", m, err)
	}
	// Gecersiz secili indeks / sayi duzeltilir.
	r.sm.sendScreenInfo(r.ctx, "s1", 5, 0)
	if err := json.Unmarshal(r.next(t).data, &m); err != nil || m.Displays != 1 || m.Display != 0 {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestHelloFeaturesNewFlags(t *testing.T) {
	has := func(fs []string, f string) bool {
		for _, x := range fs {
			if x == f {
				return true
			}
		}
		return false
	}
	fs := (&Agent{}).helloFeatures()
	if !has(fs, protocol.FeatureScreenInfo) || !has(fs, protocol.FeatureScreenPing) {
		t.Fatalf("screen_info/screen_ping ilan edilmeli: %v", fs)
	}
	// Gorsel bayragi yalniz pano + gorsel destegi olan platformda; --no-clipboard her zaman kapatir.
	if has((&Agent{NoClipboard: true}).helloFeatures(), protocol.FeatureScreenClipboardImage) {
		t.Fatal("--no-clipboard gorsel bayragini da kapatmali")
	}
	if has(fs, protocol.FeatureScreenClipboardImage) && !has(fs, protocol.FeatureScreenClipboard) {
		t.Fatal("gorsel bayragi pano bayragi olmadan ilan edilemez")
	}
	if runtime.GOOS != "windows" && has(fs, protocol.FeatureScreenClipboardImage) {
		t.Fatal("gorsel panosu yalniz Windows'ta")
	}
}

func TestExecRunnerRealProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh yok")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh yok")
	}
	var run execRunner
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := run.Output(ctx, "sh", []string{"-c", "printf abc"}, 100); err != nil || string(out) != "abc" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := run.Output(ctx, "sh", []string{"-c", "exit 1"}, 100); err != errClipCmdExit {
		t.Fatalf("hata kodu errClipCmdExit olmali: %v", err)
	}
	if _, err := run.Output(ctx, "bu-komut-yok-xyz", nil, 100); err == nil || err == errClipCmdExit {
		t.Fatalf("baslatma hatasi ayri olmali: %v", err)
	}
	// Sinir: cok cikti kesilir ve arac bekletilmez.
	start := time.Now()
	out, err := run.Output(ctx, "sh", []string{"-c", "yes aaaaaaaaaa"}, 1000)
	if err != nil || len(out) != 1000 || time.Since(start) > 3*time.Second {
		t.Fatalf("limit: %d %v %v", len(out), err, time.Since(start))
	}
	// Zaman asimi.
	short, c2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer c2()
	if _, err := run.Output(short, "sh", []string{"-c", "sleep 5"}, 100); err == nil {
		t.Fatal("zaman asimi hata vermeli")
	}
	// Input: stdin'i okur; arka plana gecen arac Wait'i asmaz.
	tmp := t.TempDir() + "/out.txt"
	if err := run.Input(ctx, "sh", []string{"-c", "cat > " + tmp}, []byte("merhaba")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tmp); string(b) != "merhaba" {
		t.Fatalf("stdin: %q", b)
	}
	start = time.Now()
	if err := run.Input(ctx, "sh", []string{"-c", "cat >/dev/null; (sleep 3 &) ; exit 0"}, []byte("x")); err != nil || time.Since(start) > 2*time.Second {
		t.Fatalf("arka plan sureci Input'u bekletmemeli: %v %v", err, time.Since(start))
	}
}
