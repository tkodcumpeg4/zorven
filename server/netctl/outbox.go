package netctl

import (
	"context"
	"sync"
	"sync/atomic"
)

// Outbox, bir dugume giden sinirli kuyruk. Kontrol mesajlari (netmap) kuyrugu
// dolarsa baglanti kapatilir (istemci yeniden baglanip tam netmap alir); veri
// (FrameWG) kuyrugu dolarsa paket DUSURULUR — TCP-over-TCP biriktirmesini onler.
type Outbox struct {
	ctrl    chan []byte
	data    chan []byte
	done    chan struct{}
	once    sync.Once
	Dropped atomic.Int64
}

func NewOutbox() *Outbox {
	return &Outbox{ctrl: make(chan []byte, 64), data: make(chan []byte, 256), done: make(chan struct{})}
}

// Ctrl: JSON metin mesaji kuyruga alir. Dolu ise outbox kapanir.
func (o *Outbox) Ctrl(b []byte) bool {
	select {
	case <-o.done:
		return false
	default:
	}
	select {
	case o.ctrl <- b:
		return true
	default:
		o.Close()
		return false
	}
}

// Data: ikili cerceve kuyruga alir; dolu ise dusurur.
func (o *Outbox) Data(b []byte) bool {
	select {
	case <-o.done:
		return false
	default:
	}
	select {
	case o.data <- b:
		return true
	default:
		o.Dropped.Add(1)
		return false
	}
}

func (o *Outbox) Close()                { o.once.Do(func() { close(o.done) }) }
func (o *Outbox) Done() <-chan struct{} { return o.done }

// Pump, kuyrugu write ile bosaltir (kontrol onceliklidir). ctx bitince veya
// outbox kapaninca doner.
func (o *Outbox) Pump(ctx context.Context, write func(ctx context.Context, binary bool, b []byte) error) error {
	for {
		select {
		case b := <-o.ctrl:
			if err := write(ctx, false, b); err != nil {
				return err
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-o.done:
			return nil
		case b := <-o.ctrl:
			if err := write(ctx, false, b); err != nil {
				return err
			}
		case b := <-o.data:
			if err := write(ctx, true, b); err != nil {
				return err
			}
		}
	}
}
