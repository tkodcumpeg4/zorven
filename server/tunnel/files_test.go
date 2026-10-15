package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// fileHarness: gercek bir WS cifti; sunucu tarafi *Session (Run calisir), istemci
// tarafi testin elinde ve protokolu elle konusur (sahte ajan).
type fileHarness struct {
	t      *testing.T
	s      *Session
	agent  *websocket.Conn
	cancel context.CancelFunc
}

func newFileHarness(t *testing.T, filesOK bool) *fileHarness {
	t.Helper()
	ready := make(chan *Session, 1)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(protocol.WSReadLimit)
		s := NewSession("s1", "c1", "c", "127.0.0.1:1", c, slog.New(slog.NewTextHandler(io.Discard, nil)))
		s.screenFiles = filesOK
		ready <- s
		_ = s.Run(ctx)
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ac, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	ac.SetReadLimit(protocol.WSReadLimit)
	h := &fileHarness{t: t, s: <-ready, agent: ac, cancel: cancel}
	t.Cleanup(func() { cancel(); ac.CloseNow() })
	return h
}

// next, sahte ajan tarafinda siradaki mesaji okur.
func (h *fileHarness) next() (websocket.MessageType, []byte) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := h.agent.Read(ctx)
	if err != nil {
		h.t.Fatalf("ajan okuyamadi: %v", err)
	}
	return typ, data
}

func (h *fileHarness) sendCtl(v any, ty protocol.MessageType) {
	h.t.Helper()
	b, err := protocol.Marshal(v, ty)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.agent.Write(context.Background(), websocket.MessageText, b); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fileHarness) sendFrame(id uint64, seq uint32, p []byte) {
	h.t.Helper()
	f := protocol.BodyFrame{FrameType: protocol.FrameFile, ReqID: id, Seq: seq, Payload: p}
	if err := h.agent.Write(context.Background(), websocket.MessageBinary, f.Encode()); err != nil {
		h.t.Fatal(err)
	}
}

// expectOpen, file_open bekler ve cozer.
func (h *fileHarness) expectOpen() protocol.FileOpen {
	h.t.Helper()
	typ, data := h.next()
	if typ != websocket.MessageText {
		h.t.Fatal("metin bekleniyordu")
	}
	var m protocol.FileOpen
	if pt, _ := protocol.PeekType(data); pt != protocol.TypeFileOpen {
		h.t.Fatalf("file_open bekleniyordu: %s", pt)
	}
	_ = json.Unmarshal(data, &m)
	return m
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type putResult struct {
	done protocol.FileDone
	err  error
}

func TestFilePutHappyPath(t *testing.T) {
	h := newFileHarness(t, true)
	payload := bytes.Repeat([]byte("zorven"), 20000) // ~120 KB, birden cok parca
	res := make(chan putResult, 1)
	go func() {
		d, err := h.s.FilePut(context.Background(), "rapor.txt", int64(len(payload)), bytes.NewReader(payload))
		res <- putResult{d, err}
	}()

	open := h.expectOpen()
	if open.Dir != protocol.FileDirPut || open.Name != "rapor.txt" || open.Size != int64(len(payload)) {
		t.Fatalf("open=%+v", open)
	}
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Name: "rapor (1).txt"}, protocol.TypeFileOpenAck)

	var got []byte
	var end protocol.FileEnd
	var acker protocol.FileAcker
	for {
		typ, data := h.next()
		if typ == websocket.MessageBinary {
			f, err := protocol.DecodeBodyFrame(data)
			if err != nil || f.FrameType != protocol.FrameFile || f.ReqID != open.XferID {
				t.Fatalf("bozuk parca: %v %+v", err, f)
			}
			if len(f.Payload) > protocol.FileChunkSize {
				t.Fatalf("parca cok buyuk: %d", len(f.Payload))
			}
			got = append(got, f.Payload...)
			if tot, ok := acker.Consume(len(f.Payload)); ok {
				h.sendCtl(protocol.FileAck{Type: protocol.TypeFileAck, XferID: open.XferID, Bytes: tot}, protocol.TypeFileAck)
			}
			continue
		}
		if pt, _ := protocol.PeekType(data); pt == protocol.TypeFileEnd {
			_ = json.Unmarshal(data, &end)
			break
		}
	}
	if !bytes.Equal(got, payload) || end.Size != int64(len(payload)) || end.SHA256 != sum(payload) {
		t.Fatalf("veri/sha uyusmuyor: %d/%d %s", len(got), len(payload), end.SHA256)
	}
	h.sendCtl(protocol.FileDone{Type: protocol.TypeFileDone, XferID: open.XferID, OK: true, Name: "rapor (1).txt", Size: end.Size, SHA256: end.SHA256}, protocol.TypeFileDone)
	r := <-res
	if r.err != nil || r.done.Name != "rapor (1).txt" || !r.done.OK {
		t.Fatalf("sonuc: %+v %v", r.done, r.err)
	}
	h.s.fileMu.Lock()
	n := len(h.s.files)
	h.s.fileMu.Unlock()
	if n != 0 {
		t.Fatalf("aktarim kaydi temizlenmedi: %d", n)
	}
}

