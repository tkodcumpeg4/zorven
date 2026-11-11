package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Regresyon (Y2): karsi taraf veriyi yollayip hemen kapattiginda tuketicinin
// FromLocal/Closed select'i kapanisi kalan veriden once secebiliyordu. Kuyruk
// artik Closed'i yalniz tum veri teslim edildikten sonra kapatir.
func TestStreamQueueDeliversAllBeforeClosed(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		var mem atomic.Int64
		q := newStreamQueue(&mem, 0, 0)
		const n = 500
		for i := 0; i < n; i++ {
			if err := q.push(make([]byte, 1000)); err != nil {
				t.Fatal(err)
			}
		}
		q.remoteClose()
		got := 0
	loop:
		for {
			select {
			case <-q.out:
				got++
			case <-q.closed:
				break loop
			}
		}
		if got != n {
			t.Fatalf("deneme %d: %d/%d oge teslim edildi", trial, got, n)
		}
		select {
		case <-q.aborted:
			t.Fatal("duzgun kapanis iptal sayilmamali")
		default:
		}
		if mem.Load() != 0 {
			t.Fatalf("oturum bellegi sifirlanmadi: %d", mem.Load())
		}
	}
}

// Eski ajan (kredi yok): bayt tavani asilinca push hata doner; iptal oturum
// bellek sayacini sifirlar.
func TestStreamQueueLegacyByteCap(t *testing.T) {
	var mem atomic.Int64
	q := newStreamQueue(&mem, rawLegacyMaxMem, 0)
	var err error
	pushed := 0
	for i := 0; i < 10000 && err == nil; i++ {
		err = q.push(make([]byte, 32<<10))
		if err == nil {
			pushed++
		}
	}
	if !errors.Is(err, errStreamOverflow) {
		t.Fatalf("tasma bekleniyordu: %v", err)
	}
	if held := mem.Load(); held > rawLegacyMaxMem {
		t.Fatalf("tavan asildi: %d", held)
	}
	q.abort()
	if mem.Load() != 0 {
		t.Fatalf("iptal sonrasi bellek: %d", mem.Load())
	}
	if pushed == 0 || pushed*(32<<10) > rawLegacyMaxMem {
		t.Fatalf("beklenmeyen kabul: %d", pushed)
	}
}

// Oturum tavani: cok akisla toplam bellek sisirilemez.
func TestStreamQueueSessionCap(t *testing.T) {
	var mem atomic.Int64
	mem.Store(sessionStreamMemMax - 1000)
	q := newStreamQueue(&mem, 0, 0)
	defer q.abort()
	if err := q.push(make([]byte, 4096)); !errors.Is(err, errSessionStreamMem) {
		t.Fatalf("oturum tavani bekleniyordu: %v", err)
	}
}

// --- gercek oturumla (sahte ajan) -------------------------------------------

func (h *fileHarness) expectType(want protocol.MessageType) []byte {
	h.t.Helper()
	for {
		typ, data := h.next()
		if typ != websocket.MessageText {
			continue
		}
		if pt, _ := protocol.PeekType(data); pt == want {
			return data
		}
	}
}

func (h *fileHarness) sendData(ft uint8, id uint64, p []byte, eof bool) {
	h.t.Helper()
	f := protocol.BodyFrame{FrameType: ft, ReqID: id, Payload: p, EOF: eof}
	if err := h.agent.Write(context.Background(), websocket.MessageBinary, f.Encode()); err != nil {
		h.t.Fatal(err)
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("zaman asimi: %s", what)
	}
}

