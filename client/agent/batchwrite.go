package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Sunucu baglantisina toplu (grup) yazim (O13 / A1 #4).
//
// NEDEN: eskiden her mesaj tek bir writeMu altinda ayri bir conn.Write ile
// yaziliyordu; coder/websocket her mesajin sonunda bufio'yu bosaltir, TLS her
// bosaltmayi ayri kayit + ayri write syscall'i yapar (64 KB'lik govde
// cercevesi 4 KB'lik bufio yuzunden 16 syscall). c=50 kucuk istek yukunde
// ajanin mutex profili 583 sn bekleme gosteriyordu ve istek basina ~5 syscall
// vardi: darbogaz agin kendisi degil, kilit el degistirme + syscall sayisiydi.
//
// TASARIM (grup yazim / "group commit"): ayri, uzun omurlu bir yazici
// goroutine yerine kuyruga ilk ekleyen ve o an yazan kimse yoksa YAZICI olur:
// kuyrukta biriken mesajlari (kendi mesaji dahil) tek parti halinde yazar ve
// alttaki TCP baglantisina TEK write ile bosaltir (corkConn). Kendi mesaji
// yazilinca rolu kuyrugun basindaki mesajin sahibine devreder. Boylece:
//   - ayni anda tek yazici vardir ve mesajlar KUYRUGA GIRIS sirasiyla yazilir
//     (ayni goroutine'in veri cerceveleri ile ardindan gelen StreamClose /
//     EOF / http_error arasindaki sira korunur);
//   - hicbir yazici kendi mesajindan sonra baskalarinin yukunu sonsuza dek
//     tasimaz (devir); okuma dongusu (pong) uzun partide takili kalmaz;
//   - baglanti basina yasayan/sizan goroutine yoktur (testlerde ve kopmada
//     temizlenecek bir sey yok).
//
// Oncelik: pong ve window_update "acil"dir; normal mesajlarin onune gecer.
// Sira baglantisi olmayan (yalniz kredi iadesi / canlilik) mesajlardir ve
// one gecmeleri yalniz daha ERKEN ulasmalari demektir; veri ile kapanis
// mesajlari arasindaki sira acil kuyruga girmedikleri icin bozulmaz. Acil
// mesaj sahibi yazici olursa yalniz acil mesajlari yazar: okuma dongusu buyuk
// bir veri partisinin ag yazimini beklemez.
//
// Geri basinc: normal kuyruk bayt ile sinirlidir (batchQueueMaxBytes); dolunca
// gonderen yer acilana kadar bekler. Her gonderen zaten kendi mesaji yazilana
// kadar bekledigi icin (eskisi gibi) kuyruk, eszamanli gonderen sayisiyla da
// sinirlidir.
//
// Hata: bir yazim basarisiz olursa (zaman asimi dahil) yazici kalici hataya
// gecer, bekleyen ve sonraki tum gonderimler ayni hatayi alir ve baglanti
// kapatilir (okuma dongusu duser, ajan yeniden baglanir). Eskiden de yazim
// zaman asimi coder/websocket'te baglantiyi kapatiyordu.

const (
	// batchQueueMaxBytes, yazilmayi bekleyen normal mesajlarin bayt tavani.
	batchQueueMaxBytes = 8 << 20
	// batchMaxBytes, tek partide (tek bosaltmada) yazilan normal mesaj tavani:
	// parti buyudukce yazicinin ag yazimi uzar, digerleri ilk sonucu gec alir.
	batchMaxBytes = 512 << 10
	// corkFlushTimeout, bir partinin aga yazilma zaman asimi (eski en uzun
	// mesaj zaman asimiyla ayni).
	corkFlushTimeout = 30 * time.Second
)

// errWriterLead, bekleyen gonderene "yazici artik sensin" der (ic isaret).
var errWriterLead = errors.New("yazici rolu devredildi")

// errConnClosed, oturum kapanirken bekleyen gonderimlerin hatasi.
var errConnClosed = errors.New("sunucu baglantisi kapandi")

type wmsg struct {
	typ     websocket.MessageType
	data    []byte
	timeout time.Duration
	urgent  bool
	res     chan error // tamponlu(1): tam olarak bir deger alir
}

type batchWriter struct {
	conn *websocket.Conn
	// cork, alttaki TCP baglantisi (TLS'in altinda). nil ise birlestirme yok:
	// her mesaj ayri yazilir (testler, ozel tasiyici); sira ve tek yazici yine gecerli.
	cork *corkConn

	mu      sync.Mutex
	space   *sync.Cond
	urgent  []*wmsg
	normal  []*wmsg
	queued  int // normal kuyruktaki bayt
	writing bool
	err     error
}

func newBatchWriter(conn *websocket.Conn, cork *corkConn) *batchWriter {
	w := &batchWriter{conn: conn, cork: cork}
	w.space = sync.NewCond(&w.mu)
	return w
}

// write, mesaji kuyruga alir ve yazilana (ya da yazici hata alana) kadar
// bekler. ctx yalniz baslangicta denetlenir: kuyruga girmis mesaj iptalle
// geri alinmaz (yarim cerceve yazimi baglantiyi bozardi; bkz. connWriteCtx).
func (w *batchWriter) write(ctx context.Context, typ websocket.MessageType, data []byte, timeout time.Duration, urgent bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m := &wmsg{typ: typ, data: data, timeout: timeout, urgent: urgent, res: make(chan error, 1)}
	w.mu.Lock()
	if !urgent {
		for w.err == nil && w.queued > 0 && w.queued+len(data) > batchQueueMaxBytes {
			w.space.Wait()
		}
	}
	if w.err != nil {
		err := w.err
		w.mu.Unlock()
		return err
	}
	if urgent {
		w.urgent = append(w.urgent, m)
	} else {
		w.normal = append(w.normal, m)
		w.queued += len(data)
	}
	if w.writing {
		w.mu.Unlock()
		err := <-m.res
		if err != errWriterLead {
			return err
		}
		return w.drain(m)
	}
	w.writing = true
	w.mu.Unlock()
	return w.drain(m)
}

// close, yaziciyi kalici hataya gecirir: bekleyen ve sonraki gonderimler err
// alir. Kuyruk doluysa bir yazici vardir; onu bir sonraki adiminda temizler.
func (w *batchWriter) close(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.space.Broadcast()
	w.mu.Unlock()
}

// drain, yazici rolundeki goroutine'in dongusu: own yazilana kadar partiler
// yazar, sonra rolu devreder. mu tutulmadan cagrilir.
func (w *batchWriter) drain(own *wmsg) error {
	for {
		w.mu.Lock()
		if w.err != nil {
			err := w.err
			w.failQueuedLocked(own, err)
			w.mu.Unlock()
			return err
		}
		batch := w.takeLocked(own.urgent)
		w.space.Broadcast()
		w.mu.Unlock()

		err := w.flush(batch)

		w.mu.Lock()
		if err != nil {
			if w.err == nil {
				w.err = fmt.Errorf("sunucuya yazim basarisiz: %w", err)
			}
			err = w.err
			for _, m := range batch {
				if m != own {
					m.res <- err
				}
			}
			w.failQueuedLocked(own, err)
			w.mu.Unlock()
			// Yarim yazilmis cerceve ya da TLS kaydi baglantiyi kullanilamaz kilar.
			_ = w.conn.CloseNow()
			return err
		}
		ownDone := false
		for _, m := range batch {
			if m == own {
				ownDone = true
				continue
			}
			m.res <- nil
		}
		if !ownDone {
			w.mu.Unlock()
			continue
		}
		// Rolu devret: kuyrukta mesaji olan herkes kendi res kanalinda bekliyor.
		if next := w.headLocked(); next != nil {
			next.res <- errWriterLead
		} else {
			w.writing = false
		}
		w.mu.Unlock()
		return nil
	}
}

// failQueuedLocked, kuyruktaki tum mesajlara err verir (own haric; onun
// sonucunu yazici kendisi doner) ve yazici rolunu birakir.
func (w *batchWriter) failQueuedLocked(own *wmsg, err error) {
	for _, m := range w.urgent {
		if m != own {
			m.res <- err
		}
	}
	for _, m := range w.normal {
		if m != own {
			m.res <- err
		}
	}
	w.urgent, w.normal, w.queued = nil, nil, 0
	w.writing = false
	w.space.Broadcast()
}

func (w *batchWriter) headLocked() *wmsg {
	if len(w.urgent) > 0 {
		return w.urgent[0]
	}
	if len(w.normal) > 0 {
		return w.normal[0]
	}
	return nil
}

// takeLocked, bir parti cikarir: once tum acil mesajlar, sonra (urgentOnly
// degilse ya da acil yoksa) batchMaxBytes'a kadar normal mesajlar (en az bir).
func (w *batchWriter) takeLocked(urgentOnly bool) []*wmsg {
	batch := w.urgent
	w.urgent = nil
	if urgentOnly && len(batch) > 0 {
		return batch
	}
	n, size := 0, 0
	for n < len(w.normal) && (n == 0 || size+len(w.normal[n].data) <= batchMaxBytes) {
		size += len(w.normal[n].data)
		n++
	}
	if n > 0 {
		batch = append(batch, w.normal[:n]...)
		// Kalan dilimi kopyala: alttaki dizi kuyruk icin yeniden kullanilsin.
		rest := copy(w.normal, w.normal[n:])
		for i := rest; i < len(w.normal); i++ {
			w.normal[i] = nil
		}
		w.normal = w.normal[:rest]
		w.queued -= size
	}
	return batch
}

// flush, partiyi yazar. cork varsa mesajlar bellekteki tampona gider ve tek
// bir TCP yazimiyla bosaltilir; yoksa her mesaj kendi zaman asimiyla yazilir.
func (w *batchWriter) flush(batch []*wmsg) error {
	if w.cork == nil {
		for _, m := range batch {
			ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
			err := w.conn.Write(ctx, m.typ, m.data)
			cancel()
			if err != nil {
				return err
			}
		}
		return nil
	}
	w.cork.cork()
	for _, m := range batch {
		// Tikali (cork) iken yazim yalniz bellege gider, bloklamaz; zaman asimi
		// bosaltmada uygulanir. Done'siz baglam coder/websocket'te zamanlayici
		// goroutine'i kurmaz.
		if err := w.conn.Write(context.Background(), m.typ, m.data); err != nil {
			w.cork.uncorkDiscard()
			return err
		}
	}
	return w.cork.uncork(corkFlushTimeout)
}

// corkConn, sunucu baglantisinin en alttaki (TLS altindaki) TCP baglantisi.
// Tikaliyken (cork) yazimlari bellekte biriktirir; uncork hepsini tek write
// ile gonderir. Tikali degilken yazim dogrudan gecer (coder/websocket'in kendi
// ping/pong/close cerceveleri). Tum yazimlar mu altinda oldugu icin TLS
// kayitlarinin sirasi korunur (sira bozulursa TLS MAC hatasiyla baglanti duser).
type corkConn struct {
	net.Conn
	mu     sync.Mutex
	corked bool
	buf    []byte
}

func (c *corkConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.corked {
		c.buf = append(c.buf, p...)
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *corkConn) cork() {
	c.mu.Lock()
	c.corked = true
	c.mu.Unlock()
}

func (c *corkConn) uncork(d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.corked = false
	if len(c.buf) == 0 {
		return nil
	}
	_ = c.Conn.SetWriteDeadline(time.Now().Add(d))
	_, err := c.Conn.Write(c.buf)
	_ = c.Conn.SetWriteDeadline(time.Time{})
	c.resetBufLocked()
	return err
}

// uncorkDiscard, yarim kalan partiyi atar (baglanti zaten kapatilacak).
func (c *corkConn) uncorkDiscard() {
	c.mu.Lock()
	c.corked = false
	c.resetBufLocked()
	c.mu.Unlock()
}

func (c *corkConn) resetBufLocked() {
	if cap(c.buf) > 1<<20 {
		c.buf = nil // tek seferlik buyuk parti bellegi tutmasin
	} else {
		c.buf = c.buf[:0]
	}
}

// corkSlot, Dial sirasinda olusan corkConn'u yakalar (baglanti basina yeni
// tasiyici kullanildigi icin tek baglanti duser).
type corkSlot struct {
	mu sync.Mutex
	c  *corkConn
}

func (s *corkSlot) get() *corkConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c
}

// dialHTTPClient, WSS el sikismasi icin baglanti basina http istemcisi:
// temel tasiyicinin (CA havuzlu ya da varsayilan) kopyasi, TCP dial'i
// corkConn ile sarilir. Tasiyici kendi dial'ini kullanmiyorsa (DialTLSContext)
// slot bos kalir ve yazim birlestirmesiz (eski sekilde) calisir.
func (a *Agent) dialHTTPClient(slot *corkSlot) *http.Client {
	var base *http.Transport
	hc := &http.Client{}
	if a.httpClient != nil {
		*hc = *a.httpClient
		if t, ok := a.httpClient.Transport.(*http.Transport); ok {
			base = t
		} else if a.httpClient.Transport != nil {
			return hc // bilinmeyen tasiyici: dokunma
		}
	}
	if base == nil {
		dt, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return hc
		}
		base = dt
	}
	tr := base.Clone()
	if tr.DialTLSContext != nil {
		hc.Transport = tr
		return hc
	}
	dial := tr.DialContext
	if dial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		dial = d.DialContext
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		cc := &corkConn{Conn: c}
		slot.mu.Lock()
		slot.c = cc
		slot.mu.Unlock()
		return cc, nil
	}
	hc.Transport = tr
	return hc
}
