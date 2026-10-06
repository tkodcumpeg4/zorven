package tunnel

import (
	"context"
	"sync"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// rawBridge, tek bir ham TCP/UDP tunel akisini temsil eder (FAZ 3 / D2).
// Ingress goroutine'i bunu tutar: accept ile baglanti sonucunu bekler,
// fromLocal'dan yerel servisten gelen baytlari/datagrami okuyup ziyaretciye yazar.
type rawBridge struct {
	reqID     uint64
	proto     string // tcp | udp — gonderilecek cerceve tipini belirler
	accept    chan protocol.StreamAck
	fromLocal chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func (b *rawBridge) close() {
	b.closeOnce.Do(func() { close(b.closed) })
}

// rawFromLocalBuffer, yerel->ziyaretci yonunde bekleyebilecek cerceve sayisi.
// Dolarsa akis kapatilir ki paylasilan okuma dongusu ASLA bloklanmasin.
const rawFromLocalBuffer = 2048

func (s *Session) ensureRawMap() {
	if s.rawBridges == nil {
		s.rawBridges = make(map[uint64]*rawBridge)
	}
}

// RawStream, ingress tarafina acilan disa donuk ham akis tutamaci.
type RawStream struct {
	b *rawBridge
	s *Session
}

func (r *RawStream) ReqID() uint64                            { return r.b.reqID }
func (r *RawStream) Accept() <-chan protocol.StreamAck        { return r.b.accept }
func (r *RawStream) FromLocal() <-chan []byte                 { return r.b.fromLocal }
func (r *RawStream) Closed() <-chan struct{}                  { return r.b.closed }
func (r *RawStream) Send(ctx context.Context, d []byte) error { return r.s.sendStreamData(ctx, r.b, d) }
func (r *RawStream) Close(reason string)                      { r.s.CloseStream(r.b.reqID, reason) }

// OpenStream, ingress tarafindan cagrilir: yeni ham akis tahsis eder, ajana
// StreamOpen gonderir ve tutamaci doner. Cagiran Accept()'ten sonucu beklemeli.
func (s *Session) OpenStream(ctx context.Context, tunnelID, proto, remoteAddr string) (*RawStream, error) {
	return s.OpenStreamTo(ctx, tunnelID, proto, remoteAddr, "")
}

// OpenStreamTo, OpenStream gibidir; ek olarak alt ag yonlendirmesi (F20) icin
// hedefi tasir. Hedefin araliga uygunlugunu ajan da ayrica dogrular.
func (s *Session) OpenStreamTo(ctx context.Context, tunnelID, proto, remoteAddr, dest string) (*RawStream, error) {
	reqID := s.nextReqID.Add(1)
	b := &rawBridge{
		reqID:     reqID,
		proto:     proto,
		accept:    make(chan protocol.StreamAck, 1),
		fromLocal: make(chan []byte, rawFromLocalBuffer),
		closed:    make(chan struct{}),
	}
	s.rawMu.Lock()
	s.ensureRawMap()
	s.rawBridges[reqID] = b
	s.rawMu.Unlock()

	if err := s.Send(ctx, protocol.StreamOpen{
		Type: protocol.TypeStreamOpen, ReqID: reqID, TunnelID: tunnelID,
		Proto: proto, RemoteAddr: remoteAddr, Dest: dest,
	}, protocol.TypeStreamOpen); err != nil {
		s.removeRaw(reqID)
		return nil, err
	}
	return &RawStream{b: b, s: s}, nil
}

// sendStreamData, ziyaretciden gelen baytlari ajana iletir. TCP -> FrameStreamData,
// UDP -> FrameDatagram (her cagri bir datagram).
func (s *Session) sendStreamData(ctx context.Context, b *rawBridge, data []byte) error {
	ft := protocol.FrameStreamData
	if b.proto == protocol.ProtoUDP {
		ft = protocol.FrameDatagram
	}
	return s.SendBinary(ctx, protocol.BodyFrame{FrameType: ft, ReqID: b.reqID, Payload: data})
}

// CloseStream, ajana StreamClose gonderir ve koprüyu temizler.
func (s *Session) CloseStream(reqID uint64, reason string) {
	_ = s.Send(context.Background(), protocol.StreamClose{
		Type: protocol.TypeStreamClose, ReqID: reqID, Reason: reason,
	}, protocol.TypeStreamClose)
	if b, ok := s.getRaw(reqID); ok {
		b.close()
	}
	s.removeRaw(reqID)
}

func (s *Session) getRaw(reqID uint64) (*rawBridge, bool) {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	b, ok := s.rawBridges[reqID]
	return b, ok
}

func (s *Session) removeRaw(reqID uint64) {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	delete(s.rawBridges, reqID)
}

// --- Okuma dongusunden cagrilan yonlendiriciler ---------------------------

func (s *Session) routeStreamAck(ack protocol.StreamAck) {
	if b, ok := s.getRaw(ack.ReqID); ok {
		select {
		case b.accept <- ack:
		default:
		}
	}
}

// routeStreamData, ajandan gelen (yerel->ziyaretci) baytlari koprüye teslim eder.
// Tampon dolarsa akisi kapatir; okuma dongusunu ASLA bloklamaz.
func (s *Session) routeStreamData(reqID uint64, payload []byte) {
	b, ok := s.getRaw(reqID)
	if !ok {
		return
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	select {
	case b.fromLocal <- cp:
	case <-b.closed:
	default:
		b.close() // tampon doldu: ziyaretci cok yavas — akisi kes
	}
}

func (s *Session) routeStreamClose(reqID uint64) {
	if b, ok := s.getRaw(reqID); ok {
		b.close()
	}
	s.removeRaw(reqID)
}

// closeAllRaw, oturum kapaninca tum ham koprüleri serbest birakir.
func (s *Session) closeAllRaw() {
	s.rawMu.Lock()
	bridges := make([]*rawBridge, 0, len(s.rawBridges))
	for _, b := range s.rawBridges {
		bridges = append(bridges, b)
	}
	s.rawBridges = make(map[uint64]*rawBridge)
	s.rawMu.Unlock()
	for _, b := range bridges {
		b.close()
	}
}
