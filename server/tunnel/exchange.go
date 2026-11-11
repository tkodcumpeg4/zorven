package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// ErrClientGone, istemci baglantisi koptugunda bekleyen isteklere verilir.
var ErrClientGone = errors.New("istemci baglantisi koptu")

// ErrReaderGone, yanit govdesini okuyan taraf (tarayici) gittiginde doner.
var ErrReaderGone = errors.New("yanit okuyucusu gitti")

// ErrFlowControlViolation, istemci acik penceresinden fazla veri gonderdiginde
// doner. Bu bir PROTOKOL IHLALIDIR: dogru davranan istemci kredisi bitince
// bekler (bkz. protocol.WindowUpdate).
var ErrFlowControlViolation = errors.New("akis kontrolu penceresi asildi")

// ResponseHead, istemciden gelen yanit basligi (veya hata).
type ResponseHead struct {
	Status  int
	Headers map[string][]string
	HasBody bool

	// ErrCode bos degilse istemci yerel servise ulasamadi.
	ErrCode string
	ErrMsg  string
}

// Exchange, tek bir tunellenmis HTTP istegini temsil eder.
//
// GOVDE TASARIMI (akis kontrolu): govde cerceveleri, BAYT siniri akis
// kontrolu PENCERESINE esit bir kuyrukta tutulur. Istemci penceresinden
// fazlasini gondermedigi surece kuyruga ekleme her zaman sigar, dolayisiyla
// oturumun okuma dongusu ASLA bloklanmaz.
//
// Onceden io.Pipe kullaniliyordu; senkron oldugu icin Write, okuyan taraf
// baytlari tuketene kadar bloklardi. O blok paylasilan okuma dongusunde
// gerceklestiginden tek bir yavas tuketici ayni istemcideki TUM istekleri
// durduruyordu (olcum: 7681 rps -> 15.3 rps, p99 7 saniye).
//
// Geri basinc kayboldu mu? Hayir, yer degistirdi: artik istemci tarafinda,
// istek BASINA kredi ile uygulaniyor (protocol.WindowUpdate).
//
// O14: kuyruk eskiden kapasitesi pencereye denk (2048 cerceve, ~48 KB) bir
// kanaldi ve HER istekte, tek cercevelik kucuk yanitlarda bile tam boy
// ayriliyordu (kucuk istekte toplam tahsisin yarisi). Artik kilitli, ihtiyac
// oldukca buyuyen bir dilim: kucuk yanitta tek elemanlik. Anlam ayni: ekleme
// BLOKLAMAZ, bayt siniri (queued) ve cerceve-sayisi tavani (maxFrames) korunur.
//
// Es zamanlilik sozlesmesi:
//   - Kuyruga YALNIZCA oturumun okuma dongusu ekler ve YALNIZCA o kapatir
//     (routeBodyFrame / closeBody / failAllPending hepsi o goroutine'dedir).
//   - Tuketici isini bitirince abandon() ile done'i kapatir.
type Exchange struct {
	reqID uint64

	head chan ResponseHead // tamponlu(1): gonderen asla bloklanmaz

	mu     sync.Mutex
	frames [][]byte // bekleyen govde cerceveleri (FIFO); mu ile korunur
	first  int      // frames icinde siradaki okunacak eleman
	closed bool     // uretici govdeyi kapatti; mu ile korunur
	// closeErr yalnizca closeOnce icinde, mu altinda yazilir.
	closeErr error
	// notify, yeni cerceve ya da kapanis oldugunu okuyucuya bildirir.
	// Tamponlu(1): gonderen bloklamaz; kacan sinyal yok, cunku okuyucu
	// beklemeden once kuyrugu kilit altinda yeniden kontrol eder.
	notify chan struct{}

	done chan struct{} // okuyucu tarafi bitti/vazgecti

	// ctx, BindContext ile baglanan okuyucu baglami (ziyaretci istegi). Iptal
	// olunca Read hemen doner: ajan hic EOF/hata yollamasa bile (eski ajan,
	// yarida kalan hedef) ziyaretci gidince okuyan goroutine serbest kalir.
	ctx context.Context

	headOnce  sync.Once
	closeOnce sync.Once
	abortOnce sync.Once

	// cur, yalnizca okuyan goroutine tarafindan kullanilir.
	cur []byte

	// onDrain, tuketici n bayt okudugunda cagrilir. Session bunu window_update
	// gondermek (kredi iade etmek) icin kullanir. nil olabilir.
	onDrain func(n int)

	// queued, kuyrukta + cur'da BEKLEYEN bayt sayisi. Akis kontrolu penceresi
	// BAYT tabanli oldugundan tampon da bayt tabanli sinirlanir; aksi halde
	// kucuk cerceveler (chunked/SSE) cerceve-sayisi tavanini pencereden cok
	// once doldurup buyuk yanitlari keserdi (FAZ 8 bug'i).
	queued atomic.Int64

	// trailers: ajanin yolladigi yanit trailer'lari (http_trailers; gRPC).
	trailerMu sync.Mutex
	trailers  http.Header

	// reqCredit, istek govdesi kredisi (body_credit onerildiyse; aksi halde nil).
	// Exchange tabloya girmeden ONCE yazilir, sonra yalniz okunur.
	reqCredit *reqBodyCredit
}