// Y3 (eski ajan): okumayan ziyaretci varken ajan veri basarsa sunucu bellegi
// tavanda kalir, akis IPTAL edilir ve ajana StreamClose gider.
func TestRawLegacyNonReaderBoundedAndAborted(t *testing.T) {
	h := newFileHarness(t, false)
	rs, err := h.s.OpenStream(context.Background(), "tun", protocol.ProtoTCP, "1.2.3.4:5")
	if err != nil {
		t.Fatal(err)
	}
	var open protocol.StreamOpen
	_ = json.Unmarshal(h.expectType(protocol.TypeStreamOpen), &open)
	if !protocol.HasCap(open.Caps, protocol.StreamCapCredit) || protocol.HasCap(open.Caps, protocol.StreamCapHalfClose) {
		t.Fatalf("OpenStream yalniz kredi onermeli: %v", open.Caps)
	}
	h.sendCtl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: open.ReqID}, protocol.TypeStreamAck) // eski ajan: caps yok
	<-rs.Accept()

	chunk := make([]byte, 32<<10)
	go func() {
		for i := 0; i < 640; i++ { // 20 MB
			f := protocol.BodyFrame{FrameType: protocol.FrameStreamData, ReqID: open.ReqID, Payload: chunk}
			if h.agent.Write(context.Background(), websocket.MessageBinary, f.Encode()) != nil {
				return
			}
		}
	}()
	var peak int64
	deadline := time.After(10 * time.Second)
	for {
		if v := h.s.streamMem.Load(); v > peak {
			peak = v
		}
		select {
		case <-rs.Aborted():
			goto aborted
		case <-deadline:
			t.Fatal("akis iptal edilmedi")
		case <-time.After(time.Millisecond):
		}
	}
aborted:
	if peak > rawLegacyMaxMem {
		t.Fatalf("bellek tavani asildi: %d", peak)
	}
	var sc protocol.StreamClose
	_ = json.Unmarshal(h.expectType(protocol.TypeStreamClose), &sc)
	if sc.ReqID != open.ReqID {
		t.Fatalf("StreamClose: %+v", sc)
	}
	waitFor(t, "oturum bellegi sifirlanmali", func() bool { return h.s.streamMem.Load() == 0 })
}

// Y3 (yeni ajan): kredi pazarlanirsa tuketici veriyi aldikca sunucu ajana
// WindowUpdate gonderir; pencereyi asan ajan akisi kaybeder.
func TestRawCreditGrantsAndViolation(t *testing.T) {
	h := newFileHarness(t, false)
	rs, err := h.s.OpenStream(context.Background(), "tun", protocol.ProtoTCP, "r")
	if err != nil {
		t.Fatal(err)
	}
	var open protocol.StreamOpen
	_ = json.Unmarshal(h.expectType(protocol.TypeStreamOpen), &open)
	h.sendCtl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: open.ReqID, Caps: []string{protocol.StreamCapCredit}}, protocol.TypeStreamAck)
	<-rs.Accept()

	chunk := make([]byte, 32<<10)
	perFrame := len(chunk) + protocol.StreamFrameCost
	frames := protocol.InitialWindowBytes / perFrame // pencere icinde
	for i := 0; i < frames; i++ {
		h.sendData(protocol.FrameStreamData, open.ReqID, chunk, false)
	}
	for i := 0; i < frames; i++ {
		select {
		case <-rs.FromLocal():
		case <-time.After(5 * time.Second):
			t.Fatalf("cerceve %d teslim edilmedi", i)
		}
	}
	var wu protocol.WindowUpdate
	_ = json.Unmarshal(h.expectType(protocol.TypeWindowUpdate), &wu)
	if wu.ReqID != open.ReqID || wu.Increment < protocol.InitialWindowBytes/2 {
		t.Fatalf("kredi iadesi: %+v", wu)
	}
	// Iade edilenden fazlasini (pencere + 1 MB) okumadan bas: ihlal -> iptal.
	go func() {
		for i := 0; i < 3*frames; i++ {
			f := protocol.BodyFrame{FrameType: protocol.FrameStreamData, ReqID: open.ReqID, Payload: chunk}
			if h.agent.Write(context.Background(), websocket.MessageBinary, f.Encode()) != nil {
				return
			}
		}
	}()
	waitClosed(t, rs.Aborted(), "pencere ihlali akisi iptal etmeli")
}

