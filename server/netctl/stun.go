package netctl

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/stun"
)

// StunServer, RFC 5389 Binding dinleyicisidir (UDP). Kimlik gerektirmez; yalniz
// istegin geldigi kaynak adresi (NAT yansimasi) doner. Savunma:
//   - yalniz gecerli, nitelikssiz Binding istekleri yanitlanir (digerleri sessizce
//     atilir; hata yaniti YOK -> yansima/sizdirma yuzeyi yok),
//   - yanit en fazla ~2.2x istek boyutu (IPv4: 32 bayt / 20 bayt),
//   - kaynak IP basina jeton kovasi + genel ust sinir (hiz siniri).
type StunServer struct {
	// PerIPRate: saniyede kaynak IP basina istek; Burst: kova buyuklugu.
	PerIPRate float64
	Burst     float64
	// GlobalRate: saniyede toplam yanit ust siniri (0 = sinirsiz).
	GlobalRate float64
	Log        *slog.Logger
	// Now testler icin saat.
	Now func() time.Time

	mu      sync.Mutex
	buckets map[netip.Addr]*bucket
	global  bucket
	lastGC  time.Time

	// Sayaclar (teshis).
	Served  uint64
	Limited uint64
	Dropped uint64
}

type bucket struct {
	tokens float64
	at     time.Time
}

// NewStunServer, varsayilan sinirlarla sunucu kurar.
func NewStunServer(log *slog.Logger) *StunServer {
	if log == nil {
		log = slog.Default()
	}
	return &StunServer{PerIPRate: 5, Burst: 10, GlobalRate: 5000, Log: log, Now: time.Now, buckets: map[netip.Addr]*bucket{}}
}

func take(b *bucket, now time.Time, rate, burst float64) bool {
	if b.at.IsZero() {
		b.tokens, b.at = burst, now
	}
	b.tokens += now.Sub(b.at).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Handle, tek bir UDP datagramini isler; yanit gerekiyorsa (resp, true) doner.
func (s *StunServer) Handle(pkt []byte, src netip.AddrPort) ([]byte, bool) {
	tx, ok := stun.ParseBindingRequest(pkt)
	if !ok {
		s.mu.Lock()
		s.Dropped++
		s.mu.Unlock()
		return nil, false
	}
	now := s.Now()
	ip := src.Addr().Unmap()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastGC) > time.Minute {
		for k, b := range s.buckets {
			if now.Sub(b.at) > 2*time.Minute {
				delete(s.buckets, k)
			}
		}
		s.lastGC = now
	}
	b := s.buckets[ip]
	if b == nil {
		if len(s.buckets) > 100000 { // bellek sinir: tablo doluysa yeni kaynak reddedilir
			s.Limited++
			return nil, false
		}
		b = &bucket{}
		s.buckets[ip] = b
	}
	if !take(b, now, s.PerIPRate, s.Burst) {
		s.Limited++
		return nil, false
	}
	if s.GlobalRate > 0 && !take(&s.global, now, s.GlobalRate, s.GlobalRate) {
		s.Limited++
		return nil, false
	}
	s.Served++
	return stun.NewBindingResponse(tx, src), true
}

// Serve, pc uzerinde ctx bitene kadar istekleri yanitlar.
func (s *StunServer) Serve(ctx context.Context, pc net.PacketConn) error {
	go func() { <-ctx.Done(); _ = pc.Close() }()
	buf := make([]byte, 512)
	udp, _ := pc.(*net.UDPConn)
	for {
		var n int
		var src netip.AddrPort
		var err error
		if udp != nil {
			n, src, err = udp.ReadFromUDPAddrPort(buf)
		} else {
			var a net.Addr
			n, a, err = pc.ReadFrom(buf)
			if ua, ok := a.(*net.UDPAddr); ok {
				src = ua.AddrPort()
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		if resp, ok := s.Handle(buf[:n], src); ok {
			if udp != nil {
				_, _ = udp.WriteToUDPAddrPort(resp, src)
			} else {
				_, _ = pc.WriteTo(resp, net.UDPAddrFromAddrPort(src))
			}
		}
	}
}

// Listen, addr ("host:port" veya ":3478") uzerinde UDP soketi acar. Hata
// (port dolu/izin yok) cagiranda yalniz-relay moduna dusme olarak ele alinir.
func (s *StunServer) Listen(addr string) (net.PacketConn, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	s.Log.Info("STUN dinleyicisi basladi", "addr", pc.LocalAddr().String())
	return pc, nil
}
