package tunnel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ham TCP/UDP ve WebSocket akislarinin ortak teslim kuyrugu ve gonderim kredisi.
//
// NEDEN: eskiden ajan -> ziyaretci verisi akis basina 1024-2048 OGELIK bir
// kanalda bekliyordu (32 KB'lik cercevelerle WS 32 MB, ham TCP 64-128 MB).
// Okumayan bir ziyaretci, kimlik gerektirmeden, akis basina bu kadar bellek
// tutturabiliyordu (20 WS ziyaretcisi: 15 MB -> 1.06 GB). Ayrica kapanis sinyali
// ayri bir kanal oldugundan tuketici select'i kapanisi kalan veriden ONCE
// secebiliyor ve son baytlar kayboluyordu.
//
// Simdi:
//   - Kuyruk bayt (dilim kapasitesi) ile olculur; eski ajanla akis basina
//     tavan, yeni ajanla kredi penceresi (StreamCapCredit) belleği sinirlar.
//     Oturum basina toplam tavan da vardir (cok akisla sisirmeye karsi).
//   - Teslim TAMPONSUZ bir kanaldan tek bir pompa goroutine'i ile yapilir:
//     pompa bir ogeyi ancak tuketici aldiginda birakir; karsi taraf duzgun
//     kapattiginda Closed() YALNIZCA tum veri teslim edildikten sonra kapanir.
//     Boylece hicbir tuketici (rawproxy, kume aktarimi, ozel ag Pipe) son
//     baytlari kaybetmez.
//   - Iptal (tasma, sunucu tarafli kapama, oturum sonu) Aborted() ile ayrica
//     bildirilir; tuketici bloklu bir ziyaretci yazisini baglantiyi kapatarak
//     cozebilir.

const (
	// rawLegacyMaxMem, kredi desteklemeyen (eski) ajanla ham TCP akisinin
	// sunucuda bekletebilecegi veri tavani. Asilinca akis kapatilir (eski
	// "kuyruk dolarsa kapat" davranisi, artik bayt cinsinden).
	rawLegacyMaxMem = 8 << 20
	// udpMaxMem / udpMaxItems: UDP akisi (flow) basina datagram kuyrugu.
	udpMaxMem   = 8 << 20
	udpMaxItems = 2048
	// wsLegacyMaxMem, kredi desteklemeyen ajanla WS akisi basina tavan.
	wsLegacyMaxMem = 2 << 20
	// sessionStreamMemMax, bir ajan oturumundaki TUM ham/WS akislarinin
	// sunucuda bekleyen toplam verisi. Kredi akis basina 2 MB ile sinirlar ama
	// akis sayisiyla carpilir; bu tavan oturum basina toplami keser.
	sessionStreamMemMax = 128 << 20
	// queueItemOverhead, oge basina bellek hesabina eklenen sabit (dilim basligi vb.).
	queueItemOverhead = 64
)

var (
	errStreamOverflow    = errors.New("akis kuyrugu doldu (ziyaretci veriyi yeterince hizli okumuyor)")
	errStreamCreditAbuse = errors.New("ajan akis kredi penceresini asti")
	errSessionStreamMem  = errors.New("oturumun akis bellek tavani asildi")
)

// ErrStreamClosed, kapanmis akisa kredi beklenirken doner.
var ErrStreamClosed = errors.New("akis kapandi")

type qItem struct {
	b   []byte
	eof bool // karsi taraf yazmayi bitirdi (yari kapama isareti)
}

// streamQueue, ajan -> ziyaretci yonunde tek bir akisin teslim kuyrugu.
// push/pushEOF/remoteClose YALNIZCA oturum okuma dongusunden cagrilir ve
// ASLA bloklamaz.
type streamQueue struct {
	sessMem  *atomic.Int64 // oturum toplami (nil = yok sayilir)
	maxMem   int64         // eski ajan: akis basina tavan (0 = yok)
	maxItems int           // 0 = yok (udp)
	credit   atomic.Bool   // kredi pazarlandi: tavan yerine pencere denetimi

	// grant, pompadan: tuketilen veri icin karsi tarafa kredi iadesi.
	grant    func(n int)
	grantAcc int          // yalniz pompa goroutine'i
	unacked  atomic.Int64 // kredi modu: alinip henuz iade edilmemis kredi

	mu         sync.Mutex
	items      []qItem
	mem        int64
	remoteDone bool

	wake      chan struct{} // tamponlu(1)
	out       chan []byte   // TAMPONSUZ: teslim = tuketici aldi
	closed    chan struct{}
	aborted   chan struct{}
	eof       chan struct{}
	closeOnce sync.Once
	abortOnce sync.Once
	eofOnce   sync.Once
}

