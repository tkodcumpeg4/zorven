package rawproxy

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// SNI demux (FAZ 3 / D2, Mod B). :443'e gelen her TLS baglantisinin ClientHello'su
// EL SIKISMADAN once okunur (peek) ve SNI cikarilir. SNI bir SNI-modu ham
// TCP-tuneline eslesiyorsa baglanti dogrudan agent akisina koprülenir; degilse
// baglanti (peek edilen baytlar oynatilarak) http.Server'a devredilir ki kendi
// TLS'ini (HTTP/2 dahil) yapabilsin.

const sniPeekTimeout = 10 * time.Second

var errPeekDone = errors.New("clienthello peeked")

// DemuxListener, net.Listener arayuzunu uygular: Accept YALNIZCA HTTP'ye
// yonlendirilen (henuz TLS yapilmamis, ClientHello baytlari oynatilan) conn'lari
// verir. http.Server.ServeTLS bunlari sarmalayip TLS'i kendisi yapar.
type DemuxListener struct {
	inner     net.Listener
	tlsConfig *tls.Config
	lookup    func(host string) (TunnelInfo, bool)
	onRaw     func(conn net.Conn, ti TunnelInfo)
	log       *slog.Logger

	httpCh    chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

// NewDemuxListener, demux'u baslatir (kendi accept dongusunu goroutine'de calistirir).
// tlsConfig, ham (SNI-modu) baglantilarin TLS'ini sonlandirmak icin kullanilir;
// HTTP baglantilari peek edilip ham hallleriyle Accept'ten donduruldugunden
// http.Server.ServeTLS ayni config ile onlarin TLS'ini kendisi yapar.
func NewDemuxListener(inner net.Listener, tlsConfig *tls.Config, lookup func(string) (TunnelInfo, bool), onRaw func(net.Conn, TunnelInfo), log *slog.Logger) *DemuxListener {
	d := &DemuxListener{
		inner: inner, tlsConfig: tlsConfig, lookup: lookup, onRaw: onRaw, log: log,
		httpCh: make(chan net.Conn), closed: make(chan struct{}),
	}
	go d.run()
	return d
}

func (d *DemuxListener) run() {
	for {
		c, err := d.inner.Accept()
		if err != nil {
			select {
			case <-d.closed:
			default:
				d.Close()
			}
			return
		}
		go d.handle(c)
	}
}

func (d *DemuxListener) handle(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(sniPeekTimeout))
	sni, replay, err := peekSNI(c)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		// Peek basarisiz (TLS olmayan / bozuk): http tarafina birak, o karar versin.
		d.toHTTP(replay)
		return
	}
	if sni != "" && d.lookup != nil {
		if ti, ok := d.lookup(sni); ok {
			// Ham (SNI-modu) tunel: TLS'i BURADA sonlandir, sonra cozulmus akisi
			// agent'a koprüle. Forwarder normal bir TLS istemcisidir.
			tc := tls.Server(replay, d.tlsConfig)
			_ = tc.SetDeadline(time.Now().Add(sniPeekTimeout))
			if herr := tc.Handshake(); herr != nil {
				tc.Close()
				return
			}
			_ = tc.SetDeadline(time.Time{})
			d.onRaw(tc, ti)
			return
		}
	}
	d.toHTTP(replay)
}

func (d *DemuxListener) toHTTP(c net.Conn) {
	select {
	case d.httpCh <- c:
	case <-d.closed:
		c.Close()
	}
}

// Accept, http.Server.ServeTLS icin peek edilmis conn'lari verir.
func (d *DemuxListener) Accept() (net.Conn, error) {
	select {
	case c := <-d.httpCh:
		return c, nil
	case <-d.closed:
		return nil, net.ErrClosed
	}
}

func (d *DemuxListener) Close() error {
	d.closeOnce.Do(func() {
		close(d.closed)
		d.inner.Close()
	})
	return nil
}

func (d *DemuxListener) Addr() net.Addr { return d.inner.Addr() }

// peekSNI, baglantidan ClientHello'yu EL SIKISMADAN okur ve SNI'yi doner.
// Yontem: tls.Server'i GetConfigForClient ile kosar; SNI yakalanir yakalanmaz
// bir sentinel hata dondurulerek handshake iptal edilir. Bu sirada sunucunun
// istemciye yazacagi uyari (alert) DISCARD edilir ki gercek handshake bozulmasin.
// Donen conn, okunan ClientHello baytlarini yeniden oynatir.
func peekSNI(c net.Conn) (sni string, replay net.Conn, err error) {
	rec := &peekConn{r: c}
	perr := tls.Server(rec, &tls.Config{
		GetConfigForClient: func(hi *tls.ClientHelloInfo) (*tls.Config, error) {
			sni = hi.ServerName
			return nil, errPeekDone
		},
	}).Handshake()

	replay = &prefixConn{Conn: c, prefix: bytes.NewReader(rec.buf.Bytes())}

	if perr != nil && !errors.Is(perr, errPeekDone) {
		return "", replay, perr
	}
	return sni, replay, nil
}

// peekConn, yalnizca okuma yapar ve okunanlari buf'a kaydeder; yazmalar
// DISCARD edilir (peek sirasindaki alert istemciye gitmesin).
type peekConn struct {
	r   net.Conn
	buf bytes.Buffer
}

func (p *peekConn) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.buf.Write(b[:n])
	}
	return n, err
}
func (p *peekConn) Write(b []byte) (int, error)        { return len(b), nil } // discard
func (p *peekConn) Close() error                       { return nil }
func (p *peekConn) LocalAddr() net.Addr                { return p.r.LocalAddr() }
func (p *peekConn) RemoteAddr() net.Addr               { return p.r.RemoteAddr() }
func (p *peekConn) SetDeadline(t time.Time) error      { return nil }
func (p *peekConn) SetReadDeadline(t time.Time) error  { return nil }
func (p *peekConn) SetWriteDeadline(t time.Time) error { return nil }

// prefixConn, once prefix'i (peek edilen ClientHello) sonra alttaki conn'u okur;
// yazmalar dogrudan alttaki conn'a gider.
type prefixConn struct {
	net.Conn
	prefix *bytes.Reader
}

func (p *prefixConn) Read(b []byte) (int, error) {
	if p.prefix != nil && p.prefix.Len() > 0 {
		n, err := p.prefix.Read(b)
		if err == io.EOF {
			err = nil
		}
		return n, err
	}
	return p.Conn.Read(b)
}
