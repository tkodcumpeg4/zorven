package tunnel

// Ham akis koprusu — bir net.Conn ile ajana acilmis bir RawStream'i baglar.
// Ozel ag (FAZ 3 / F17) bunu kullanir: istemcinin WebSocket baglantisi bir
// net.Conn'a sarilir ve ajanin yerel hedefe actigi TCP akisina kopru kurulur.

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// ErrStreamRejected, ajanin yerel hedefe baglanamadigini bildirir.
var ErrStreamRejected = errors.New("ajan yerel hedefe baglanamadi")

// WaitAccept, ajanin akis onayini bekler. Onay bir hata kodu tasiyorsa
// ErrStreamRejected (sarilmis) doner; sure dolarsa context.DeadlineExceeded.
func WaitAccept(ctx context.Context, stream *RawStream, timeout time.Duration) (protocol.StreamAck, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case ack := <-stream.Accept():
		if ack.Code != "" {
			return ack, ErrStreamRejected
		}
		return ack, nil
	case <-stream.Closed():
		return protocol.StreamAck{}, ErrStreamRejected
	case <-t.C:
		return protocol.StreamAck{}, context.DeadlineExceeded
	case <-ctx.Done():
		return protocol.StreamAck{}, ctx.Err()
	}
}

// Pipe, conn ile stream arasinda iki yonlu kopyalama yapar ve taraflardan
// biri kapaninca doner. Donmeden once stream'i kapatir; conn'u kapatmak
// cagirana aittir.
func Pipe(ctx context.Context, conn net.Conn, stream *RawStream) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer stream.Close("closed")

	// conn -> ajan
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				// Send, ayni tamponu yeniden kullanmadan once cerceveyi
				// yazmis olur (SendBinary senkron).
				if serr := stream.Send(ctx, buf[:n]); serr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// ajan -> conn
	for {
		select {
		case data := <-stream.FromLocal():
			if _, werr := conn.Write(data); werr != nil {
				return
			}
		case <-stream.Closed():
			return
		case <-ctx.Done():
			return
		}
	}
}