func newStreamQueue(sessMem *atomic.Int64, maxMem int64, maxItems int) *streamQueue {
	q := &streamQueue{
		sessMem: sessMem, maxMem: maxMem, maxItems: maxItems,
		wake:    make(chan struct{}, 1),
		out:     make(chan []byte),
		closed:  make(chan struct{}),
		aborted: make(chan struct{}),
		eof:     make(chan struct{}),
	}
	go q.run()
	return q
}

func itemMem(b []byte) int64 { return int64(cap(b)) + queueItemOverhead }

func creditCost(n int) int { return n + protocol.StreamFrameCost }

func (q *streamQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// push, b'nin sahipligini devralir (kopyalamaz: coder/websocket her mesaj icin
// taze tampon ayirir). Bellek hesabi cap(b) ile yapilir ki io.ReadAll'un
// buyuttugu tamponun bos kismi da sayilsin. Hata = akis iptal edilmeli.
func (q *streamQueue) push(b []byte) error {
	m := itemMem(b)
	q.mu.Lock()
	defer q.mu.Unlock()
	select {
	case <-q.closed:
		return nil // kapanmis akis: veri atilir
	default:
	}
	if q.credit.Load() {
		// Dogru davranan ajan pencereyi asamaz; asan ajan bellegi sisirmesin.
		if q.unacked.Add(int64(creditCost(len(b)))) > protocol.InitialWindowBytes {
			return errStreamCreditAbuse
		}
	} else if q.maxMem > 0 && q.mem+m > q.maxMem {
		return errStreamOverflow
	}
	if q.maxItems > 0 && len(q.items) >= q.maxItems {
		return errStreamOverflow
	}
	if q.sessMem != nil {
		if q.sessMem.Add(m) > sessionStreamMemMax {
			q.sessMem.Add(-m)
			return errSessionStreamMem
		}
	}
	q.items = append(q.items, qItem{b: b})
	q.mem += m
	q.signal()
	return nil
}

// pushEOF, karsi tarafin yazmayi bitirdigini SIRALI olarak isaretler: onceki
// tum veri teslim edildikten sonra RemoteEOF() kapanir.
func (q *streamQueue) pushEOF() {
	q.mu.Lock()
	q.items = append(q.items, qItem{eof: true})
	q.mu.Unlock()
	q.signal()
}

// remoteClose, karsi taraf akisi duzgun kapatti: kalan veri teslim edilince
// Closed() kapanir (iptal DEGIL).
func (q *streamQueue) remoteClose() {
	q.mu.Lock()
	q.remoteDone = true
	q.mu.Unlock()
	q.signal()
}

// abort, akisi aninda sonlandirir; bekleyen veri atilir.
func (q *streamQueue) abort() {
	q.abortOnce.Do(func() { close(q.aborted) })
	q.closeOnce.Do(func() { close(q.closed) })
	q.mu.Lock()
	if q.sessMem != nil && q.mem > 0 {
		q.sessMem.Add(-q.mem)
	}
	q.mem = 0
	q.items = nil
	q.mu.Unlock()
}

func (q *streamQueue) run() {
	// Panik (ornegin kredi iadesinde): yalniz bu akis iptal edilir; abort kanallari
	// kapatip bellegi oturum sayacindan dusurur, tuketiciler normal kapanis
	// yolunu izler. Kilit next() icinde defer ile birakildigi icin abort kilitlenmez.
	defer obs.Recover(nil, "tunnel.streamq", func(any) { q.abort() })
	for {
		it, ok := q.next()
		if !ok {
			return
		}
		if it.eof {
			q.eofOnce.Do(func() { close(q.eof) })
			continue
		}
		select {
		case q.out <- it.b:
			q.delivered(len(it.b))
		case <-q.closed:
			return
		}
	}
}

// next, siradaki ogeyi bekleyip cikarir; akis bittiyse ok=false. Kilit her
// donuste (panik dahil) birakilir.
func (q *streamQueue) next() (qItem, bool) {
	for {
		it, ok, wait := q.take()
		if !wait {
			return it, ok
		}
		select {
		case <-q.wake:
		case <-q.closed:
			return qItem{}, false
		}
	}
}

// take, kilit altinda tek deneme: oge varsa cikarir; yoksa ve karsi taraf
// kapattiysa akisi kapatir; aksi halde wait=true (uyandirilmayi bekle).
func (q *streamQueue) take() (it qItem, ok, wait bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		if q.remoteDone {
			q.closeOnce.Do(func() { close(q.closed) })
			return qItem{}, false, false
		}
		return qItem{}, false, true
	}
	it = q.items[0]
	q.items[0] = qItem{}
	q.items = q.items[1:]
	if len(q.items) == 0 {
		q.items = nil // bos dizinin bas kismini birak
	}
	if !it.eof {
		m := itemMem(it.b)
		q.mem -= m
		if q.sessMem != nil {
			q.sessMem.Add(-m)
		}
	}
	return it, true, false
}

