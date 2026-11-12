package tunnel

import (
	"context"
	"errors"
	"net/http"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsBridge, tek bir yukseltilmis (upgraded) WebSocket akisini temsil eder.
// Ingress goroutine'i bunu tutar: accept ile 101 sonucunu bekler, FromLocal'dan
// yerel servisten gelen baytlari okuyup tarayiciya yazar.
type wsBridge struct {
	reqID    uint64
	tunnelID string
	accept   chan protocol.WSAccept // tamponlu(1)
	q        *streamQueue           // ajan -> tarayici
	send     creditGate             // tarayici -> ajan
	acked    bool                   // yalniz okuma dongusu
}

// maxWSPerTunnel, bir oturumda tek tunel icin eszamanli WS akisi tavani.
// NEDEN: WS yukseltmesi kimlik gerektirmeyebilir (acik tunel); her akis
// sunucuda goroutine, baglanti ve (kredi penceresi kadar) bellek tutar.
// Tavan, mesru kullanimin (sekme basina birkac WS) cok ustunde tutuldu.
const maxWSPerTunnel = 1000

// ErrTooManyStreams, tunelin eszamanli WS akisi tavani doluyken doner.
var ErrTooManyStreams = errors.New("tunel icin cok fazla eszamanli WebSocket akisi")

func (s *Session) ensureWSMap() {
	if s.wsBridges == nil {
		s.wsBridges = make(map[uint64]*wsBridge)
	}
}

// WSStream, ingress tarafina acilan disa donuk WS akis tutamaci. Ingress
// baska bir pakette oldugu icin wsBridge'in ic alanlarina degil bu tutamaca
// erisir.
type WSStream struct {
	b *wsBridge
	s *Session
}

// ReqID, akisin oturum ici kimligi.
func (w *WSStream) ReqID() uint64 { return w.b.reqID }

// Accept, yerel servisin 101 (veya hata) sonucunun bekleneceği kanal.
func (w *WSStream) Accept() <-chan protocol.WSAccept { return w.b.accept }

// FromLocal, yerel servisten gelen (tarayiciya yazilacak) ham baytlar. Kanal
// tamponsuzdur; duzgun kapanista Closed() tum veri alindiktan sonra kapanir.
func (w *WSStream) FromLocal() <-chan []byte { return w.b.q.out }

// Closed, akis kapandiginda kapanan sinyal kanali.
func (w *WSStream) Closed() <-chan struct{} { return w.b.q.closed }

// Aborted, akis iptal edildiginde (tasma, oturum sonu, sunucu tarafli kapama)
// kapanir; tuketici bloklu tarayici yazisini baglantiyi kapatarak cozmeli.
func (w *WSStream) Aborted() <-chan struct{} { return w.b.q.aborted }

// Send, tarayicidan gelen baytlari yerel servise iletir. Kredi pazarlandiysa
// pencere bitince bekler.
func (w *WSStream) Send(ctx context.Context, data []byte) error {
	if err := w.b.send.acquire(ctx, w.b.q.closed, creditCost(len(data))); err != nil {
		return err
	}
	return w.s.SendWSData(ctx, w.b.reqID, data)
}

// Close, akisi kapatir (ajana WSClose gonderir, koprüyu temizler).
func (w *WSStream) Close(reason string) { w.s.closeWSBridge(w.b, reason) }

// OpenWS, ingress tarafindan cagrilir: yeni bir WS akisi tahsis eder, ajana
// WSOpen gonderir ve akis tutamacini doner. Cagiran Accept() kanalindan 101
// sonucunu beklemelidir.
func (s *Session) OpenWS(ctx context.Context, tunnelID, path, query string, headers map[string][]string) (*WSStream, error) {
	reqID := s.nextReqID.Add(1)
	q := newStreamQueue(&s.streamMem, wsLegacyMaxMem, 0)
	q.grant = func(n int) { s.sendStreamWindow(reqID, n) }
	b := &wsBridge{
		reqID:    reqID,
		tunnelID: tunnelID,
		accept:   make(chan protocol.WSAccept, 1),
		q:        q,
	}
	s.wsMu.Lock()
	s.ensureWSMap()
	n := 0
	for _, o := range s.wsBridges {
		if o.tunnelID == tunnelID {
			n++
		}
	}
	if n >= maxWSPerTunnel {
		s.wsMu.Unlock()
		q.abort()
		return nil, ErrTooManyStreams
	}
	s.wsBridges[reqID] = b
	s.wsMu.Unlock()

	if err := s.Send(ctx, protocol.WSOpen{
		Type: protocol.TypeWSOpen, ReqID: reqID, TunnelID: tunnelID,
		Path: path, Query: query, Headers: headers,
		Caps: []string{protocol.StreamCapCredit},
	}, protocol.TypeWSOpen); err != nil {
		s.removeWS(reqID)
		q.abort()
		return nil, err
	}
	return &WSStream{b: b, s: s}, nil
}

// SendWSData, tarayicidan gelen baytlari ajana (yerel servise) iletir.
func (s *Session) SendWSData(ctx context.Context, reqID uint64, data []byte) error {
	return s.SendBinary(ctx, protocol.BodyFrame{
		FrameType: protocol.FrameWSData, ReqID: reqID, Payload: data,
	})
}

// CloseWS, ajana WSClose gonderir ve yerel koprüyu temizler.
func (s *Session) CloseWS(reqID uint64, reason string) {
	if b, ok := s.getWS(reqID); ok {
		s.closeWSBridge(b, reason)
		return
	}
	_ = s.Send(context.Background(), protocol.WSClose{
		Type: protocol.TypeWSClose, ReqID: reqID, Reason: reason,
	}, protocol.TypeWSClose)
}

// closeWSBridge, kopruyu (haritadan kaldirilmis olsa bile) iptal eder.
func (s *Session) closeWSBridge(b *wsBridge, reason string) {
	_ = s.Send(context.Background(), protocol.WSClose{
		Type: protocol.TypeWSClose, ReqID: b.reqID, Reason: reason,
	}, protocol.TypeWSClose)
	b.q.abort()
	s.wsMu.Lock()
	if s.wsBridges[b.reqID] == b {
		delete(s.wsBridges, b.reqID)
	}
	s.wsMu.Unlock()
}

func (s *Session) getWS(reqID uint64) (*wsBridge, bool) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	b, ok := s.wsBridges[reqID]
	return b, ok
}

