package agent

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// Yerel servise (origin) HTTP surumu secimi (Akis G1, gRPC).
//
// Politika (--origin-protocol / Agent.OriginProtocol):
//
//	auto  (varsayilan) gRPC istekleri (Content-Type application/grpc, grpc-web
//	      haric) HTTP/2 ile gider: http:// hedefte h2c (on bilgili, yani ALPN'siz
//	      dogrudan HTTP/2), https:// hedefte TLS ALPN h2. Diger istekler HTTP/1.1.
//	http1 hicbir istek HTTP/2 kullanmaz (gRPC calismaz; eski davranis).
//	h2    TUM istekler HTTP/2 (http:// hedefte h2c, https:// hedefte ALPN h2).
//	h2c   h2 ile ayni; yalniz duz (TLS'siz) hedefi vurgular.
//
// gRPC icin geri donus YOKTUR: gRPC HTTP/1.1 ile calismaz, h2 baglantisi
// kurulamazsa istek net bir yerel hata (local_unreachable) ile biter.
// Yetenek (http_trailers) sunucuyla pazarlanmadiysa auto modda h2 kullanilmaz.

// Origin protokol degerleri.
const (
	OriginAuto  = "auto"
	OriginHTTP1 = "http1"
	OriginH2    = "h2"
	OriginH2C   = "h2c"
)

// NormalizeOriginProtocol, degeri normalize eder; bilinmeyen/bos -> auto.
func NormalizeOriginProtocol(s string) string {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case OriginHTTP1, OriginH2, OriginH2C:
		return v
	}
	return OriginAuto
}

// ValidOriginProtocol, CLI dogrulamasi: bos veya bilinen deger.
func ValidOriginProtocol(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", OriginAuto, OriginHTTP1, OriginH2, OriginH2C:
		return true
	}
	return false
}

// isGRPCRequest, istek basliklarina gore gRPC mi (grpc-web haric).
func isGRPCRequest(h http.Header) bool {
	ct := strings.ToLower(strings.TrimSpace(h.Get("Content-Type")))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if !strings.HasPrefix(ct, "application/grpc") {
		return false
	}
	rest := ct[len("application/grpc"):]
	return rest == "" || rest[0] == '+'
}

// originClients, h2 yerel istemcileri; ilk kullanimda kurulur.
type originClients struct {
	once  sync.Once
	h2c   *http.Client // duz TCP uzerinden on bilgili HTTP/2
	h2tls *http.Client // TLS ALPN h2
}

func (o *originClients) init() {
	o.once.Do(func() {
		noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		// h2c: SSRF korumali dial ile duz TCP; TLS yok (AllowHTTP + sahte "TLS" dial).
		o.h2c = &http.Client{
			CheckRedirect: noRedirect,
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return guardedDial(ctx, network, addr, true, 10*time.Second)
				},
				// Olu baglantiyi yakala: bosta iken PING, yanit gelmezse kapat.
				ReadIdleTimeout: 30 * time.Second,
				PingTimeout:     15 * time.Second,
			},
		}
		// TLS: http.Transport ALPN ile h2 pazarlar (ForceAttemptHTTP2 ozel dial'da sart).
		// ResponseHeaderTimeout YOK: gRPC sunucusu basligi akis boyunca gecikmeli yollayabilir.
		tr := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return guardedDial(ctx, network, addr, true, 10*time.Second)
			},
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     90 * time.Second,
			HTTP2: &http.HTTP2Config{
				SendPingTimeout: 15 * time.Second,
				PingTimeout:     15 * time.Second,
			},
		}
		o.h2tls = &http.Client{CheckRedirect: noRedirect, Transport: tr}
	})
}

// originClient, istek icin kullanilacak yerel istemciyi ve HTTP/2 olup
// olmadigini doner. scheme: hedef URL semasi (http/https).
func (cs *clientSession) originClient(scheme string, grpc bool) (*http.Client, bool) {
	useH2 := false
	switch cs.originProto {
	case OriginHTTP1:
	case OriginH2, OriginH2C:
		useH2 = true
	default: // auto
		useH2 = grpc && cs.httpTrailers
	}
	if !useH2 {
		return cs.http, false
	}
	cs.origin.init()
	if strings.EqualFold(scheme, "https") {
		return cs.origin.h2tls, true
	}
	return cs.origin.h2c, true
}
