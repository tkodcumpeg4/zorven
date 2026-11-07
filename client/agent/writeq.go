package agent

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Yerel hedefe yazim kuyrugu (head-of-line onlemi).
//
// NEDEN: sunucudan gelen govde / WS / ham akis baytlari okuma dongusunde
// DOGRUDAN yerel baglantiya yazilirsa, tek bir yavas yerel servis butun
// tuneli (diger tum istekleri) bloklar. Her istek/akis icin SINIRLI bir kuyruk
// ve tek bir yazici goroutine kullanilir: siralama akis basina korunur, yazim
// zaman asimina sahiptir, tasma veya zaman asiminda YALNIZCA o istek/akis iptal
// edilir.

var (
	errWriteQueueOverflow = errors.New("yazma kuyrugu doldu (yerel servis veriyi yeterince hizli almiyor)")
	errWriteQueueClosed   = errors.New("yazma kuyrugu kapali")
	errWriteStalled       = errors.New("yerel servise yazim zaman asimina ugradi")
)

const (
	// writeQueueMaxBytes, akis basina kuyrukta bekleyebilecek bayt tavani.
	writeQueueMaxBytes = 4 << 20
	// writeQueueMaxItems, kuyruk uzunlugu (parca sayisi).
	writeQueueMaxItems = 1024
	// localWriteTimeout, tek bir yerel yazimin zaman asimi.
	localWriteTimeout = 30 * time.Second
)

type wqItem struct {
	b   []byte
	fin bool // son: fin() cagrilir (yazma yonunu kapat / govde EOF)
}

type writeQueue struct {
	write func(b []byte, timeout time.Duration) error
	fin   func()
	// onFail, tasma/yazim hatasinda BIR kez cagrilir (yazici goroutine'den).
	onFail  func(error)
	timeout time.Duration

	ch       chan wqItem
	queued   atomic.Int64
	maxBytes int64
	done     chan struct{}
	stopOnce sync.Once
	failOnce sync.Once
	finished atomic.Bool
}

func newWriteQueue(write func([]byte, time.Duration) error, fin func(), onFail func(error)) *writeQueue {
	q := &writeQueue{
		write:    write,
		fin:      fin,
		onFail:   onFail,
		timeout:  localWriteTimeout,
		ch:       make(chan wqItem, writeQueueMaxItems),
		maxBytes: writeQueueMaxBytes,
		done:     make(chan struct{}),
	}
	go q.run()
	return q
}

// newConnWriteQueue, net.Conn'e yazan kuyruk; fin TCP'de CloseWrite (yarim kapama).
func newConnWriteQueue(c net.Conn, onFail func(error)) *writeQueue {
	return newWriteQueue(func(b []byte, d time.Duration) error {
		_ = c.SetWriteDeadline(time.Now().Add(d))
		_, err := c.Write(b)
		return err
	}, func() {
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}, onFail)
}

// push, b'yi sirayla yazilmak uzere kuyruga alir; BLOKLAMAZ. Tasarsa kuyruk
// basarisiz sayilir (onFail) ve hata doner.
func (q *writeQueue) push(b []byte) error {
	select {
	case <-q.done:
		return errWriteQueueClosed
	default:
	}
	n := int64(len(b))
	if q.queued.Add(n) > q.maxBytes {
		q.queued.Add(-n)
		q.fail(errWriteQueueOverflow)
		return errWriteQueueOverflow
	}
	select {
	case q.ch <- wqItem{b: b}:
		return nil
	default:
		q.queued.Add(-n)
		q.fail(errWriteQueueOverflow)
		return errWriteQueueOverflow
	}
}

// finish, kuyruktaki her sey yazildiktan sonra fin()'i cagirir (EOF / CloseWrite).
func (q *writeQueue) finish() {
	if !q.finished.CompareAndSwap(false, true) {
		return
	}
	select {
	case q.ch <- wqItem{fin: true}:
	default:
		q.fail(errWriteQueueOverflow)
	}
}

func (q *writeQueue) fail(err error) {
	q.failOnce.Do(func() {
		q.stop()
		if q.onFail != nil {
			go q.onFail(err)
		}
	})
}

// stop, yaziciyi durdurur (bekleyen veri atilir).
func (q *writeQueue) stop() {
	q.stopOnce.Do(func() { close(q.done) })
}

func (q *writeQueue) run() {
	for {
		select {
		case <-q.done:
			return
		case it := <-q.ch:
			if it.fin {
				if q.fin != nil {
					q.fin()
				}
				continue
			}
			err := q.write(it.b, q.timeout)
			q.queued.Add(-int64(len(it.b)))
			if err != nil {
				select {
				case <-q.done: // zaten kapatildi; hata beklenen
				default:
					if isTimeoutErr(err) {
						err = errWriteStalled
					}
					q.fail(err)
				}
				return
			}
		}
	}
}

func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
