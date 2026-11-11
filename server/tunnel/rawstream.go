package tunnel

import (
	"context"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// rawBridge, tek bir ham TCP/UDP tunel akisini temsil eder (FAZ 3 / D2).
// Ingress goroutine'i bunu tutar: accept ile baglanti sonucunu bekler,
// FromLocal'dan yerel servisten gelen baytlari/datagrami okuyup ziyaretciye yazar.
type rawBridge struct {
	reqID     uint64
	tunnelID  string
	proto     string // tcp | udp — gonderilecek cerceve tipini belirler
	offerHalf bool   // StreamOpen'da yari kapama onerildi
	accept    chan protocol.StreamAck
	q         *streamQueue // ajan -> ziyaretci
	send      creditGate   // ziyaretci -> ajan
	half      bool         // ajan yari kapamayi kabul etti (ack'ten once yazilir, kanalla yayinlanir)
	acked     bool         // yalniz okuma dongusu: ilk ack islendi
}

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

func (r *RawStream) ReqID() uint64                     { return r.b.reqID }
func (r *RawStream) Accept() <-chan protocol.StreamAck { return r.b.accept }

// FromLocal, yerel hedeften gelen baytlar. Kanal tamponsuzdur; duzgun
// kapanista Closed() yalniz tum veri buradan alindiktan sonra kapanir.
func (r *RawStream) FromLocal() <-chan []byte { return r.b.q.out }

// Closed, akis bittiginde (karsi taraf kapatip veri bosaldiginda ya da iptal) kapanir.
func (r *RawStream) Closed() <-chan struct{} { return r.b.q.closed }

// Aborted, akis IPTAL edildiginde (tasma, sunucu tarafli kapama, oturum sonu)
// kapanir. Tuketici bloklu ziyaretci yazisini baglantiyi kapatarak cozmeli.
func (r *RawStream) Aborted() <-chan struct{} { return r.b.q.aborted }

// RemoteEOF, yari kapama pazarlandiysa ajanin yazmayi bitirdigini (yerel hedef
// EOF) bildirir; o ana kadarki tum veri FromLocal'dan teslim edilmistir.
// Pazarlanmadiysa hic kapanmaz.
func (r *RawStream) RemoteEOF() <-chan struct{} { return r.b.q.eof }

// HalfClose, ajanin yari kapamayi kabul edip etmedigi (Accept'ten sonra gecerli).
func (r *RawStream) HalfClose() bool { return r.b.half }

// Send, ziyaretciden gelen veriyi ajana iletir. Kredi pazarlandiysa pencere
// bitince bekler (gercek geri basinc); akis kapaninca ErrStreamClosed doner.
func (r *RawStream) Send(ctx context.Context, d []byte) error {
	if r.b.proto != protocol.ProtoUDP {
		if err := r.b.send.acquire(ctx, r.b.q.closed, creditCost(len(d))); err != nil {
			return err
		}
	}
	return r.s.sendStreamData(ctx, r.b, d)
}

// CloseWrite, ziyaretci yazmayi bitirdi (okumada EOF). Yari kapama
// pazarlandiysa ajana EOF isareti gider ve akis acik kalir (true doner).
// Pazarlanmadiysa (eski ajan) StreamClose gonderilir: eski ajan yerel hedefe
// CloseWrite yapip kisa bir sure (3 sn) yanitin akmasina izin verir; bu surede
// gelen veri hala teslim edilsin diye kopru KALDIRILMAZ (false doner, cagiran
// kendi bekleme suresini kurar).
func (r *RawStream) CloseWrite() bool {
	if r.b.half {
		_ = r.s.SendBinary(context.Background(), protocol.BodyFrame{
			FrameType: protocol.FrameStreamData, ReqID: r.b.reqID, EOF: true,
		})
		return true
	}
	_ = r.s.Send(context.Background(), protocol.StreamClose{
		Type: protocol.TypeStreamClose, ReqID: r.b.reqID, Reason: "visitor closed write",
	}, protocol.TypeStreamClose)
	return false
}

// Close, akisi iptal eder (ajana StreamClose gider, bekleyen veri atilir).
func (r *RawStream) Close(reason string) { r.s.closeRawBridge(r.b, reason) }

// OpenStream, ingress tarafindan cagrilir: yeni ham akis tahsis eder, ajana
// StreamOpen gonderir ve tutamaci doner. Cagiran Accept()'ten sonucu beklemeli.
func (s *Session) OpenStream(ctx context.Context, tunnelID, proto, remoteAddr string) (*RawStream, error) {
	return s.openStream(ctx, tunnelID, proto, remoteAddr, "", false)
}

// OpenStreamHalf, OpenStream gibidir; ek olarak tcp'de yari kapama onerir.
// Yalniz RemoteEOF/CloseWrite'i isleyen tuketici (rawproxy) kullanmali: aksi
// halde yerel hedef kapaninca tuketici bunu hic ogrenmez.
func (s *Session) OpenStreamHalf(ctx context.Context, tunnelID, proto, remoteAddr string) (*RawStream, error) {
	return s.openStream(ctx, tunnelID, proto, remoteAddr, "", proto == protocol.ProtoTCP)
}

// OpenStreamTo, OpenStream gibidir; ek olarak alt ag yonlendirmesi (F20) icin
// hedefi tasir. Hedefin araliga uygunlugunu ajan da ayrica dogrular.
func (s *Session) OpenStreamTo(ctx context.Context, tunnelID, proto, remoteAddr, dest string) (*RawStream, error) {
	return s.openStream(ctx, tunnelID, proto, remoteAddr, dest, false)
}

func (s *Session) openStream(ctx context.Context, tunnelID, proto, remoteAddr, dest string, half bool) (*RawStream, error) {
	reqID := s.nextReqID.Add(1)
	var q *streamQueue
	var caps []string
	if proto == protocol.ProtoUDP {
		q = newStreamQueue(&s.streamMem, udpMaxMem, udpMaxItems)
	} else {
		// Kredi her tcp tuketicisinde guvenlidir: pompa krediyi tuketici veriyi
		// ALDIGINDA iade eder, tuketicinin ozel bir sey yapmasi gerekmez.
		q = newStreamQueue(&s.streamMem, rawLegacyMaxMem, 0)
		caps = []string{protocol.StreamCapCredit}
		if half {
			caps = append(caps, protocol.StreamCapHalfClose)
		}
	}
	q.grant = func(n int) { s.sendStreamWindow(reqID, n) }
	b := &rawBridge{
		reqID:     reqID,
		tunnelID:  tunnelID,
		proto:     proto,
		offerHalf: half,
		accept:    make(chan protocol.StreamAck, 1),
		q:         q,
	}
	s.rawMu.Lock()
	s.ensureRawMap()
	s.rawBridges[reqID] = b
	s.rawMu.Unlock()

	if err := s.Send(ctx, protocol.StreamOpen{
		Type: protocol.TypeStreamOpen, ReqID: reqID, TunnelID: tunnelID,
		Proto: proto, RemoteAddr: remoteAddr, Dest: dest, Caps: caps,
	}, protocol.TypeStreamOpen); err != nil {
		s.removeRaw(reqID)
		q.abort()
		return nil, err
	}
	return &RawStream{b: b, s: s}, nil
}

// sendStreamWindow, ajana bu akis icin kredi iade eder (ajan -> ziyaretci yonu).
// HTTP'deki sendWindowUpdate'ten farki: oturum duzeyindeki flow_control
// yetenegine degil akis basina pazarliga baglidir ve esik birikimi kuyrukta yapilir.
func (s *Session) sendStreamWindow(reqID uint64, n int) {
	_ = s.Send(context.Background(), protocol.WindowUpdate{
		Type: protocol.TypeWindowUpdate, ReqID: reqID, Increment: n,
	}, protocol.TypeWindowUpdate)
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

// CloseStream, ajana StreamClose gonderir ve koprüyu iptal edip temizler.
func (s *Session) CloseStream(reqID uint64, reason string) {
	if b, ok := s.getRaw(reqID); ok {
		s.closeRawBridge(b, reason)
		return
	}
	_ = s.Send(context.Background(), protocol.StreamClose{
		Type: protocol.TypeStreamClose, ReqID: reqID, Reason: reason,
	}, protocol.TypeStreamClose)
}

// closeRawBridge, kopruyu (haritadan kaldirilmis olsa bile) iptal eder. Tutamac
// uzerinden cagrilir ki ajan akisi once kapatmis olsa da pompa goroutine'i sizmasin.
func (s *Session) closeRawBridge(b *rawBridge, reason string) {
	_ = s.Send(context.Background(), protocol.StreamClose{
		Type: protocol.TypeStreamClose, ReqID: b.reqID, Reason: reason,
	}, protocol.TypeStreamClose)
	b.q.abort()
	s.rawMu.Lock()
	if s.rawBridges[b.reqID] == b {
		delete(s.rawBridges, b.reqID)
	}
	s.rawMu.Unlock()
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
	b, ok := s.getRaw(ack.ReqID)
	if !ok || b.acked {
		return // ikinci ack: yok say (mod bayraklari tuketici okurken degismesin)
	}
	b.acked = true
	if ack.Code == "" && b.proto == protocol.ProtoTCP {
		// Mod bayraklari ack kanala yazilmadan ONCE ayarlanir: tuketici ack'i
		// aldiginda (kanal happens-before) dogru modu gorur. Ajan veriyi ack'ten
		// sonra yollar ve ayni okuma dongusunden gelir, kuyruk da dogru modda baslar.
		if protocol.HasCap(ack.Caps, protocol.StreamCapCredit) {
			b.q.credit.Store(true)
			b.send.enable()
		}
		b.half = b.offerHalf && protocol.HasCap(ack.Caps, protocol.StreamCapHalfClose)
	}
	select {
	case b.accept <- ack:
	default:
	}
}

// routeStreamData, ajandan gelen (yerel->ziyaretci) baytlari koprüye teslim eder.
// Kuyruk tavani/kredi asilirsa akisi kapatir; okuma dongusunu ASLA bloklamaz.
func (s *Session) routeStreamData(reqID uint64, payload []byte) {
	b, ok := s.getRaw(reqID)
	if !ok {
		return
	}
	if len(payload) == 0 {
		// Yari kapama isareti (yalniz pazarlandiysa anlamli; eski ajan bos cerceve yollamaz).
		if b.half {
			b.q.pushEOF()
		}
		return
	}
	if err := b.q.push(payload); err != nil {
		if b.proto == protocol.ProtoUDP {
			s.UDPProbes().AddDropped(b.tunnelID, 1) // kuyruk tasmasi: datagram dusuruldu
		}
		s.logStreamAbort(b.proto, reqID, b.tunnelID, err)
		// Iptal ve ajana bildirim ayri goroutine'de: Send yazim kilidini bekleyebilir,
		// okuma dongusu bloklanmamali. Iptal kuyrugu hemen bosaltir.
		b.q.abort()
		go s.closeRawBridge(b, "overflow")
	}
}

// routeStreamClose, ajan akisi kapatti: kalan veri ziyaretciye teslim edildikten
// sonra Closed() kapanir (son baytlar kaybolmaz).
func (s *Session) routeStreamClose(reqID uint64) {
	if b, ok := s.getRaw(reqID); ok {
		b.q.remoteClose()
	}
	s.removeRaw(reqID)
}

// routeStreamWindow, ajandan gelen kredi iadesi (ziyaretci -> yerel hedef yonu).
// Ham akis ya da WS akisi olabilir (req_id oturum icinde tekil).
func (s *Session) routeStreamWindow(reqID uint64, n int) {
	if b, ok := s.getRaw(reqID); ok {
		b.send.add(n)
		return
	}
	if b, ok := s.getWS(reqID); ok {
		b.send.add(n)
	}
}

// StreamMemBytes, oturumun ham/WS akis kuyruklarinda bekleyen toplam bellek (gozlem/test).
func (s *Session) StreamMemBytes() int64 { return s.streamMem.Load() }

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
		b.q.abort()
	}
}
