package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// bwHarness: gercek WS cifti; istemci tarafi dialHTTPClient (corkConn) ile kurulur.
type bwHarness struct {
	cli   *websocket.Conn
	cork  *corkConn
	recv  chan []byte
	srv   *httptest.Server
	hold  chan struct{} // kapaliysa sunucu okur; acik kalirsa okumayi bekletir
	close func()
}

func newBWHarness(t *testing.T, useTLS, holdReads bool) *bwHarness {
	t.Helper()
	h := &bwHarness{recv: make(chan []byte, 1<<16), hold: make(chan struct{})}
	if !holdReads {
		close(h.hold)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 24)
		<-h.hold
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			h.recv <- data
		}
	})
	if useTLS {
		h.srv = httptest.NewTLSServer(handler)
	} else {
		h.srv = httptest.NewServer(handler)
	}
	a := &Agent{}
	if useTLS {
		a.httpClient = h.srv.Client()
	}
	slot := &corkSlot{}
	scheme := "ws"
	if useTLS {
		scheme = "wss"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _, err := websocket.Dial(ctx, scheme+strings.TrimPrefix(strings.TrimPrefix(h.srv.URL, "https"), "http"),
		&websocket.DialOptions{HTTPClient: a.dialHTTPClient(slot)})
	if err != nil {
		t.Fatal(err)
	}
	h.cli = cli
	h.cork = slot.get()
	if h.cork == nil {
		t.Fatal("corkConn yakalanmadi")
	}
	h.close = func() { cli.CloseNow(); h.srv.Close() }
	return h
}

func msgOf(sender, seq int, size int) []byte {
	b := make([]byte, 8+size)
	binary.BigEndian.PutUint32(b[0:4], uint32(sender))
	binary.BigEndian.PutUint32(b[4:8], uint32(seq))
	for i := 8; i < len(b); i++ {
		b[i] = byte(sender + seq + i)
	}
	return b
}

// Ayni goroutine'in mesajlari sirasiyla ulasmali (veri cerceveleri ile
// ardindan gelen kapanis mesaji arasindaki FIFO), govdeler bozulmamali (TLS
// alti tikama), coder/websocket'in dogrudan yazdigi ping'ler araya girse bile.
func TestBatchWriterOrderAndIntegrityTLS(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", useTLS), func(t *testing.T) {
			h := newBWHarness(t, useTLS, false)
			defer h.close()
			bw := newBatchWriter(h.cli, h.cork)
			const senders, per = 40, 50
			stopPing := make(chan struct{})
			var pingWG sync.WaitGroup
			pingWG.Add(1)
			go func() { // sunucu okuyor: Ping'e otomatik pong doner, dogrudan yazim yolu
				defer pingWG.Done()
				for {
					select {
					case <-stopPing:
						return
					default:
					}
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = h.cli.Ping(ctx)
					cancel()
				}
			}()
			// Ping yaniti icin istemcinin okumasi gerekir.
			go func() {
				for {
					if _, _, err := h.cli.Read(context.Background()); err != nil {
						return
					}
				}
			}()
			var wg sync.WaitGroup
			for s := 0; s < senders; s++ {
				wg.Add(1)
				go func(s int) {
					defer wg.Done()
					for i := 0; i < per; i++ {
						size := 10 + (i*977)%70000
						urgent := i%7 == 3
						if err := bw.write(context.Background(), websocket.MessageBinary, msgOf(s, i, size), time.Second*10, urgent); err != nil {
							t.Errorf("yazim: %v", err)
							return
						}
					}
				}(s)
			}
			wg.Wait()
			close(stopPing)
			pingWG.Wait()
			last := make([]int, senders)
			for i := range last {
				last[i] = -1
			}
			deadline := time.After(10 * time.Second)
			for n := 0; n < senders*per; n++ {
				var b []byte
				select {
				case b = <-h.recv:
				case <-deadline:
					t.Fatalf("yalniz %d/%d mesaj ulasti", n, senders*per)
				}
				s, seq := int(binary.BigEndian.Uint32(b[0:4])), int(binary.BigEndian.Uint32(b[4:8]))
				want := msgOf(s, seq, len(b)-8)
				if string(want) != string(b) {
					t.Fatalf("mesaj bozuk: gonderen %d sira %d", s, seq)
				}
				if seq != last[s]+1 {
					t.Fatalf("gonderen %d: sira %d, beklenen %d", s, seq, last[s]+1)
				}
				last[s] = seq
			}
		})
	}
}

// Eszamanli gonderimde mesajlar birlestirilmeli: ham TCP yazim sayisi mesaj
// sayisindan belirgin az olmali (O13'un amaci).
func TestBatchWriterCoalesces(t *testing.T) {
	h := newBWHarness(t, false, false)
	defer h.close()
	cnt := &countConn{Conn: h.cork.Conn}
	h.cork.Conn = cnt
	bw := newBatchWriter(h.cli, h.cork)
	const senders, per = 50, 40
	var wg sync.WaitGroup
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				_ = bw.write(context.Background(), websocket.MessageBinary, msgOf(s, i, 64), 10*time.Second, false)
			}
		}(s)
	}
	wg.Wait()
	if w := cnt.writes.Load(); w >= senders*per {
		t.Fatalf("birlestirme yok: %d mesaj icin %d yazim", senders*per, w)
	} else {
		t.Logf("%d mesaj, %d TCP yazimi", senders*per, w)
	}
}

type countConn struct {
	net.Conn
	writes atomic.Int64
}

func (c *countConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(p)
}

