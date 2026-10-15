package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type fakeSink struct {
	mu     sync.Mutex
	ctl    []json.RawMessage
	frames []protocol.BodyFrame
	cond   chan struct{}
}

func newFakeSink() *fakeSink { return &fakeSink{cond: make(chan struct{}, 1024)} }

func (f *fakeSink) sendControl(_ context.Context, v any, t protocol.MessageType) error {
	b, err := protocol.Marshal(v, t)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.ctl = append(f.ctl, b)
	f.mu.Unlock()
	f.cond <- struct{}{}
	return nil
}

func (f *fakeSink) sendBinary(_ context.Context, b []byte) error {
	fr, err := protocol.DecodeBodyFrame(b)
	if err != nil {
		return err
	}
	fr.Payload = append([]byte(nil), fr.Payload...)
	f.mu.Lock()
	f.frames = append(f.frames, fr)
	f.mu.Unlock()
	f.cond <- struct{}{}
	return nil
}

// waitCtl, t tipli bir kontrol mesaji gelene kadar bekler ve dondurur.
func (f *fakeSink) waitCtl(t *testing.T, ty protocol.MessageType, out any) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	seen := 0
	for {
		f.mu.Lock()
		for ; seen < len(f.ctl); seen++ {
			if pt, _ := protocol.PeekType(f.ctl[seen]); pt == ty {
				b := f.ctl[seen]
				f.mu.Unlock()
				if err := json.Unmarshal(b, out); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		f.mu.Unlock()
		select {
		case <-f.cond:
		case <-deadline:
			t.Fatalf("%s gelmedi", ty)
		}
	}
}

func (f *fakeSink) frameBytes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, fr := range f.frames {
		n += len(fr.Payload)
	}
	return n
}

func newTestFM(t *testing.T) (*fileManager, *fakeSink, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Zorven")
	sink := newFakeSink()
	fm := newFileManager(sink, slog.New(slog.NewTextHandler(io.Discard, nil)), dir, 0)
	t.Cleanup(fm.closeAll)
	return fm, sink, dir
}

func hsum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// putFile, yuklemeyi uctan uca surer (sha dogru) ve file_done'i doner.
func putFile(t *testing.T, fm *fileManager, sink *fakeSink, id uint64, name string, data []byte) (protocol.FileOpenAck, protocol.FileDone) {
	t.Helper()
	ctx := context.Background()
	fm.open(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: id, Dir: protocol.FileDirPut, Name: name, Size: int64(len(data))})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	if !ack.OK {
		return ack, protocol.FileDone{}
	}
	for off := 0; off < len(data); off += protocol.FileChunkSize {
		fm.data(ctx, id, data[off:min(off+protocol.FileChunkSize, len(data))])
	}
	fm.end(ctx, protocol.FileEnd{Type: protocol.TypeFileEnd, XferID: id, Size: int64(len(data)), SHA256: hsum(data)})
	var done protocol.FileDone
	sink.waitCtl(t, protocol.TypeFileDone, &done)
	return ack, done
}

func TestAgentPutSavesAndNeverOverwrites(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	ack, done := putFile(t, fm, sink, 1, "not.txt", []byte("birinci"))
	if !ack.OK || !done.OK || done.Name != "not.txt" {
		t.Fatalf("ack=%+v done=%+v", ack, done)
	}
	sink2 := newFakeSink()
	fm.sink = sink2
	_, done2 := putFile(t, fm, sink2, 2, "not.txt", []byte("ikinci"))
	if !done2.OK || done2.Name != "not (1).txt" {
		t.Fatalf("ikinci ad: %+v", done2)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "not.txt")); string(b) != "birinci" {
		t.Fatalf("mevcut dosya ezildi: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "not (1).txt")); string(b) != "ikinci" {
		t.Fatalf("ikinci icerik: %q", b)
	}
}

func TestAgentPutLargeMultiChunk(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	data := bytes.Repeat([]byte("0123456789abcdef"), 40000) // 640 KB
	_, done := putFile(t, fm, sink, 1, "buyuk.bin", data)
	if !done.OK || done.SHA256 != hsum(data) {
		t.Fatalf("done=%+v", done)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "buyuk.bin"))
	if !bytes.Equal(got, data) {
		t.Fatal("icerik bozuk")
	}
	// 640 KB > 256 KB: tuketim ack'i (kredi iadesi) gonderilmis olmali.
	var ack protocol.FileAck
	sink.waitCtl(t, protocol.TypeFileAck, &ack)
	if ack.Bytes < protocol.FileAckEvery {
		t.Fatalf("ack bayt=%d", ack.Bytes)
	}
}