func TestFilePutBackpressure(t *testing.T) {
	h := newFileHarness(t, true)
	payload := make([]byte, 4<<20) // pencereden (1 MiB) cok buyuk
	res := make(chan putResult, 1)
	go func() {
		d, err := h.s.FilePut(context.Background(), "buyuk.bin", int64(len(payload)), bytes.NewReader(payload))
		res <- putResult{d, err}
	}()
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Name: "buyuk.bin"}, protocol.TypeFileOpenAck)

	// Ajan HIC ack vermeden okur: gonderen pencerede durmali. (coder/websocket'te
	// zaman asimli Read baglantiyi kapatir; bu yuzden arka plan okuyucu + sayac.)
	var mu sync.Mutex
	read := 0
	go func() {
		for {
			_, data, err := h.agent.Read(context.Background())
			if err != nil {
				return
			}
			if f, derr := protocol.DecodeBodyFrame(data); derr == nil && f.FrameType == protocol.FrameFile {
				mu.Lock()
				read += len(f.Payload)
				mu.Unlock()
			}
		}
	}()
	time.Sleep(800 * time.Millisecond)
	mu.Lock()
	got := read
	mu.Unlock()
	read = got
	if read > protocol.FileWindowBytes {
		t.Fatalf("ack olmadan %d bayt gitti (pencere %d)", read, protocol.FileWindowBytes)
	}
	if read < protocol.FileWindowBytes-protocol.FileChunkSize {
		t.Fatalf("pencere doldurulmadi: %d", read)
	}
	// Ack gelince devam eder.
	h.sendCtl(protocol.FileAck{Type: protocol.TypeFileAck, XferID: open.XferID, Bytes: int64(got)}, protocol.TypeFileAck)
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	more := read - got
	mu.Unlock()
	if more <= 0 {
		t.Fatal("ack sonrasi akis surmedi")
	}
	h.cancel()
}

func TestFilePutAgentRejects(t *testing.T) {
	h := newFileHarness(t, true)
	res := make(chan putResult, 1)
	go func() {
		d, err := h.s.FilePut(context.Background(), "x.txt", 3, strings.NewReader("abc"))
		res <- putResult{d, err}
	}()
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, Code: protocol.FileErrTooLarge, Message: "buyuk"}, protocol.TypeFileOpenAck)
	r := <-res
	var fe *FileError
	if !errors.As(r.err, &fe) || fe.Code != protocol.FileErrTooLarge {
		t.Fatalf("hata: %v", r.err)
	}
}

func TestFilePutChecksumMismatchReported(t *testing.T) {
	h := newFileHarness(t, true)
	res := make(chan putResult, 1)
	go func() {
		d, err := h.s.FilePut(context.Background(), "x.txt", 3, strings.NewReader("abc"))
		res <- putResult{d, err}
	}()
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Name: "x.txt"}, protocol.TypeFileOpenAck)
	for {
		_, data := h.next()
		if pt, _ := protocol.PeekType(data); pt == protocol.TypeFileEnd {
			break
		}
	}
	h.sendCtl(protocol.FileDone{Type: protocol.TypeFileDone, XferID: open.XferID, Code: protocol.FileErrChecksum, Message: "uyusmuyor"}, protocol.TypeFileDone)
	r := <-res
	var fe *FileError
	if !errors.As(r.err, &fe) || fe.Code != protocol.FileErrChecksum {
		t.Fatalf("checksum hatasi bekleniyordu: %v", r.err)
	}
}

