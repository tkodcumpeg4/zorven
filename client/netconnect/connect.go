package netconnect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/coder/websocket"
)

// dialTimeout, sunucuya WSS baglantisi + ajan onayi icin ust sinir.
// Sunucu ajanin yerel hedefe baglanmasini 15 sn bekler; uzerine pay.
const dialTimeout = 25 * time.Second

// Server, yerel SOCKS5 dinleyicisi.
type Server struct {
	ServerAddr string // "zorven.app:443"
	Token      string // API token (Authorization: Bearer)
	Insecure   bool   // ws:// (yalnizca lokal gelistirme)
	Listen     string // "127.0.0.1:1080"
	HTTPClient *http.Client
	Log        *slog.Logger

	// AllowFrom (F19 ag gecidi modu): bos degilse YALNIZCA bu araliklardan
	// gelen yerel baglantilar kabul edilir. Yerel dinleyicide kimlik
	// dogrulama olmadigi icin LAN'a acilan gecidin tek kapisi budur.
	AllowFrom []netip.Prefix

	// OnConnect, her baglanti sonucunda cagrilir (CLI ekrana yazar). nil olabilir.
	OnConnect func(target string, err error)
}

// ListenAndServe, ctx iptal edilene kadar baglanti kabul eder.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return fmt.Errorf("dinlenemedi (%s): %w", s.Listen, err)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go s.handle(ctx, c)
	}
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	if !s.sourceAllowed(c.RemoteAddr()) {
		return // selamlasma bile yok: izinsiz istemciye proxy varligi sizmaz
	}
	// Selamlasma ve istek icin kisa sure: yarim kalan istemci kaynak tutmasin.
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if err := negotiate(c); err != nil {
		return
	}
	target, err := readRequest(c)
	if err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})

	remote, rep, err := s.dial(ctx, target)
	if s.OnConnect != nil {
		s.OnConnect(target, err)
	}
	if err != nil {
		_ = WriteReply(c, rep)
		return
	}
	defer remote.Close()
	if err := WriteReply(c, RepSucceeded); err != nil {
		return
	}
	pipe(c, remote)
}

// dial, sunucuya WSS acar. Hata halinde uygun SOCKS yanit kodunu da doner.
func (s *Server) dial(ctx context.Context, target string) (net.Conn, byte, error) {
	scheme := "wss"
	if s.Insecure {
		scheme = "ws"
	}
	u := url.URL{
		Scheme:   scheme,
		Host:     s.ServerAddr,
		Path:     "/api/v1/network/connect",
		RawQuery: url.Values{"target": {target}}.Encode(),
	}

	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	wc, resp, err := websocket.Dial(dctx, u.String(), &websocket.DialOptions{
		HTTPClient: s.HTTPClient,
		// Token basliga konur, sorgu dizisine DEGIL: sorgu erisim loglarina sizar.
		HTTPHeader: http.Header{"Authorization": {"Bearer " + s.Token}},
	})
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		return nil, replyForStatus(code), statusError(code, err)
	}
	wc.SetReadLimit(1 << 20)
	// NetConn kendi context'ine baglanir; dial zaman asimindan bagimsiz olmali.
	return websocket.NetConn(ctx, wc, websocket.MessageBinary), RepSucceeded, nil
}

// replyForStatus, sunucunun HTTP yanitini en yakin SOCKS5 koduna esler.
func replyForStatus(code int) byte {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired:
		return RepNotAllowed
	case http.StatusNotFound, http.StatusBadRequest:
		return RepHostUnreachable
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return RepConnectionRefused
	default:
		return RepGeneralFailure
	}
}

// statusError, kullaniciya anlasilir bir hata metni uretir.
func statusError(code int, err error) error {
	switch code {
	case http.StatusUnauthorized:
		return errors.New("API token gecersiz")
	case http.StatusForbidden:
		return errors.New("erisim reddedildi (token kapsami veya plan)")
	case http.StatusPaymentRequired:
		return errors.New("ozel ag bu planda yok (Team+)")
	case http.StatusNotFound:
		return errors.New("ozel kaynak bulunamadi veya erisim yetkiniz yok")
	case http.StatusServiceUnavailable:
		return errors.New("kaynagi yayinlayan cihaz cevrimdisi")
	case http.StatusBadGateway:
		return errors.New("cihaz yerel hedefe baglanamadi")
	}
	return err
}

// pipe, iki baglanti arasinda veri tasir; biri kapaninca ikisini de kapatir.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}

// sourceAllowed, yerel istemcinin AllowFrom araliklarinda olup olmadigi.
// AllowFrom bossa (yalnizca loopback dinlemede) her baglanti kabul edilir.
func (s *Server) sourceAllowed(a net.Addr) bool {
	if len(s.AllowFrom) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.AllowFrom {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