func TestAgentPutRejectsBadNames(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	bad := []string{"../evil.txt", `..\evil.txt`, "a/b.txt", `C:\Windows\x.dll`, "", "..", "a\x00b"}
	for i, n := range bad {
		fm.open(context.Background(), protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: uint64(10 + i), Dir: protocol.FileDirPut, Name: n, Size: 1})
		var ack protocol.FileOpenAck
		sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
		sink.mu.Lock()
		sink.ctl = nil
		sink.mu.Unlock()
		if ack.OK || ack.Code != protocol.FileErrBadName {
			t.Errorf("%q kabul edildi: %+v", n, ack)
		}
	}
	// Klasor disina hicbir sey yazilmamis olmali.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Fatal("klasor disina yazildi")
	}
}

func TestAgentPutReservedWindowsNameSanitized(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	_, done := putFile(t, fm, sink, 1, "CON.txt", []byte("x"))
	if !done.OK || done.Name != "_CON.txt" {
		t.Fatalf("done=%+v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, "_CON.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPutUnicodeName(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	name := "Türkçe rapor şğüıİ 日本語.txt"
	_, done := putFile(t, fm, sink, 1, name, []byte("x"))
	if !done.OK || done.Name != name {
		t.Fatalf("done=%+v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPutSizeLimit(t *testing.T) {
	fm, sink, _ := newTestFM(t)
	fm.maxSize = 10
	fm.open(context.Background(), protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 1, Dir: protocol.FileDirPut, Name: "a", Size: 11})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	if ack.OK || ack.Code != protocol.FileErrTooLarge {
		t.Fatalf("ack=%+v", ack)
	}
}

func TestAgentPutChecksumMismatchDeletesFile(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	ctx := context.Background()
	fm.open(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 1, Dir: protocol.FileDirPut, Name: "x.bin", Size: 3})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	fm.data(ctx, 1, []byte("abc"))
	fm.end(ctx, protocol.FileEnd{Type: protocol.TypeFileEnd, XferID: 1, Size: 3, SHA256: hsum([]byte("zzz"))})
	var done protocol.FileDone
	sink.waitCtl(t, protocol.TypeFileDone, &done)
	if done.OK || done.Code != protocol.FileErrChecksum {
		t.Fatalf("done=%+v", done)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.bin")); err == nil {
		t.Fatal("bozuk dosya silinmedi")
	}
}

func TestAgentPutExceedingDeclaredSizeAborts(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	ctx := context.Background()
	fm.open(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 1, Dir: protocol.FileDirPut, Name: "x.bin", Size: 2})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	fm.data(ctx, 1, []byte("abc"))
	var ab protocol.FileAbort
	sink.waitCtl(t, protocol.TypeFileAbort, &ab)
	if ab.Code != protocol.FileErrTooLarge {
		t.Fatalf("abort=%+v", ab)
	}
	waitGone(t, filepath.Join(dir, "x.bin"))
}

func waitGone(t *testing.T, p string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(p); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s silinmedi", p)
}

func TestAgentPutAbortFromPeerDeletesFile(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	ctx := context.Background()
	fm.open(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 1, Dir: protocol.FileDirPut, Name: "x.bin", Size: 100})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	fm.data(ctx, 1, []byte("abc"))
	fm.abortFromPeer(1)
	waitGone(t, filepath.Join(dir, "x.bin"))
}

func TestAgentConcurrencyLimit(t *testing.T) {
	fm, sink, _ := newTestFM(t)
	ctx := context.Background()
	for i := 1; i <= protocol.MaxFileXfers; i++ {
		fm.open(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: uint64(i), Dir: protocol.FileDirPut, Name: "f" + string(rune('a'+i)), Size: 5})
	}
	sink.mu.Lock()
	sink.ctl = nil
	sink.mu.Unlock()
	fm.open(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 99, Dir: protocol.FileDirPut, Name: "dort", Size: 5})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	if ack.OK || ack.Code != protocol.FileErrBusy {
		t.Fatalf("4. aktarim: %+v", ack)
	}
}

func TestAgentGetStreamsAndVerifies(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	os.MkdirAll(dir, 0o755)
	data := bytes.Repeat([]byte("zorven!"), 20000) // 140 KB
	os.WriteFile(filepath.Join(dir, "veri.bin"), data, 0o644)
	fm.open(context.Background(), protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 5, Dir: protocol.FileDirGet, Name: "veri.bin"})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	if !ack.OK || ack.Size != int64(len(data)) {
		t.Fatalf("ack=%+v", ack)
	}
	var end protocol.FileEnd
	sink.waitCtl(t, protocol.TypeFileEnd, &end)
	if end.Size != int64(len(data)) || end.SHA256 != hsum(data) {
		t.Fatalf("end=%+v", end)
	}
	var got []byte
	sink.mu.Lock()
	for i, fr := range sink.frames {
		if fr.FrameType != protocol.FrameFile || fr.ReqID != 5 || fr.Seq != uint32(i) || len(fr.Payload) > protocol.FileChunkSize {
			t.Fatalf("parca %d bozuk: %+v", i, fr)
		}
		got = append(got, fr.Payload...)
	}
	sink.mu.Unlock()
	if !bytes.Equal(got, data) {
		t.Fatal("veri bozuk")
	}
}