func (s *Session) removeWS(reqID uint64) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	delete(s.wsBridges, reqID)
}

// --- Okuma dongusunden cagrilan yonlendiriciler ---------------------------

func (s *Session) routeWSAccept(acc protocol.WSAccept) {
	b, ok := s.getWS(acc.ReqID)
	if !ok || b.acked {
		return
	}
	b.acked = true
	if acc.Code == "" && acc.Status == http.StatusSwitchingProtocols && protocol.HasCap(acc.Caps, protocol.StreamCapCredit) {
		b.q.credit.Store(true)
		b.send.enable()
	}
	select {
	case b.accept <- acc:
	default:
	}
}

// routeWSData, ajandan gelen (yerel->tarayici) baytlari koprüye teslim eder.
// Tavan/kredi asilirsa akisi kapatir; okuma dongusunu ASLA bloklamaz.
//
// Payload KOPYALANMAZ: coder/websocket her mesaj icin taze tampon ayirir
// (exchange.go writeBody ile ayni varsayim); bellek hesabi cap() ile yapilir.
func (s *Session) routeWSData(reqID uint64, payload []byte) {
	b, ok := s.getWS(reqID)
	if !ok || len(payload) == 0 {
		return
	}
	if err := b.q.push(payload); err != nil {
		s.logStreamAbort("ws", reqID, b.tunnelID, err)
		b.q.abort()
		s.goSafe("tunnel.ws_close", nil, func() { s.closeWSBridge(b, "overflow") })
	}
}

// routeWSClose, ajan WS akisini kapatti: kalan veri tarayiciya yazildiktan
// sonra Closed() kapanir.
func (s *Session) routeWSClose(reqID uint64) {
	if b, ok := s.getWS(reqID); ok {
		b.q.remoteClose()
	}
	s.removeWS(reqID)
}

// closeAllWS, oturum kapaninca tum WS koprülerini serbest birakir.
func (s *Session) closeAllWS() {
	s.wsMu.Lock()
	bridges := make([]*wsBridge, 0, len(s.wsBridges))
	for _, b := range s.wsBridges {
		bridges = append(bridges, b)
	}
	s.wsBridges = make(map[uint64]*wsBridge)
	s.wsMu.Unlock()
	for _, b := range bridges {
		b.q.abort()
	}
}
