// Package netconnect, "zorven connect" komutunun yerel SOCKS5 sunucusudur
// (FAZ 3 / F17, Asama 1). Uygulamalar 127.0.0.1:1080'e SOCKS5 ile baglanir;
// her CONNECT istegi Zorven sunucusuna ayri bir WSS baglantisiyla tasinir.
//
// Yalnizca RFC 1928'in gereken alt kumesi: kimlik dogrulamasiz (0x00) yontem
// ve CONNECT komutu. BIND ve UDP ASSOCIATE desteklenmez ve dogru hata koduyla
// reddedilir. Kimlik dogrulama yerel dinleyicide yok cunku varsayilan olarak
// yalnizca loopback'e baglanir; asil kimlik (API token) sunucu tarafindadir.
package netconnect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// SOCKS5 sabitleri (RFC 1928).
const (
	socksVersion = 0x05

	methodNoAuth       = 0x00
	methodNoAcceptable = 0xFF

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	// Yanit kodlari.
	RepSucceeded           = 0x00
	RepGeneralFailure      = 0x01
	RepNotAllowed          = 0x02
	RepHostUnreachable     = 0x04
	RepConnectionRefused   = 0x05
	RepCommandNotSupported = 0x07
	RepAddrNotSupported    = 0x08
)

// ErrNoAcceptableMethod, istemci kimlik dogrulamasiz yontemi sunmadi.
var ErrNoAcceptableMethod = errors.New("socks5: kabul edilebilir yontem yok")

// ErrUnsupportedCommand, CONNECT disinda bir komut istendi.
var ErrUnsupportedCommand = errors.New("socks5: yalnizca CONNECT desteklenir")

// negotiate, selamlasmayi okur ve yanitlar. Kimliksiz yontem yoksa 0xFF
// yazar ve ErrNoAcceptableMethod doner.
func negotiate(rw io.ReadWriter) error {
	var hdr [2]byte
	if _, err := io.ReadFull(rw, hdr[:]); err != nil {
		return err
	}
	if hdr[0] != socksVersion {
		return fmt.Errorf("socks5: desteklenmeyen surum %d", hdr[0])
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(rw, methods); err != nil {
		return err
	}
	for _, m := range methods {
		if m == methodNoAuth {
			_, err := rw.Write([]byte{socksVersion, methodNoAuth})
			return err
		}
	}
	_, _ = rw.Write([]byte{socksVersion, methodNoAcceptable})
	return ErrNoAcceptableMethod
}

// readRequest, CONNECT istegini okur ve "host:port" doner. Desteklenmeyen
// komut/adres turunde uygun yaniti YAZAR ve hata doner.
func readRequest(rw io.ReadWriter) (string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(rw, hdr[:]); err != nil {
		return "", err
	}
	if hdr[0] != socksVersion {
		return "", fmt.Errorf("socks5: desteklenmeyen surum %d", hdr[0])
	}

	var host string
	switch hdr[3] {
	case atypIPv4:
		var b [4]byte
		if _, err := io.ReadFull(rw, b[:]); err != nil {
			return "", err
		}
		host = net.IP(b[:]).String()
	case atypIPv6:
		var b [16]byte
		if _, err := io.ReadFull(rw, b[:]); err != nil {
			return "", err
		}
		host = net.IP(b[:]).String()
	case atypDomain:
		var l [1]byte
		if _, err := io.ReadFull(rw, l[:]); err != nil {
			return "", err
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(rw, b); err != nil {
			return "", err
		}
		host = string(b)
	default:
		_ = WriteReply(rw, RepAddrNotSupported)
		return "", fmt.Errorf("socks5: desteklenmeyen adres turu %d", hdr[3])
	}

	var p [2]byte
	if _, err := io.ReadFull(rw, p[:]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(p[:])

	// Komutu adres okunduktan SONRA degerlendir: aksi halde akista okunmamis
	// bayt kalir ve yanit istemci tarafinda yanlis yorumlanir.
	if hdr[1] != cmdConnect {
		_ = WriteReply(rw, RepCommandNotSupported)
		return "", ErrUnsupportedCommand
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// WriteReply, bir SOCKS5 yaniti yazar. Baglanan adres bilgisi anlamli
// olmadigi icin 0.0.0.0:0 bildirilir (istemciler bunu kullanmaz).
func WriteReply(w io.Writer, rep byte) error {
	_, err := w.Write([]byte{socksVersion, rep, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}