// delivered, tuketiciye teslim edilen veri icin krediyi biriktirir ve esigi
// asinca iade eder (HTTP govdesiyle ayni esik: pencerenin yarisi).
func (q *streamQueue) delivered(n int) {
	if !q.credit.Load() || q.grant == nil {
		return
	}
	q.grantAcc += creditCost(n)
	if q.grantAcc < windowUpdateThreshold {
		return
	}
	acc := q.grantAcc
	q.grantAcc = 0
	// ONCE dus, SONRA gonder: ajan krediyi alip yeni cerceve yollarsa gecici
	// olarak pencere asilmis gorunmesin.
	q.unacked.Add(-int64(acc))
	q.grant(acc)
}

// creditGate, sunucu -> ajan yonunde (ziyaretci -> yerel hedef) gonderim kredisi.
// Kredi pazarlanmadiysa acquire hemen doner (eski davranis: ajanin kendi
// yazma kuyrugu tavani).
type creditGate struct {
	enabled atomic.Bool
	mu      sync.Mutex
	avail   int
	wake    chan struct{} // kredi gelince kapatilip yenilenir (tum bekleyenler uyanir)
}

func (g *creditGate) enable() {
	g.mu.Lock()
	g.avail = protocol.InitialWindowBytes
	if g.wake == nil {
		g.wake = make(chan struct{})
	}
	g.mu.Unlock()
	g.enabled.Store(true)
}

func (g *creditGate) acquire(ctx context.Context, closed <-chan struct{}, n int) error {
	if !g.enabled.Load() {
		return nil
	}
	for {
		g.mu.Lock()
		if g.avail >= n {
			g.avail -= n
			g.mu.Unlock()
			return nil
		}
		ch := g.wake
		g.mu.Unlock()
		select {
		case <-ch:
		case <-closed:
			return ErrStreamClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *creditGate) add(n int) {
	if n <= 0 || !g.enabled.Load() {
		return
	}
	g.mu.Lock()
	g.avail += n
	if g.avail > 1<<30 { // kotu niyetli ajanin tasirmasina karsi
		g.avail = 1 << 30
	}
	ch := g.wake
	g.wake = make(chan struct{})
	g.mu.Unlock()
	close(ch)
}

// rateLog, ayni uyarinin log selini onler: en fazla her 10 sn'de bir satir;
// arada bastirilanlarin sayisi bir sonraki satirda verilir.
type rateLog struct {
	last       atomic.Int64
	suppressed atomic.Int64
}

func (r *rateLog) allow() (bool, int64) {
	now := time.Now().UnixNano()
	last := r.last.Load()
	if now-last < int64(10*time.Second) || !r.last.CompareAndSwap(last, now) {
		r.suppressed.Add(1)
		return false, 0
	}
	return true, r.suppressed.Swap(0)
}

var streamAbortLog rateLog

func (s *Session) logStreamAbort(kind string, reqID uint64, tunnelID string, err error) {
	ok, sup := streamAbortLog.allow()
	if !ok || s.log == nil {
		return
	}
	s.log.Warn("akis kuyrugu tasti, akis kapatiliyor", "tur", kind, "req_id", reqID, "tunnel", tunnelID, "hata", err, "bastirilan", sup)
}
