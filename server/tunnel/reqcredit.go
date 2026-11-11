package tunnel

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Istek govdesi kredisi (ziyaretci -> sunucu -> ajan -> yerel servis yonu).
// Tel bicimi ve geriye uyum: shared/protocol/streamflow.go (ReqCapBodyCredit).
//
// NEDEN: bu yonde akis kontrolu yoktu. streamRequestBody govdeyi ziyaretcinin
// hiziyla ajana akitiyordu; yerel servis yavas tuketince ajandaki 4 MB'lik
// istek govdesi kuyrugu tasiyor ve yukleme kesiliyordu (loopback'te 30 MB h2
// yukleme 10/10 basarisiz). Kredi ile sunucu, ajanin verdigi pencere kadar
// govde gonderir; pencere bitince ziyaretciden OKUMAYI birakir ve geri basinc
// dogal yoldan (TCP/H2 akis penceresi) ziyaretciye yansir. Bekleme yalniz o
// istegin ingress goroutine'indedir; oturumun okuma dongusu hic bloklanmaz
// (kabul/kredi orada yalniz sayaca eklenir).

// Oturum duzeyinde ajan yetenegi: ilk kabulle "var", kabulsuz yanit basligi
// ya da kabul beklemesinin zaman asimiyla "yok" ogrenilir. "Yok" iken de
// oneri surer (kabul gelirse "var"a doner) ama gonderim beklemez.
const (
	reqCreditUnknown int32 = iota
	reqCreditYes
	reqCreditNo
)

// reqCreditAckWait, ajanin yetenegi henuz bilinmiyorken pencere bittiginde
// kabulun beklenecegi en uzun sure. Yeni ajan kabulu istegi alir almaz (bir
// RTT) yollar; eski ajan hic yollamaz. Bu bekleme oturumda en fazla bir kez
// (eski ajanin ilk buyuk yuklemesinde) yasanir; cogu oturumda daha once
// gelen kucuk bir govdeli istegin kabulsuz yaniti eski ajani zaten belli eder.
var reqCreditAckWait = 3 * time.Second

type reqBodyCredit struct {
	mu    sync.Mutex
	avail int
	acked bool          // ajan bu istek icin kabul/kredi yolladi
	off   bool          // kredi uygulanmiyor (eski ajan ya da istek bitti)
	wake  chan struct{} // kredi/kapanis gelince kapatilip yenilenir
}

func newReqBodyCredit(off bool) *reqBodyCredit {
	return &reqBodyCredit{avail: protocol.InitialWindowBytes, off: off, wake: make(chan struct{})}
}

// add, ajandan gelen kabulu (Increment 0) ya da kredi iadesini isler.
// Okuma dongusunden cagrilir; BLOKLAMAZ.
func (c *reqBodyCredit) add(n int) {
	c.mu.Lock()
	c.acked = true
	if n > 0 {
		c.avail += n
		if c.avail > 1<<30 { // kotu niyetli ajanin tasirmasina karsi
			c.avail = 1 << 30
		}
	}
	ch := c.wake
	c.wake = make(chan struct{})
	c.mu.Unlock()
	close(ch)
}

func (c *reqBodyCredit) isAcked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acked
}

// disable, krediyi kapatir: bekleyen gonderen hemen devam eder.
func (c *reqBodyCredit) disable() {
	c.mu.Lock()
	if c.off {
		c.mu.Unlock()
		return
	}
	c.off = true
	ch := c.wake
	c.wake = make(chan struct{})
	c.mu.Unlock()
	close(ch)
}

// acquire, n birim kredi ayirir; yoksa gelene, istek bitene ya da ctx iptal
// olana kadar bekler. Ajanin yetenegi bilinmiyor ve bu istek icin kabul
// gelmemisse en fazla reqCreditAckWait bekler, sonra eski davranisa duser.
func (c *reqBodyCredit) acquire(ctx context.Context, n int, mode *atomic.Int32) error {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		c.mu.Lock()
		if c.off {
			c.mu.Unlock()
			return nil
		}
		if c.avail >= n {
			c.avail -= n
			c.mu.Unlock()
			return nil
		}
		ch, acked := c.wake, c.acked
		c.mu.Unlock()

		var tc <-chan time.Time
		if !acked && mode.Load() != reqCreditYes {
			if timer == nil {
				timer = time.NewTimer(reqCreditAckWait)
			}
			tc = timer.C
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		case <-tc:
			// Kabul gelmedi: eski ajan. Bu istek ve (bilinmiyorsa) oturum
			// kredisiz devam eder; gec gelen kabul oturumu yeniden "var" yapar.
			mode.CompareAndSwap(reqCreditUnknown, reqCreditNo)
			c.disable()
		}
	}
}

// offerReqCredit, govdeli istek icin kredi onerir (yalniz yanit yonunde akis
// kontrolunu pazarlamis ajanlara: daha eskileri istek yonunu hic bilmez).
func (s *Session) offerReqCredit(req *protocol.HTTPRequest) *reqBodyCredit {
	if !req.HasBody || !s.flowControl {
		return nil
	}
	req.Caps = append(req.Caps, protocol.ReqCapBodyCredit)
	return newReqBodyCredit(s.reqCreditMode.Load() == reqCreditNo)
}

// routeReqCredit, ajandan gelen window_update bir HTTP isteginin govde
// kredisiyse isler ve true doner (degilse ham/WS akisinindir).
func (s *Session) routeReqCredit(reqID uint64, n int) bool {
	e, ok := s.lookupExchange(reqID)
	if !ok || e.reqCredit == nil {
		return false
	}
	e.reqCredit.add(n)
	s.reqCreditMode.Store(reqCreditYes)
	return true
}

// noteReqHead, yanit basligi (ya da hata) geldiginde cagrilir: kabul bu
// istegin basligindan ONCE ulasir (ajan yazimlari sirali), gelmediyse ajan
// eskidir. Kredi kapatilir ki gonderen beklemesin.
func (s *Session) noteReqHead(e *Exchange) {
	if e.reqCredit == nil || e.reqCredit.isAcked() {
		return
	}
	s.reqCreditMode.CompareAndSwap(reqCreditUnknown, reqCreditNo)
	e.reqCredit.disable()
}