// Kredi (sunucu -> ajan): pencere bitince Send bekler, WindowUpdate gelince akar.
func TestRawCreditSendBlocksUntilGrant(t *testing.T) {
	h := newFileHarness(t, false)
	rs, err := h.s.OpenStream(context.Background(), "tun", protocol.ProtoTCP, "r")
	if err != nil {
		t.Fatal(err)
	}
	var open protocol.StreamOpen
	_ = json.Unmarshal(h.expectType(protocol.TypeStreamOpen), &open)
	h.sendCtl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: open.ReqID, Caps: []string{protocol.StreamCapCredit}}, protocol.TypeStreamAck)
	<-rs.Accept()

	// Ajan tarafinda gelen cerceveleri say.
	var recv atomic.Int64
	go func() {
		for {
			typ, data, err := h.agent.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if f, err := protocol.DecodeBodyFrame(data); err == nil {
					recv.Add(int64(len(f.Payload)))
				}
			}
		}
	}()
	chunk := make([]byte, 32<<10)
	sent := make(chan int, 1)
	go func() {
		n := 0
		for i := 0; i < 200; i++ { // ~6.4 MB > pencere
			if rs.Send(context.Background(), chunk) != nil {
				break
			}
			n++
		}
		sent <- n
	}()
	time.Sleep(300 * time.Millisecond)
	window := protocol.InitialWindowBytes / (len(chunk) + protocol.StreamFrameCost)
	if got := recv.Load(); got > int64(window*len(chunk)) {
		t.Fatalf("kredisiz pencereden fazla gonderildi: %d", got)
	}
	// Kredi iade et: kalanlar akmali.
	for i := 0; i < 10; i++ {
		h.sendCtl(protocol.WindowUpdate{Type: protocol.TypeWindowUpdate, ReqID: open.ReqID, Increment: protocol.InitialWindowBytes}, protocol.TypeWindowUpdate)
	}
	select {
	case n := <-sent:
		if n != 200 {
			t.Fatalf("gonderilen: %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kredi gelince Send devam etmeliydi")
	}
	// Kapaninca bekleyen Send cozulmeli.
	rs2, _ := h.s.OpenStream(context.Background(), "tun", protocol.ProtoTCP, "r")
	waitFor(t, "ikinci open", func() bool { _, ok := h.s.getRaw(rs2.ReqID()); return ok })
	h.sendCtl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: rs2.ReqID(), Caps: []string{protocol.StreamCapCredit}}, protocol.TypeStreamAck)
	<-rs2.Accept()
	errc := make(chan error, 1)
	go func() {
		var err error
		for err == nil {
			err = rs2.Send(context.Background(), chunk)
		}
		errc <- err
	}()
	time.Sleep(200 * time.Millisecond)
	rs2.Close("test")
	select {
	case err := <-errc:
		if !errors.Is(err, ErrStreamClosed) {
			t.Fatalf("hata: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kapanista kredi beklemesi cozulmedi")
	}
}

// Y2 + O12: yari kapamada EOF isareti veriden SONRA teslim edilir, akis acik
// kalir; CloseWrite ajana bos EOF cercevesi gonderir.
func TestRawHalfCloseOrdering(t *testing.T) {
	h := newFileHarness(t, false)
	rs, err := h.s.OpenStreamHalf(context.Background(), "tun", protocol.ProtoTCP, "r")
	if err != nil {
		t.Fatal(err)
	}
	var open protocol.StreamOpen
	_ = json.Unmarshal(h.expectType(protocol.TypeStreamOpen), &open)
	if !protocol.HasCap(open.Caps, protocol.StreamCapHalfClose) {
		t.Fatalf("yari kapama onerilmeli: %v", open.Caps)
	}
	h.sendCtl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: open.ReqID,
		Caps: []string{protocol.StreamCapCredit, protocol.StreamCapHalfClose}}, protocol.TypeStreamAck)
	<-rs.Accept()
	if !rs.HalfClose() {
		t.Fatal("yari kapama pazarlanmali")
	}
	want := bytes.Repeat([]byte("abc"), 100000)
	for off := 0; off < len(want); off += 16 << 10 {
		end := min(off+16<<10, len(want))
		h.sendData(protocol.FrameStreamData, open.ReqID, want[off:end], false)
	}
	h.sendData(protocol.FrameStreamData, open.ReqID, nil, true)
	var got []byte
	eof := rs.RemoteEOF()
	for eof != nil {
		select {
		case d := <-rs.FromLocal():
			got = append(got, d...)
		case <-eof:
			eof = nil
		case <-rs.Closed():
			t.Fatal("yari kapamada akis kapanmamali")
		case <-time.After(5 * time.Second):
			t.Fatal("zaman asimi")
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EOF'tan once tum veri gelmeli: %d/%d", len(got), len(want))
	}
	if !rs.CloseWrite() {
		t.Fatal("CloseWrite yari kapama yolunu kullanmali")
	}
	for {
		typ, data := h.next()
		if typ != websocket.MessageBinary {
			continue
		}
		f, err := protocol.DecodeBodyFrame(data)
		if err != nil || f.ReqID != open.ReqID {
			continue
		}
		if len(f.Payload) != 0 || !f.EOF {
			t.Fatalf("bos EOF cercevesi bekleniyordu: %+v", f)
		}
		break
	}
}