func TestFilePutShortBodyAborts(t *testing.T) {
	h := newFileHarness(t, true)
	res := make(chan putResult, 1)
	go func() {
		d, err := h.s.FilePut(context.Background(), "x.txt", 10, strings.NewReader("abc"))
		res <- putResult{d, err}
	}()
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Name: "x.txt"}, protocol.TypeFileOpenAck)
	r := <-res
	if !errors.Is(r.err, errFileShortBody) {
		t.Fatalf("kisa govde: %v", r.err)
	}
	// Ajan'a abort gitmeli (yarim dosya silinsin).
	for i := 0; i < 5; i++ {
		_, data := h.next()
		if pt, _ := protocol.PeekType(data); pt == protocol.TypeFileAbort {
			return
		}
	}
	t.Fatal("file_abort gonderilmedi")
}

func TestFilePutCancelSendsAbort(t *testing.T) {
	h := newFileHarness(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan putResult, 1)
	go func() {
		d, err := h.s.FilePut(ctx, "x.txt", 3, strings.NewReader("abc"))
		res <- putResult{d, err}
	}()
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Name: "x.txt"}, protocol.TypeFileOpenAck)
	for {
		_, data := h.next()
		if pt, _ := protocol.PeekType(data); pt == protocol.TypeFileEnd {
			break
		}
	}
	cancel() // tarayici iptal etti, ajan done vermedi
	if r := <-res; r.err == nil {
		t.Fatal("iptal hata vermeli")
	}
	for i := 0; i < 5; i++ {
		_, data := h.next()
		if pt, _ := protocol.PeekType(data); pt == protocol.TypeFileAbort {
			return
		}
	}
	t.Fatal("iptalde file_abort gonderilmedi")
}

func TestFileConcurrencyLimitAndUnsupported(t *testing.T) {
	h := newFileHarness(t, true)
	for i := 0; i < protocol.MaxFileXfers; i++ {
		x, err := h.s.newXfer(protocol.FileDirPut)
		if err != nil {
			t.Fatal(err)
		}
		defer h.s.dropXfer(x)
	}
	if _, err := h.s.newXfer(protocol.FileDirGet); !errors.Is(err, ErrFileBusy) {
		t.Fatalf("4. aktarim reddedilmeli: %v", err)
	}
	h2 := newFileHarness(t, false)
	if _, err := h2.s.FilePut(context.Background(), "a", 1, strings.NewReader("a")); !errors.Is(err, ErrScreenFilesUnsupported) {
		t.Fatalf("desteksiz: %v", err)
	}
	if _, err := h2.s.FileList(context.Background()); !errors.Is(err, ErrScreenFilesUnsupported) {
		t.Fatalf("desteksiz liste: %v", err)
	}
	// Pazarlanmamissa gelen dosya mesajlari yok sayilir.
	h2.s.routeFileCtl(protocol.TypeFileAbort, []byte(`{"type":"file_abort","xfer_id":1}`))
	h2.s.routeFileData(protocol.BodyFrame{FrameType: protocol.FrameFile, ReqID: 1})
}

type getResult struct {
	end  protocol.FileEnd
	err  error
	body []byte
	size int64
}

func runGet(h *fileHarness, name string) chan getResult {
	res := make(chan getResult, 1)
	go func() {
		var buf bytes.Buffer
		var r getResult
		r.end, r.err = h.s.FileGet(context.Background(), name, func(size int64) error { r.size = size; return nil }, &buf)
		r.body = buf.Bytes()
		res <- r
	}()
	return res
}

func TestFileGetHappyPath(t *testing.T) {
	h := newFileHarness(t, true)
	payload := bytes.Repeat([]byte("abc"), 300000)
	acks := make(chan struct{}, 64)
	res := runGet(h, "a.bin")
	open := h.expectOpen()
	go func() {
		for {
			_, data, err := h.agent.Read(context.Background())
			if err != nil {
				return
			}
			if pt, _ := protocol.PeekType(data); pt == protocol.TypeFileAck {
				acks <- struct{}{}
			}
		}
	}()
	if open.Dir != protocol.FileDirGet || open.Name != "a.bin" {
		t.Fatalf("open=%+v", open)
	}
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Size: int64(len(payload))}, protocol.TypeFileOpenAck)
	var seq uint32
	for off := 0; off < len(payload); off += protocol.FileChunkSize {
		end := min(off+protocol.FileChunkSize, len(payload))
		h.sendFrame(open.XferID, seq, payload[off:end])
		seq++
	}
	h.sendCtl(protocol.FileEnd{Type: protocol.TypeFileEnd, XferID: open.XferID, Size: int64(len(payload)), SHA256: sum(payload)}, protocol.TypeFileEnd)
	r := <-res
	if r.err != nil || !bytes.Equal(r.body, payload) || r.size != int64(len(payload)) {
		t.Fatalf("indirme: %v %d", r.err, len(r.body))
	}
	select {
	case <-acks:
	case <-time.After(2 * time.Second):
		t.Fatal("alici file_ack gondermedi (kredi iadesi yok)")
	}
}