func TestAgentGetBackpressure(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	os.MkdirAll(dir, 0o755)
	fm.rate = 1 << 40 // hiz siniri devre disi: yalnizca kredi test edilir
	data := make([]byte, 4<<20)
	os.WriteFile(filepath.Join(dir, "b.bin"), data, 0o644)
	fm.open(context.Background(), protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 7, Dir: protocol.FileDirGet, Name: "b.bin"})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	time.Sleep(500 * time.Millisecond)
	if n := sink.frameBytes(); n > protocol.FileWindowBytes {
		t.Fatalf("ack olmadan %d bayt gitti", n)
	} else if n < protocol.FileWindowBytes-protocol.FileChunkSize {
		t.Fatalf("pencere dolmadi: %d", n)
	}
	fm.ack(protocol.FileAck{Type: protocol.TypeFileAck, XferID: 7, Bytes: int64(sink.frameBytes())})
	time.Sleep(300 * time.Millisecond)
	if sink.frameBytes() <= protocol.FileWindowBytes {
		t.Fatal("ack sonrasi akis surmedi")
	}
	// Gecersiz (gonderilenden fazla) ack aktarimi sonlandirir.
	fm.ack(protocol.FileAck{Type: protocol.TypeFileAck, XferID: 7, Bytes: 1 << 40})
	time.Sleep(200 * time.Millisecond)
	n := sink.frameBytes()
	time.Sleep(200 * time.Millisecond)
	if sink.frameBytes() != n {
		t.Fatal("protokol ihlalinden sonra gonderim surdu")
	}
}

func TestAgentGetRejects(t *testing.T) {
	fm, sink, dir := newTestFM(t)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "gizli.txt"), []byte("s"), 0o644)
	os.MkdirAll(filepath.Join(dir, "altklasor"), 0o755)
	cases := map[string]string{
		"../gizli.txt": protocol.FileErrBadName,
		`..\gizli.txt`: protocol.FileErrBadName,
		"yok.txt":      protocol.FileErrNotFound,
		"altklasor":    protocol.FileErrNotFound,
		"a/b":          protocol.FileErrBadName,
		"":             protocol.FileErrBadName,
	}
	id := uint64(100)
	for name, code := range cases {
		id++
		fm.open(context.Background(), protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: id, Dir: protocol.FileDirGet, Name: name})
		var ack protocol.FileOpenAck
		sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
		sink.mu.Lock()
		sink.ctl = nil
		sink.mu.Unlock()
		if ack.OK || ack.Code != code {
			t.Errorf("%q: %+v (beklenen %s)", name, ack, code)
		}
	}
}

func TestAgentGetRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sembolik baglanti ayrıcalik ister")
	}
	fm, sink, dir := newTestFM(t)
	os.MkdirAll(dir, 0o755)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	os.WriteFile(secret, []byte("s"), 0o644)
	if err := os.Symlink(secret, filepath.Join(dir, "link.txt")); err != nil {
		t.Skip(err)
	}
	fm.open(context.Background(), protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: 1, Dir: protocol.FileDirGet, Name: "link.txt"})
	var ack protocol.FileOpenAck
	sink.waitCtl(t, protocol.TypeFileOpenAck, &ack)
	if ack.OK {
		t.Fatal("sembolik baglanti indirilebildi")
	}
	// Liste de sembolik baglanti ve klasorleri gostermez.
	os.WriteFile(filepath.Join(dir, "iyi.txt"), []byte("12345"), 0o644)
	os.MkdirAll(filepath.Join(dir, "klasor"), 0o755)
	fm.list(context.Background(), protocol.FileList{Type: protocol.TypeFileList, ReqID: 3})
	var res protocol.FileListResult
	sink.waitCtl(t, protocol.TypeFileListResult, &res)
	if len(res.Files) != 1 || res.Files[0].Name != "iyi.txt" || res.Files[0].Size != 5 || res.Dir != dir {
		t.Fatalf("liste=%+v", res)
	}
}

func TestAgentListMissingDirIsEmpty(t *testing.T) {
	fm, sink, _ := newTestFM(t)
	fm.list(context.Background(), protocol.FileList{Type: protocol.TypeFileList, ReqID: 1})
	var res protocol.FileListResult
	sink.waitCtl(t, protocol.TypeFileListResult, &res)
	if res.Code != "" || len(res.Files) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

func TestHelloFeaturesFileTransferOptOut(t *testing.T) {
	has := func(a *Agent) bool {
		for _, f := range a.helloFeatures() {
			if f == protocol.FeatureScreenFiles {
				return true
			}
		}
		return false
	}
	if !has(&Agent{}) {
		t.Fatal("varsayilan olarak ilan edilmeli")
	}
	if has(&Agent{NoFileTransfer: true}) {
		t.Fatal("--no-file-transfer ilan etmemeli")
	}
}