// Eski ajan: CloseWrite StreamClose gonderir ama kopru kalir; ajanin sonradan
// gelen verisi ve StreamClose'u hala teslim edilir (son baytlar kaybolmaz).
func TestRawLegacyCloseWriteKeepsBridge(t *testing.T) {
	h := newFileHarness(t, false)
	rs, _ := h.s.OpenStreamHalf(context.Background(), "tun", protocol.ProtoTCP, "r")
	var open protocol.StreamOpen
	_ = json.Unmarshal(h.expectType(protocol.TypeStreamOpen), &open)
	h.sendCtl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: open.ReqID}, protocol.TypeStreamAck)
	<-rs.Accept()
	if rs.CloseWrite() {
		t.Fatal("eski ajanda yari kapama olmamali")
	}
	h.expectType(protocol.TypeStreamClose)
	h.sendData(protocol.FrameStreamData, open.ReqID, []byte("yanit"), false)
	h.sendCtl(protocol.StreamClose{Type: protocol.TypeStreamClose, ReqID: open.ReqID}, protocol.TypeStreamClose)
	var got []byte
	for {
		select {
		case d := <-rs.FromLocal():
			got = append(got, d...)
			continue
		case <-rs.Closed():
		case <-time.After(5 * time.Second):
			t.Fatal("zaman asimi")
		}
		break
	}
	if string(got) != "yanit" {
		t.Fatalf("got %q", got)
	}
}

// Y1: eski ajanla WS akisi 2 MB'ta kesilir; kredili ajanda pencere asimi kesilir.
func TestWSLegacyCapAborts(t *testing.T) {
	h := newFileHarness(t, false)
	ws, err := h.s.OpenWS(context.Background(), "tun", "/", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var open protocol.WSOpen
	_ = json.Unmarshal(h.expectType(protocol.TypeWSOpen), &open)
	if !protocol.HasCap(open.Caps, protocol.StreamCapCredit) {
		t.Fatalf("WS kredi onermeli: %v", open.Caps)
	}
	h.sendCtl(protocol.WSAccept{Type: protocol.TypeWSAccept, ReqID: open.ReqID, Status: http.StatusSwitchingProtocols}, protocol.TypeWSAccept)
	<-ws.Accept()
	chunk := make([]byte, 32<<10)
	go func() {
		for i := 0; i < 320; i++ { // 10 MB
			f := protocol.BodyFrame{FrameType: protocol.FrameWSData, ReqID: open.ReqID, Payload: chunk}
			if h.agent.Write(context.Background(), websocket.MessageBinary, f.Encode()) != nil {
				return
			}
		}
	}()
	waitClosed(t, ws.Aborted(), "WS tavani asilinca akis iptal edilmeli")
	h.expectType(protocol.TypeWSClose)
	waitFor(t, "oturum bellegi sifirlanmali", func() bool { return h.s.streamMem.Load() == 0 })
}

// Y1 (d): tunel basina eszamanli WS tavani.
func TestWSPerTunnelLimit(t *testing.T) {
	h := newFileHarness(t, false)
	go func() { // ajan tarafini bosalt (WSOpen mesajlari)
		for {
			if _, _, err := h.agent.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	for i := 0; i < maxWSPerTunnel; i++ {
		if _, err := h.s.OpenWS(context.Background(), "tun", "/", "", nil); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := h.s.OpenWS(context.Background(), "tun", "/", "", nil); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("tavan bekleniyordu: %v", err)
	}
	if _, err := h.s.OpenWS(context.Background(), "baska", "/", "", nil); err != nil {
		t.Fatalf("baska tunel etkilenmemeli: %v", err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zaman asimi: %s", what)
}
