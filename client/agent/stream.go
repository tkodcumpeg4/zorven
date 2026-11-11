package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	tgt := cs.targetFor(m.TunnelID)
	if tgt == zappUnavailable {
		cs.sendStreamAck(m.ReqID, protocol.CodeLocalUnreachable, "uygulama calismiyor")
		return
	}
	addr, err := resolveStreamAddr(tgt, m.Dest)
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

	// Dial iptal edilebilir: dial surerken stream_close gelirse bosuna beklenmez
	// ve acilmis baglanti sizmaz.
	dctx, endDial := cs.beginDial(m.ReqID, context.Background())
	defer endDial()
	conn, err := streamDialContext(dctx, network, addr, streamDialTimeout)
	if err != nil {
		cs.sendStreamAck(m.ReqID, protocol.CodeLocalUnreachable, "yerel hedefe baglanilamadi: "+err.Error())
		return
	}
	if dctx.Err() != nil { // dial sirasinda stream_close geldi
		conn.Close()
		return
	}

	// Akis yetenekleri (yalniz tcp; eski sunucu onermez -> eski davranis).
	var caps []string
	if m.Proto != protocol.ProtoUDP {
		credit, half, accepted := cs.negotiateStreamCaps(m.Caps, true)
		if credit || half {
			conn = newFlowConn(conn, m.ReqID, credit, half)
			caps = accepted
		}
	}

	// ONCE kaydet, SONRA ack gonder: sunucu ack'i alir almaz ziyaretci verisini
	// yollar; akis kayitli degilse routeStreamData o baytlari sessizce dusurur
	// (eszamanli baglantilarda aralikli 19/20 hatasinin kok nedeni).
	cs.registerStream(m.ReqID, conn)
	cs.noteConn(m.ReqID, m.TunnelID)
	cs.tm.request(m.TunnelID)
	if m.Proto == protocol.ProtoUDP {
		cs.streamMu.Lock()
		cs.streamTunnels[m.ReqID] = m.TunnelID
		cs.streamMu.Unlock()
	}
	_ = cs.sendControl(context.Background(), protocol.StreamAck{
		Type: protocol.TypeStreamAck, ReqID: m.ReqID, Caps: caps,
	}, protocol.TypeStreamAck)

	frameType := protocol.FrameStreamData
	if m.Proto == protocol.ProtoUDP {
		frameType = protocol.FrameDatagram
	}
	fc, _ := conn.(*flowConn)

	// yerel -> sunucu. Veri ve kapanis ayni yazim kilidinden SIRAYLA gider
	// (sendFrame senkron): sunucu StreamClose'u son veriden once goremez.
	go func() {
		defer cs.releaseSend(conn, m.ReqID)
		buf := make([]byte, 32*1024)
		for {
			if m.Proto == protocol.ProtoUDP {
				_ = conn.SetReadDeadline(time.Now().Add(udpFlowIdle))
			}
			n, rerr := conn.Read(buf)
			if n > 0 {
				if cs.acquireSend(conn, m.ReqID, n) != nil {
					break // akis kapandi
				}
				if serr := cs.sendFrame(context.Background(), protocol.BodyFrame{
					FrameType: frameType, ReqID: m.ReqID, Payload: buf[:n],
				}); serr != nil {
					if m.Proto == protocol.ProtoUDP {
						cs.udpDrops.add(m.TunnelID, 1) // sunucuya gonderilemedi
					}
					break
				}
				cs.tm.addOut(m.TunnelID, n)
			}
			if rerr != nil {
				if fc != nil && fc.half && errors.Is(rerr, io.EOF) {
					// Yari kapama: hedef yazmayi bitirdi ama okumaya devam edebilir.
					// Akis acik kalir; sunucu iki yon de bitince StreamClose gonderir.
					_ = cs.sendFrame(context.Background(), protocol.BodyFrame{
						FrameType: protocol.FrameStreamData, ReqID: m.ReqID, EOF: true,
					})
					return
				}
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
// baglantiya iletir. UDP: tek datagram, yazim bloklamaz (kisa son tarih, hata =
// dusurme). TCP: SINIRLI yazma kuyrugu + yazim zaman asimi; okuma dongusu bir
// akisin yavasligi yuzunden bloklanmaz, tasmada yalniz o akis kapatilir.
func (cs *clientSession) routeStreamData(_ uint8, reqID uint64, payload []byte) {
	cs.streamMu.Lock()
	conn, ok := cs.streamConns[reqID]
	tunnelID := cs.streamTunnels[reqID]
	cs.streamMu.Unlock()
	if !ok {
		return
	}
	cs.tm.addIn(cs.tunnelOfConn(reqID), len(payload))
	if tunnelID != "" { // UDP akisi
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(payload); err != nil {
			cs.udpDrops.add(tunnelID, 1) // UDP datagrami yerel servise yazilamadi
			cs.closeStream(reqID)
		}
		return
	}
	if len(payload) == 0 {
		// Bos cerceve: yari kapama pazarlandiysa sunucunun EOF isareti. Kalan
		// veri hedefe yazildiktan SONRA CloseWrite yapilir (kuyruk sirasi korunur).
		if fc, ok := conn.(*flowConn); ok && fc.half {
			fc.peerEOF.Store(true)
			cs.streamQueue(reqID, conn).finish()
		}
		return
	}
	_ = cs.streamQueue(reqID, conn).push(payload)
}

// streamQueue, akisin yerel yazma kuyrugunu (tembel olusturarak) doner.
func (cs *clientSession) streamQueue(reqID uint64, conn net.Conn) *writeQueue {
	cs.streamMu.Lock()
	defer cs.streamMu.Unlock()
	q := cs.streamQ[reqID]
	if q == nil {
		if _, live := cs.streamConns[reqID]; !live {
			// Arada kapandi: kayitsiz (sizan) kuyruk yaratma; durdurulmus kuyruk push'u reddeder.
			q = newWriteQueue(func([]byte, time.Duration) error { return nil }, nil, nil)
			q.stop()
			return q
		}
		if cs.streamQ == nil {
			cs.streamQ = make(map[uint64]*writeQueue)
		}
		q = cs.newLocalWriteQueue(conn, func(err error) {
			cs.log.Warn("akis yazma kuyrugu basarisiz, akis kapatiliyor", "req_id", reqID, "hata", err)
			_ = cs.sendControl(context.Background(), protocol.StreamClose{Type: protocol.TypeStreamClose, ReqID: reqID}, protocol.TypeStreamClose)
			cs.closeStream(reqID)
		})
		cs.streamQ[reqID] = q
	}
	return q
}

// streamCloseLinger, ziyaretci tarafi kapatinca (stream_close) yerel hedefe
// kalan baytlar yazilip yazma yonu kapatildiktan sonra, yerel servisin son
// yanitini okumasi icin taninan sure (TCP yarim kapama).
const streamCloseLinger = 3 * time.Second

// handleStreamClose, sunucu akisi kapattiginda: bekleyen dial'i iptal eder; TCP'de
// kuyrugu bosaltip yazma yonunu kapatir (CloseWrite) ve kisa sure yerel yanitin
// akmasina izin verir, sonra tam kapatir. UDP ve kuyrugu olmayan akis aninda kapanir.
func (cs *clientSession) handleStreamClose(reqID uint64) {
	cs.cancelDial(reqID)
	cs.streamMu.Lock()
	q := cs.streamQ[reqID]
	_, isUDP := cs.streamTunnels[reqID]
	_, exists := cs.streamConns[reqID]
	cs.streamMu.Unlock()
	if q == nil || isUDP || !exists {
		cs.closeStream(reqID)
		return
	}
	cs.streamMu.Lock()
	fc, _ := cs.streamConns[reqID].(*flowConn)
	cs.streamMu.Unlock()
	if fc != nil && fc.half && fc.peerEOF.Load() {
		// Yari kapamali akisin DUZGUN sonu: sunucu iki yonun de bittigini
		// biliyor. Hedefe kalan veri yazilip CloseWrite yapilana kadar bekle
		// (3 sn'lik sabit bekleme buyuk kuyrugu yarida kesebilirdi).
		go func() {
			t := time.NewTimer(localWriteTimeout)
			defer t.Stop()
			select {
			case <-fc.finDone:
			case <-q.done:
			case <-t.C:
			}
			cs.closeStream(reqID)
		}()
		return
	}
	q.finish() // kalan yazimlar bitince CloseWrite
	time.AfterFunc(streamCloseLinger, func() { cs.closeStream(reqID) })
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
	delete(cs.streamTunnels, reqID)
	q := cs.streamQ[reqID]
	delete(cs.streamQ, reqID)
	cs.streamMu.Unlock()
	if q != nil {
		q.stop()
	}
	if ok {
		cs.forgetConn(reqID)
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
	cs.streamTunnels = make(map[uint64]string)
	qs := cs.streamQ
	cs.streamQ = nil
	cs.streamMu.Unlock()
	for _, q := range qs {
		q.stop()
	}
	for _, c := range conns {
		c.Close()
	}
}