func TestFileGetChecksumMismatch(t *testing.T) {
	h := newFileHarness(t, true)
	res := runGet(h, "a.bin")
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Size: 3}, protocol.TypeFileOpenAck)
	h.sendFrame(open.XferID, 0, []byte("abc"))
	h.sendCtl(protocol.FileEnd{Type: protocol.TypeFileEnd, XferID: open.XferID, Size: 3, SHA256: sum([]byte("xyz"))}, protocol.TypeFileEnd)
	r := <-res
	var fe *FileError
	if !errors.As(r.err, &fe) || fe.Code != protocol.FileErrChecksum {
		t.Fatalf("checksum hatasi bekleniyordu: %v", r.err)
	}
}

func TestFileGetOversizeAndAgentAbort(t *testing.T) {
	h := newFileHarness(t, true)
	res := runGet(h, "a.bin")
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, OK: true, Size: 2}, protocol.TypeFileOpenAck)
	h.sendFrame(open.XferID, 0, []byte("abc")) // bildirilenden fazla
	r := <-res
	var fe *FileError
	if !errors.As(r.err, &fe) || fe.Code != protocol.FileErrChanged {
		t.Fatalf("fazla veri: %v", r.err)
	}

	h2 := newFileHarness(t, true)
	res2 := runGet(h2, "b.bin")
	o2 := h2.expectOpen()
	h2.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: o2.XferID, OK: true, Size: 9}, protocol.TypeFileOpenAck)
	h2.sendCtl(protocol.FileAbort{Type: protocol.TypeFileAbort, XferID: o2.XferID, Code: protocol.FileErrIO, Message: "disk"}, protocol.TypeFileAbort)
	r2 := <-res2
	if !errors.As(r2.err, &fe) || fe.Code != protocol.FileErrIO {
		t.Fatalf("ajan abort: %v", r2.err)
	}
}

func TestFileGetNotFound(t *testing.T) {
	h := newFileHarness(t, true)
	res := runGet(h, "yok.bin")
	open := h.expectOpen()
	h.sendCtl(protocol.FileOpenAck{Type: protocol.TypeFileOpenAck, XferID: open.XferID, Code: protocol.FileErrNotFound}, protocol.TypeFileOpenAck)
	r := <-res
	var fe *FileError
	if !errors.As(r.err, &fe) || fe.Code != protocol.FileErrNotFound {
		t.Fatalf("not_found: %v", r.err)
	}
}

func TestFileListAndSessionDrop(t *testing.T) {
	h := newFileHarness(t, true)
	type lr struct {
		res protocol.FileListResult
		err error
	}
	ch := make(chan lr, 1)
	go func() { r, err := h.s.FileList(context.Background()); ch <- lr{r, err} }()
	typ, data := h.next()
	var req protocol.FileList
	if typ != websocket.MessageText || json.Unmarshal(data, &req) != nil || req.Type != protocol.TypeFileList {
		t.Fatalf("file_list bekleniyordu: %s", data)
	}
	h.sendCtl(protocol.FileListResult{Type: protocol.TypeFileListResult, ReqID: req.ReqID, Dir: "/d",
		Files: []protocol.FileInfo{{Name: "a", Size: 1}}}, protocol.TypeFileListResult)
	r := <-ch
	if r.err != nil || r.res.Dir != "/d" || len(r.res.Files) != 1 {
		t.Fatalf("liste: %+v %v", r.res, r.err)
	}

	// Oturum kopunca bekleyen aktarim asili kalmaz.
	res := runGet(h, "a.bin")
	_ = h.expectOpen()
	h.agent.CloseNow()
	select {
	case r := <-res:
		if r.err == nil {
			t.Fatal("hata bekleniyordu")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("aktarim oturum kopunca asili kaldi")
	}
}

var _ sync.Mutex
