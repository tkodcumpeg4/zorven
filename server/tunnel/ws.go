package tunnel

import (
	"context"
	"sync"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsBridge, tek bir yukseltilmis (upgraded) WebSocket akisini temsil eder.
// Ingress goroutine'i bunu tutar: accept ile 101 sonucunu bekler, fromLocal'dan
// yerel servisten gelen baytlari okuyup tarayiciya yazar.
type wsBridge struct {
	reqID     uint64
	accept    chan protocol.WSAccept // tamponlu(1)
	fromLocal chan []byte            // ajan -> sunucu (yerel -> tarayici)
	closed    chan struct{}
	closeOnce sync.Once
}

func (b *wsBridge) close() {
	b.closeOnce.Do(func() { close(b.closed) })
}

// wsFromLocalBuffer, yerel->tarayici yonunde bekleyebilecek cerceve sayisi.
// WS trafigi ( or. HMR) dusuk hacimlidir; dolarsa akis kapatilir ki paylasilan
// okuma dongusu ASLA bloklanmasin (head-of-line korumasi).
const wsFromLocalBuffer = 1024

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

// FromLocal, yerel servisten gelen (tarayiciya yazilacak) ham baytlar.
func (w *WSStream) FromLocal() <-chan []byte { return w.b.fromLocal }

// Closed, akis kapandiginda kapanan sinyal kanali.
func (w *WSStream) Closed() <-chan struct{} { return w.b.closed }

// Send, tarayicidan gelen baytlari yerel servise iletir.
func (w *WSStream) Send(ctx context.Context, data []byte) error {
	return w.s.SendWSData(ctx, w.b.reqID, data)
}

// Close, akisi kapatir (ajana WSClose gonderir, koprüyu temizler).
func (w *WSStream) Close(reason string) { w.s.CloseWS(w.b.reqID, reason) }

// OpenWS, ingress tarafindan cagrilir: yeni bir WS akisi tahsis eder, ajana
// WSOpen gonderir ve akis tutamacini doner. Cagiran Accept() kanalindan 101
// sonucunu beklemelidir.
func (s *Session) OpenWS(ctx context.Context, tunnelID, path, query string, headers map[string][]string) (*WSStream, error) {
	reqID := s.nextReqID.Add(1)
	b := &wsBridge{
		reqID:     reqID,
		accept:    make(chan protocol.WSAccept, 1),
		fromLocal: make(chan []byte, wsFromLocalBuffer),
		closed:    make(chan struct{}),
	}
	s.wsMu.Lock()
	s.ensureWSMap()
	s.wsBridges[reqID] = b
	s.wsMu.Unlock()

	if err := s.Send(ctx, protocol.WSOpen{
		Type: protocol.TypeWSOpen, ReqID: reqID, TunnelID: tunnelID,
		Path: path, Query: query, Headers: headers,
	}, protocol.TypeWSOpen); err != nil {
		s.removeWS(reqID)
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
	_ = s.Send(context.Background(), protocol.WSClose{
		Type: protocol.TypeWSClose, ReqID: reqID, Reason: reason,
	}, protocol.TypeWSClose)
	if b, ok := s.getWS(reqID); ok {
		b.close()
	}
	s.removeWS(reqID)
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
	if b, ok := s.getWS(acc.ReqID); ok {
		select {
		case b.accept <- acc:
		default:
		}
	}
}

// routeWSData, ajandan gelen (yerel->tarayici) baytlari koprüye teslim eder.
// Tampon dolarsa akisi kapatir; okuma dongusunu ASLA bloklamaz.
func (s *Session) routeWSData(reqID uint64, payload []byte) {
	b, ok := s.getWS(reqID)
	if !ok {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	select {
	case b.fromLocal <- cp:
	case <-b.closed:
	default:
		b.close() // tampon doldu: tarayici cok yavas — akisi kes
	}
}

func (s *Session) routeWSClose(reqID uint64) {
	if b, ok := s.getWS(reqID); ok {
		b.close()
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
		b.close()
	}
}
