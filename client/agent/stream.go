package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ham TCP/UDP tunel (FAZ 3 / D2): sunucudan gelen StreamOpen ile yerel hedefe
// TCP/UDP baglantisi acilir ve baytlar/datagramlar iki yonlu kopyalanir.
//
// WebSocket passthrough'a (ws.go) cok benzer; fark: HTTP upgrade yoktur, ham
// baytlar dogrudan tasinir. UDP'de her okuma/yazma bir datagramdir.

const streamDialTimeout = 15 * time.Second

// udpFlowIdle, UDP akisinin (baglanti kavramsizdir) yerel soketi bosta bu sure
// sonra kapatilir; boylece sizinti olmaz.
const udpFlowIdle = 90 * time.Second

// rawHostPort, tunel hedefinden ("tcp://host:port", "host:port", "http://h:p/")
// dial edilebilir host:port cikarir.
func rawHostPort(target string) (string, error) {
	t := strings.TrimSpace(target)
	if i := strings.Index(t, "://"); i >= 0 {
		t = t[i+3:]
	}
	t = strings.TrimSuffix(t, "/")
	host, port, err := net.SplitHostPort(t)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("ham tunel hedefi host:port biciminde olmali (or. localhost:25565)")
	}
	return net.JoinHostPort(host, port), nil
}

// subnetPrefix, alt ag tunel hedeflerinin oneki (FAZ 3 / F20): "subnet:192.168.1.0/24".
const subnetPrefix = "subnet:"

// resolveStreamAddr, akisin baglanacagi adresi belirler.
//
// Tekil kaynak: hedef tunel TANIMINDAN gelir; istekteki dest YOK SAYILIR
// (kullanici ajan uzerinden keyfi adres tarayamasin).
//
// Alt ag (F20): dest ZORUNLUDUR, bir IP LITERALI olmalidir (ad olursa DNS
// cozumlemesi araligin disina cikabilirdi) ve ilan edilen araligin ICINDE
// olmalidir. Sunucu da ayni kontrolu yapar; bu, ajan tarafindaki bagimsiz
// ikinci kapidir — ele gecirilmis bir sunucu bile araligin disina yonlendiremez.
func resolveStreamAddr(target, dest string) (string, error) {
	if !strings.HasPrefix(target, subnetPrefix) {
		return rawHostPort(target)
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(strings.TrimPrefix(target, subnetPrefix)))
	if err != nil {
		return "", fmt.Errorf("gecersiz alt ag tanimi")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(dest))
	if err != nil {
		return "", fmt.Errorf("alt ag hedefi ip:port olmali")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Errorf("alt ag hedefi bir IP adresi olmali")
	}
	if !prefix.Contains(ip.Unmap()) {
		return "", fmt.Errorf("hedef ilan edilen alt agin disinda")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("gecersiz port")
	}
	return net.JoinHostPort(ip.Unmap().String(), port), nil
}

// handleStreamOpen, yerel TCP/UDP hedefe baglanip akisi kopruler.
func (cs *clientSession) handleStreamOpen(m protocol.StreamOpen) {
	addr, err := resolveStreamAddr(cs.targetFor(m.TunnelID), m.Dest)
	if err != nil {
		cs.sendStreamAck(m.ReqID, protocol.CodeLocalUnreachable, err.Error())
		return
	}
	host, _, _ := net.SplitHostPort(addr)
	if isBlockedTargetHost(host) {
		cs.sendStreamAck(m.ReqID, protocol.CodeLocalUnreachable, "SSRF korumasi: hedef engellendi")
		return
	}

	network := "tcp"
	if m.Proto == protocol.ProtoUDP {
		network = "udp"
	}

	conn, err := (&net.Dialer{Timeout: streamDialTimeout}).Dial(network, addr)
	if err != nil {
		cs.sendStreamAck(m.ReqID, protocol.CodeLocalUnreachable, "yerel hedefe baglanilamadi: "+err.Error())
		return
	}

	cs.sendStreamAck(m.ReqID, "", "")
	cs.registerStream(m.ReqID, conn)

	frameType := protocol.FrameStreamData
	if m.Proto == protocol.ProtoUDP {
		frameType = protocol.FrameDatagram
	}

	// yerel -> sunucu
	go func() {
		buf := make([]byte, 32*1024)
		for {
			if m.Proto == protocol.ProtoUDP {
				_ = conn.SetReadDeadline(time.Now().Add(udpFlowIdle))
			}
			n, rerr := conn.Read(buf)
			if n > 0 {
				if serr := cs.sendFrame(context.Background(), protocol.BodyFrame{
					FrameType: frameType, ReqID: m.ReqID, Payload: buf[:n],
				}); serr != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		_ = cs.sendControl(context.Background(), protocol.StreamClose{
			Type: protocol.TypeStreamClose, ReqID: m.ReqID,
		}, protocol.TypeStreamClose)
		cs.closeStream(m.ReqID)
	}()
}

// routeStreamData, sunucudan gelen (ziyaretci -> yerel) baytlari/datagrami yerel
// baglantiya yazar. FrameStreamData ve FrameDatagram ayni yolu kullanir (bagli
// UDP soketinde Write bir datagram uretir).
func (cs *clientSession) routeStreamData(_ uint8, reqID uint64, payload []byte) {
	cs.streamMu.Lock()
	conn, ok := cs.streamConns[reqID]
	cs.streamMu.Unlock()
	if !ok {
		return
	}
	if _, err := conn.Write(payload); err != nil {
		cs.closeStream(reqID)
	}
}

func (cs *clientSession) handleStreamClose(reqID uint64) {
	cs.closeStream(reqID)
}

func (cs *clientSession) sendStreamAck(reqID uint64, code, msg string) {
	_ = cs.sendControl(context.Background(), protocol.StreamAck{
		Type: protocol.TypeStreamAck, ReqID: reqID, Code: code, Message: msg,
	}, protocol.TypeStreamAck)
}

func (cs *clientSession) registerStream(reqID uint64, conn net.Conn) {
	cs.streamMu.Lock()
	cs.streamConns[reqID] = conn
	cs.streamMu.Unlock()
}

func (cs *clientSession) closeStream(reqID uint64) {
	cs.streamMu.Lock()
	conn, ok := cs.streamConns[reqID]
	delete(cs.streamConns, reqID)
	cs.streamMu.Unlock()
	if ok {
		conn.Close()
	}
}

// closeAllStreams, oturum kapaninca tum yerel ham baglantilari kapatir.
func (cs *clientSession) closeAllStreams() {
	cs.streamMu.Lock()
	conns := make([]net.Conn, 0, len(cs.streamConns))
	for _, c := range cs.streamConns {
		conns = append(conns, c)
	}
	cs.streamConns = make(map[uint64]net.Conn)
	cs.streamMu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}