// exchangeBufferMinFrame, cerceve-sayisi tavanini hesaplarken varsayilan
// asgari cerceve boyu. Asil sinir queued BAYT kontroludur; bu yalnizca asiri
// kucuk cercevelerle kuyrugun (dilim basliklari) sisirilmesini onler.
const exchangeBufferMinFrame = 1024

// maxFrames, bir istegin kuyrugunda bekleyebilecek en fazla cerceve sayisi
// (eski kanal kapasitesiyle ayni: pencere / 1 KB, en az 32).
var maxFrames = max(protocol.InitialWindowBytes/exchangeBufferMinFrame, 32)

func newExchange(reqID uint64) *Exchange {
	return &Exchange{
		reqID:  reqID,
		head:   make(chan ResponseHead, 1),
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// ReqID, bu istegin oturum icindeki kimligi.
func (e *Exchange) ReqID() uint64 { return e.reqID }

// BindContext, govde okumasini ctx'e baglar: ctx iptal olunca (ziyaretci
// gitti, istek zaman asimi) Read ctx.Err() ile doner. Ilk Read'den ONCE ve
// okuyan goroutine'den cagrilmalidir.
func (e *Exchange) BindContext(ctx context.Context) { e.ctx = ctx }

// Head, yanit basliginin bekleneceği kanal. Tam olarak bir deger alir.
func (e *Exchange) Head() <-chan ResponseHead { return e.head }

// Body, yanit govdesi akisi.
func (e *Exchange) Body() io.Reader { return e }

// Read, govde cercevelerini sirayla akitir ve okunan baytlari onDrain ile
// bildirir (kredi iadesi).
//
// Uretici her durumda kuyrugu kapatir (EOF cercevesi, hata mesaji ya da
// oturum kapanisinda failAllPending). Yine de ajan govdeyi yarida birakip HIC
// bir sey yollamazsa (eski ajan, Y4) okuma baglanan baglama (BindContext) ve
// abandon()'a da bakar; boylece goroutine oturum sonuna kadar asili kalmaz.
func (e *Exchange) Read(p []byte) (int, error) {
	for len(e.cur) == 0 {
		e.mu.Lock()
		if e.first < len(e.frames) {
			e.cur = e.frames[e.first]
			e.frames[e.first] = nil // tuketilen cerceve GC'ye birakilsin
			e.first++
			if e.first == len(e.frames) {
				e.frames, e.first = e.frames[:0], 0
			}
			e.mu.Unlock()
			continue
		}
		if e.closed {
			err := e.closeErr
			e.mu.Unlock()
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		e.mu.Unlock()
		var ctxDone <-chan struct{}
		if e.ctx != nil {
			ctxDone = e.ctx.Done()
		}
		select {
		case <-e.notify:
		case <-ctxDone:
			return 0, e.ctx.Err()
		case <-e.done:
			return 0, ErrReaderGone
		}
	}
	n := copy(p, e.cur)
	e.cur = e.cur[n:]
	e.queued.Add(-int64(n)) // tampondan cikan bayt
	if e.onDrain != nil {
		e.onDrain(n)
	}
	return n, nil
}

// signal, okuyucuyu uyandirir; BLOKLAMAZ (bekleyen bir sinyal yeterlidir).
func (e *Exchange) signal() {
	select {
	case e.notify <- struct{}{}:
	default:
	}
}

// setTrailers, ajanin yolladigi yanit trailer'larini saklar (oturum okuma
// dongusu cagirir; EOF cercevesinden ONCE gelir).
func (e *Exchange) setTrailers(t map[string][]string) {
	if len(t) == 0 {
		return
	}
	h := make(http.Header, len(t))
	for k, v := range t {
		h[k] = append([]string(nil), v...)
	}
	e.trailerMu.Lock()
	e.trailers = h
	e.trailerMu.Unlock()
}

// Trailers, yanit trailer'larini doner. Govde okuma EOF'a ulastiktan SONRA
// cagrilmalidir; trailer yoksa nil.
func (e *Exchange) Trailers() http.Header {
	e.trailerMu.Lock()
	defer e.trailerMu.Unlock()
	return e.trailers
}

// deliverHead, yanit basligini bekleyen tarafa iletir.
// Birden fazla cagri guvenlidir; yalnizca ilki gecerlidir (hatali bir istemci
// ayni req_id icin iki yanit gonderirse ikincisi yok sayilir).
func (e *Exchange) deliverHead(h ResponseHead) {
	e.headOnce.Do(func() { e.head <- h })
}

// writeBody, bir govde cercevesini kuyruga koyar. ASLA BLOKLAMAZ.
//
// p'nin sahipligi devralinir (kopyalanmaz): coder/websocket'in Conn.Read'i her
// mesaj icin io.ReadAll ile TAZE tampon ayirir, dolayisiyla dilim baskasi
// tarafindan yeniden kullanilmaz.
func (e *Exchange) writeBody(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	// BAYT tabanli sinir: bekleyen + yeni, akis-kontrolu penceresini asamaz.
	// Dogru davranan istemci penceresini asmaz; asarsa protokol ihlalidir.
	if int(e.queued.Load())+len(p) > protocol.InitialWindowBytes {
		return ErrFlowControlViolation
	}
	select {
	case <-e.done:
		return ErrReaderGone
	default:
	}
	e.mu.Lock()
	if e.closed {
		// Kapanistan sonra gelen cerceve (hatali ajan): atilir.
		e.mu.Unlock()
		return ErrReaderGone
	}
	if len(e.frames)-e.first >= maxFrames {
		// Cerceve-sayisi tavani (asiri kucuk cerceveler). Bayt siniri
		// asilmadigi surece nadir; yine de ihlal say.
		e.mu.Unlock()
		return ErrFlowControlViolation
	}
	e.frames = append(e.frames, p)
	e.queued.Add(int64(len(p)))
	e.mu.Unlock()
	e.signal()
	return nil
}

// closeBody, govde akisini sonlandirir. err nil ise normal EOF.
// YALNIZCA uretici (oturum okuma dongusu) cagirmalidir.
func (e *Exchange) closeBody(err error) {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closeErr = err
		e.closed = true
		e.mu.Unlock()
		e.signal()
	})
	// Yanit bitti / istek hata ile sonlandi: ajan bu istegin govdesini artik
	// almaz (kalan cerceveleri atar), kredi de iade etmez. Gonderen beklemesin;
	// kalan govde eski davranisla (kredisiz) akar ve ajanda atilir.
	if e.reqCredit != nil {
		e.reqCredit.disable()
	}
}

// abandon, okuyan tarafin isinin bittigini bildirir. Kuyrugu KAPATMAZ —
// kapatma hakki ureticidedir (bkz. es zamanlilik sozlesmesi).
func (e *Exchange) abandon() {
	e.abortOnce.Do(func() { close(e.done) })
	if e.reqCredit != nil {
		e.reqCredit.disable()
	}
}

// fail, hem basligi hem govdeyi hata ile kapatir.
// Bekleyen istek asla asili kalmaz — sizinti onlemi budur.
func (e *Exchange) fail(code, msg string, err error) {
	e.deliverHead(ResponseHead{ErrCode: code, ErrMsg: msg})
	e.closeBody(err)
}

// ErrTooManyRequests, oturum basina eszamanli istek siniri asildiginda doner.
var ErrTooManyRequests = errors.New("oturum eszamanli istek siniri asildi (too many concurrent requests)")

// --- Session tarafi kayit defteri -----------------------------------------

func (s *Session) newExchange() (*Exchange, error) { return s.newExchangeCredit(nil) }

// newExchangeCredit, istek govdesi kredisi (nil olabilir) ile yeni exchange.
// Kredi tabloya girmeden once baglanir: ajanin kabulu (okuma dongusu) alani
// yalniz okur.
func (s *Session) newExchangeCredit(rc *reqBodyCredit) (*Exchange, error) {
	// O14: nesne ve kapanis pendingMu DISINDA ayrilir; ayni kilidi okuma
	// dongusu (lookupExchange) her cercevede aldigi icin kilit altinda tahsis
	// oturumu yavaslatiyordu. Sinir asiminda atlanan kimlik zararsizdir
	// (kimlikler yalniz tekil olmali, ardisik olmasi gerekmez).
	id := s.nextReqID.Add(1)
	e := newExchange(id)
	e.reqCredit = rc
	// Tuketici govdeyi okudukca istemciye kredi iade et.
	e.onDrain = func(n int) { s.sendWindowUpdate(id, n) }

	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if len(s.pending) >= MaxPendingExchanges {
		return nil, ErrTooManyRequests
	}
	s.pending[id] = e
	return e, nil
}

func (s *Session) lookupExchange(id uint64) (*Exchange, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	e, ok := s.pending[id]
	return e, ok
}

// FinishExchange, istegi kayittan dusurur. Istek bitince MUTLAKA cagrilmali,
// aksi halde pending tablosu ve kredi kaydi sizar.
//
// Govde kuyrugunu KAPATMAZ: kapatma hakki ureticiye (oturum okuma dongusu)
// aittir. Bunun yerine abandon() ile okuyucunun gittigi bildirilir.
func (s *Session) FinishExchange(id uint64) {
	s.pendingMu.Lock()
	e, ok := s.pending[id]
	delete(s.pending, id)
	s.pendingMu.Unlock()

	s.creditMu.Lock()
	delete(s.credit, id)
	s.creditMu.Unlock()

	if ok {
		e.abandon()
	}
}

// failAllPending, oturum kapanirken TUM bekleyen istekleri hata ile sonlandirir.
//
// Bu cagri olmadan, istemci koptugunda o an ucusta olan her istek ingress
// tarafinda sonsuza kadar yanit bekler; goroutine sizar, tarayici da hicbir
// zaman yanit almaz. api_contract.md §3: 502 client_offline.
func (s *Session) failAllPending() {
	s.pendingMu.Lock()
	pending := s.pending
	s.pending = make(map[uint64]*Exchange)
	s.pendingMu.Unlock()

	for _, e := range pending {
		e.fail(protocol.CodeClientOffline, "istemci baglantisi koptu", ErrClientGone)
	}
	if n := len(pending); n > 0 {
		s.log.Warn("bekleyen istekler istemci koptugu icin sonlandirildi", "adet", n)
	}
}

// PendingCount, o an ucusta olan istek sayisi (metrik/teshis icin).
func (s *Session) PendingCount() int {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return len(s.pending)
}

// SendCancel, tarayici vazgectiginde istemciye istegi birakmasini bildirir.
// Hata yutulur: iptal en iyi cabadir, baglanti zaten kopmus olabilir.
func (s *Session) SendCancel(ctx context.Context, reqID uint64, reason string) {
	_ = s.Send(ctx, protocol.Cancel{
		Type:   protocol.TypeCancel,
		ReqID:  reqID,
		Reason: reason,
	}, protocol.TypeCancel)
}
