package tunnel

import (
	"context"
	"errors"
	"io"
	"sync"

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
// GOVDE TASARIMI (akis kontrolu): govde cerceveleri, kapasitesi akis kontrolu
// PENCERESINE denk gelen tamponlu bir kanalda tutulur. Istemci penceresinden
// fazlasini gondermedigi surece gonderim her zaman sigar, dolayisiyla oturumun
// okuma dongusu ASLA bloklanmaz.
//
// Onceden io.Pipe kullaniliyordu; senkron oldugu icin Write, okuyan taraf
// baytlari tuketene kadar bloklardi. O blok paylasilan okuma dongusunde
// gerceklestiginden tek bir yavas tuketici ayni istemcideki TUM istekleri
// durduruyordu (olcum: 7681 rps -> 15.3 rps, p99 7 saniye).
//
// Geri basinc kayboldu mu? Hayir, yer degistirdi: artik istemci tarafinda,
// istek BASINA kredi ile uygulaniyor (protocol.WindowUpdate).
//
// Es zamanlilik sozlesmesi:
//   - chunks'a YALNIZCA oturumun okuma dongusu gonderir ve YALNIZCA o kapatir
//     (routeBodyFrame / closeBody / failAllPending hepsi o goroutine'dedir).
//     Tuketici asla kapatmaz; kapatsaydi ucusta bir gonderim "send on closed
//     channel" ile panikleyebilirdi.
//   - Tuketici isini bitirince abandon() ile done'i kapatir.
type Exchange struct {
	reqID uint64

	head chan ResponseHead // tamponlu(1): gonderen asla bloklanmaz

	chunks chan []byte   // govde cerceveleri; kapasite = pencere
	done   chan struct{} // okuyucu tarafi bitti/vazgecti

	headOnce  sync.Once
	closeOnce sync.Once
	abortOnce sync.Once

	// closeErr yalnizca closeOnce icinde yazilir; close(chunks) ile okuyan
	// tarafa gorunur olur (happens-before).
	closeErr error

	// cur, yalnizca okuyan goroutine tarafindan kullanilir.
	cur []byte

	// onDrain, tuketici n bayt okudugunda cagrilir. Session bunu window_update
	// gondermek (kredi iade etmek) icin kullanir. nil olabilir.
	onDrain func(n int)
}

func newExchange(reqID uint64) *Exchange {
	// Pencereye kac cerceve sigar: en kotu durumda her cerceve tam boy.
	capFrames := protocol.InitialWindowBytes / protocol.BodyChunkSize
	if capFrames < 1 {
		capFrames = 1
	}
	return &Exchange{
		reqID:  reqID,
		head:   make(chan ResponseHead, 1),
		chunks: make(chan []byte, capFrames),
		done:   make(chan struct{}),
	}
}

// ReqID, bu istegin oturum icindeki kimligi.
func (e *Exchange) ReqID() uint64 { return e.reqID }

// Head, yanit basliginin bekleneceği kanal. Tam olarak bir deger alir.
func (e *Exchange) Head() <-chan ResponseHead { return e.head }

// Body, yanit govdesi akisi.
func (e *Exchange) Body() io.Reader { return e }

// Read, govde cercevelerini sirayla akitir ve okunan baytlari onDrain ile
// bildirir (kredi iadesi).
//
// done'a BAKMAZ: uretici her durumda kanali kapatir (EOF cercevesi, hata
// mesaji veya oturum kapanisinda failAllPending), dolayisiyla okuma sonsuza
// kadar asili kalamaz — onceki io.Pipe davranisiyla ayni.
func (e *Exchange) Read(p []byte) (int, error) {
	for len(e.cur) == 0 {
		b, ok := <-e.chunks
		if !ok {
			if e.closeErr != nil {
				return 0, e.closeErr
			}
			return 0, io.EOF
		}
		e.cur = b
	}
	n := copy(p, e.cur)
	e.cur = e.cur[n:]
	if e.onDrain != nil {
		e.onDrain(n)
	}
	return n, nil
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
	select {
	case e.chunks <- p:
		return nil
	case <-e.done:
		return ErrReaderGone
	default:
		// Kuyruk dolu => istemci penceresinden fazlasini gonderdi.
		return ErrFlowControlViolation
	}
}

// closeBody, govde akisini sonlandirir. err nil ise normal EOF.
// YALNIZCA uretici (oturum okuma dongusu) cagirmalidir.
func (e *Exchange) closeBody(err error) {
	e.closeOnce.Do(func() {
		e.closeErr = err
		close(e.chunks)
	})
}

// abandon, okuyan tarafin isinin bittigini bildirir. Kanali KAPATMAZ —
// kapatma hakki ureticidedir (bkz. es zamanlilik sozlesmesi).
func (e *Exchange) abandon() {
	e.abortOnce.Do(func() { close(e.done) })
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

func (s *Session) newExchange() (*Exchange, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	if len(s.pending) >= MaxPendingExchanges {
		return nil, ErrTooManyRequests
	}

	id := s.nextReqID.Add(1)
	e := newExchange(id)
	// Tuketici govdeyi okudukca istemciye kredi iade et.
	e.onDrain = func(n int) { s.sendWindowUpdate(id, n) }

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
// Govde kanalini KAPATMAZ: kapatma hakki ureticiye (oturum okuma dongusu)
// aittir; buradan kapatmak ucusta bir gonderimle yarisip panige yol acardi.
// Bunun yerine abandon() ile okuyucunun gittigi bildirilir.
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
// tarafinda sonsuza kadar yanit bekler; goroutine ve io.Pipe sizar, tarayici da
// hicbir zaman yanit almaz. api_contract.md §3: 502 client_offline.
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