// Sunucu okumazsa kuyruk bayt tavaninda durmali (gonderenler bekler) ve
// baglanti kapaninca bekleyenlerin hepsi hata ile cozulmeli.
func TestBatchWriterBackpressureAndClose(t *testing.T) {
	h := newBWHarness(t, false, true)
	defer h.close()
	bw := newBatchWriter(h.cli, h.cork)
	const senders = 64
	var done atomic.Int32
	errs := make(chan error, senders)
	for s := 0; s < senders; s++ {
		go func(s int) {
			var err error
			for i := 0; err == nil; i++ {
				err = bw.write(context.Background(), websocket.MessageBinary, msgOf(s, i, 256<<10), 10*time.Second, false)
			}
			done.Add(1)
			errs <- err
		}(s)
	}
	// TCP tamponlari dolana ve kuyruk tavana dayanana kadar bekle.
	var maxQ int
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		bw.mu.Lock()
		if bw.queued > maxQ {
			maxQ = bw.queued
		}
		bw.mu.Unlock()
	}
	if maxQ > batchQueueMaxBytes+(256<<10)+8 {
		t.Fatalf("kuyruk tavani asildi: %d", maxQ)
	}
	if done.Load() != 0 {
		t.Fatalf("okumayan sunucuda gonderim bitmemeliydi: %d", done.Load())
	}
	// Oturum kapanisi (closeAll) + baglanti kapanisi: herkes cozulmeli.
	bw.close(errConnClosed)
	h.cli.CloseNow()
	for s := 0; s < senders; s++ {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("hata bekleniyordu")
			}
		case <-time.After(35 * time.Second):
			t.Fatalf("%d gonderen asili kaldi", senders-s)
		}
	}
	if err := bw.write(context.Background(), websocket.MessageText, []byte(`{"type":"x"}`), time.Second, true); err == nil {
		t.Fatal("kapali yazici hata donmeli")
	}
}

// Yazim hatasi kalici: sonraki gonderimler ayni hatayi alir, baglanti kapanir.
func TestBatchWriterErrorIsSticky(t *testing.T) {
	h := newBWHarness(t, false, false)
	defer h.close()
	bw := newBatchWriter(h.cli, h.cork)
	_ = h.cork.Conn.Close() // alt baglanti gitti
	err1 := bw.write(context.Background(), websocket.MessageBinary, []byte("abc"), time.Second, false)
	if err1 == nil {
		t.Fatal("hata bekleniyordu")
	}
	err2 := bw.write(context.Background(), websocket.MessageText, []byte(`{"type":"pong"}`), time.Second, true)
	if err2 == nil || !errors.Is(err2, net.ErrClosed) && !strings.Contains(err2.Error(), "yazim basarisiz") {
		t.Fatalf("kalici hata bekleniyordu: %v", err2)
	}
}

// Iptal edilmis baglamla gonderim hic baslamaz; kuyruktaki mesaj icin iptal
// yazimi kesmez (coder/websocket iptalde tum baglantiyi kapatirdi).
func TestBatchWriterCanceledCtx(t *testing.T) {
	h := newBWHarness(t, false, false)
	defer h.close()
	bw := newBatchWriter(h.cli, h.cork)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bw.write(ctx, websocket.MessageBinary, []byte("x"), time.Second, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("iptal hatasi bekleniyordu: %v", err)
	}
	if err := bw.write(context.Background(), websocket.MessageBinary, []byte("y"), time.Second, false); err != nil {
		t.Fatalf("baglanti saglam kalmali: %v", err)
	}
	select {
	case b := <-h.recv:
		if string(b) != "y" {
			t.Fatalf("beklenmeyen %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mesaj ulasmadi")
	}
}

// Parti secimi: acil mesajlar once; acil sahibi yalniz acilleri yazar; normal
// mesajlarin sirasi korunur ve parti tavani uygulanir.
func TestBatchWriterTake(t *testing.T) {
	w := newBatchWriter(nil, nil)
	mk := func(n int, urgent bool) *wmsg { return &wmsg{data: make([]byte, n), urgent: urgent} }
	n1, n2, n3 := mk(batchMaxBytes-10, false), mk(20, false), mk(5, false)
	u1 := mk(3, true)
	w.normal = []*wmsg{n1, n2, n3}
	w.queued = len(n1.data) + len(n2.data) + len(n3.data)
	w.urgent = []*wmsg{u1}
	b := w.takeLocked(true)
	if len(b) != 1 || b[0] != u1 {
		t.Fatalf("acil sahibi yalniz acili almali: %d", len(b))
	}
	b = w.takeLocked(false)
	if len(b) != 1 || b[0] != n1 {
		t.Fatalf("tavan: yalniz n1 beklenirdi, %d", len(b))
	}
	b = w.takeLocked(false)
	if len(b) != 2 || b[0] != n2 || b[1] != n3 || w.queued != 0 {
		t.Fatalf("kalanlar sirayla: %d queued=%d", len(b), w.queued)
	}
}

func TestCorkConnSingleWrite(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	cnt := &countConn{Conn: a}
	c := &corkConn{Conn: cnt}
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := b.Read(buf)
		got <- buf[:n]
	}()
	c.cork()
	_, _ = c.Write([]byte("ab"))
	_, _ = c.Write([]byte("cd"))
	if err := c.uncork(time.Second); err != nil {
		t.Fatal(err)
	}
	if s := string(<-got); s != "abcd" || cnt.writes.Load() != 1 {
		t.Fatalf("tek yazim bekleniyordu: %q %d", s, cnt.writes.Load())
	}
}
