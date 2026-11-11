package agent

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ham TCP ve WS akislarinda akis basina kredi ve yari kapama (ajan tarafi).
// Pazarlik ve tel bicimi: shared/protocol/streamflow.go.
//
// NEDEN: eskiden iki yonde de geri basinc yoktu. Yerel hedef hizli yazip
// ziyaretci yavas okudugunda veri sunucu belleginde birikiyordu (5 okumayan
// ziyaretci: 93 MB -> 724 MB); ziyaretci hizli yazip hedef yavas okudugunda
// ajanin yazma kuyrugu 4 MB'ta tasip akisi kesiyordu. Kredi ile gonderen
// pencere bitince bekler: bekleyen okuyucu TCP uzerinden kaynagi yavaslatir.

// flowConn, pazarlik sonucunu ve akis durumunu yerel baglantiyla birlikte
// tasir. streamConns / wsConns haritalarinda net.Conn olarak saklanir; boylece
// akis durumu icin clientSession'a yeni alan gerekmez.
type flowConn struct {
	net.Conn
	reqID  uint64
	credit bool // sunucu kredi oneriyor ve ajan kabul etti
	half   bool // yari kapama pazarlandi (yalniz tcp)

	// ctx, kredi beklemesini akis kapaninca iptal eder.
	ctx    context.Context
	cancel context.CancelFunc

	grantMu  sync.Mutex
	grantAcc int

	peerEOF atomic.Bool   // sunucu tarafi yazmayi bitirdi (EOF isareti geldi)
	finDone chan struct{} // yerel hedefe CloseWrite yapildi
	finOnce sync.Once
}

func newFlowConn(c net.Conn, reqID uint64, credit, half bool) *flowConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &flowConn{Conn: c, reqID: reqID, credit: credit, half: half, ctx: ctx, cancel: cancel, finDone: make(chan struct{})}
}

// Close, bekleyen kredi beklemesini de serbest birakir.
func (f *flowConn) Close() error {
	f.cancel()
	return f.Conn.Close()
}

// negotiateStreamCaps, sunucunun onerdigi yeteneklerden ajanin kabul ettiklerini
// doner. Kredi icin pencere tablosu (cs.windows) gerekir.
func (cs *clientSession) negotiateStreamCaps(offered []string, allowHalf bool) (credit, half bool, caps []string) {
	if cs.windows != nil && protocol.HasCap(offered, protocol.StreamCapCredit) {
		credit = true
		caps = append(caps, protocol.StreamCapCredit)
	}
	if allowHalf && protocol.HasCap(offered, protocol.StreamCapHalfClose) {
		half = true
		caps = append(caps, protocol.StreamCapHalfClose)
	}
	return credit, half, caps
}

// acquireSend, sunucuya n bayt gondermeden once krediyi bekler (kredi yoksa hemen doner).
func (cs *clientSession) acquireSend(c net.Conn, reqID uint64, n int) error {
	fc, ok := c.(*flowConn)
	if !ok || !fc.credit {
		return nil
	}
	return cs.windows.acquire(fc.ctx, reqID, n+protocol.StreamFrameCost)
}

// releaseSend, okuyucu bitince pencere kaydini temizler.
func (cs *clientSession) releaseSend(c net.Conn, reqID uint64) {
	if fc, ok := c.(*flowConn); ok && fc.credit {
		cs.windows.release(reqID)
	}
}

// grantRecv, yerel hedefe yazilan veri icin sunucuya kredi iade eder (esik:
// pencerenin yarisi, sunucudaki HTTP ile ayni).
func (cs *clientSession) grantRecv(fc *flowConn, n int) {
	fc.grantMu.Lock()
	fc.grantAcc += n + protocol.StreamFrameCost
	acc := fc.grantAcc
	if acc < protocol.InitialWindowBytes/2 {
		fc.grantMu.Unlock()
		return
	}
	fc.grantAcc = 0
	fc.grantMu.Unlock()
	_ = cs.sendControl(context.Background(), protocol.WindowUpdate{
		Type: protocol.TypeWindowUpdate, ReqID: fc.reqID, Increment: acc,
	}, protocol.TypeWindowUpdate)
}

// newLocalWriteQueue, yerel baglantiya yazan kuyruk. flowConn ise yazimdan
// sonra kredi iade eder ve CloseWrite'in yapildigini isaretler.
func (cs *clientSession) newLocalWriteQueue(c net.Conn, onFail func(error)) *writeQueue {
	fc, ok := c.(*flowConn)
	if !ok {
		return newConnWriteQueue(c, onFail)
	}
	return newWriteQueue(func(b []byte, d time.Duration) error {
		_ = fc.Conn.SetWriteDeadline(time.Now().Add(d))
		_, err := fc.Conn.Write(b)
		if err == nil && fc.credit {
			cs.grantRecv(fc, len(b))
		}
		return err
	}, func() {
		if cw, ok := fc.Conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		fc.finOnce.Do(func() { close(fc.finDone) })
	}, onFail)
}
