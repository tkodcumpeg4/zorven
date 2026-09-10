// Package tunnel, istemci WSS oturumlarini yonetir.
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	// HeartbeatInterval, sunucunun ping gonderme araligi (api_contract.md §2).
	HeartbeatInterval = 30 * time.Second
	// maxMissedPongs asilirsa baglanti olu sayilir ve kapatilir.
	maxMissedPongs = 2
	// writeTimeout, tek bir yazma isleminin ust siniri.
	writeTimeout = 10 * time.Second

	// MaxPendingExchanges, tek bir oturumda ayni anda bekleyen en fazla istek sayisi.
	// Asiri yuk / DoS durumunda goroutine ve bellek patlamasini (OOM) onler.
	MaxPendingExchanges = 512

	// maxConsecutiveBadFrames, pes pese gelen bozuk kontrol/govde cercevesi siniri.
	// Asilirsa saldiri/bozuk istemci sayilir ve baglanti kapatilir.
	maxConsecutiveBadFrames = 5
)

// Session, bagli tek bir istemciyi temsil eder.
type Session struct {
	ID          string
	ClientID    string
	ClientName  string
	TenantID    string
	Version     string
	Platform    string
	RemoteAddr  string
	ConnectedAt time.Time
	IsService   bool

	metricsMu     sync.RWMutex
	latestMetrics *protocol.Metrics

	conn *websocket.Conn
	log  *slog.Logger

	// coder/websocket ayni anda tek yazar kaldirir; tum yazmalar bu kilitten gecer.
	writeMu sync.Mutex

	mu          sync.Mutex
	lastSeen    time.Time
	missedPongs int
	badFrames   atomic.Int32

	// Ucusta olan istekler. req_id oturum omru boyunca artan bir sayacdir;
	// ayni WSS baglantisi uzerinde es zamanli istekleri ayirt eder
	// (MVP multiplexing'i budur, R1'de yamux'a tasinacak).
	nextReqID atomic.Uint64
	pendingMu sync.Mutex
	pending   map[uint64]*Exchange

	// flowControl, istemcinin kredi tabanli akis kontrolunu destekleyip
	// desteklemedigi (hello el sikismasinda belirlenir). Desteklemiyorsa
	// window_update GONDERILMEZ; eski istemciler kirilmaz.
	flowControl bool

	// creditMu, istek basina biriken kredi iadesini korur.
	creditMu sync.Mutex
	credit   map[uint64]int

	// Uzak terminal aboneleri: session_id -> ciktinin gonderilecegi kanal.
	// Dashboard koprusu (server/api) buraya abone olur.
	termMu   sync.Mutex
	termSubs map[string]chan<- protocol.TerminalOutput
	termExit map[string]chan<- protocol.TerminalExit

	// Uzak ekran aboneleri: session_id -> kare/hata kanallari.
	scrMu   sync.Mutex
	scrSubs map[string]chan<- protocol.ScreenFrame
	scrErr  map[string]chan<- protocol.ScreenError

	// Aktif WebSocket passthrough akislari: req_id -> koprü.
	wsMu      sync.Mutex
	wsBridges map[uint64]*wsBridge
}

func NewSession(id, clientID, clientName, remoteAddr string, conn *websocket.Conn, log *slog.Logger) *Session {
	now := time.Now().UTC()
	return &Session{
		ID:          id,
		ClientID:    clientID,
		ClientName:  clientName,
		RemoteAddr:  remoteAddr,
		ConnectedAt: now,
		conn:        conn,
		log:         log.With("session", id, "client", clientID),
		lastSeen:    now,
		pending:     make(map[uint64]*Exchange),
		credit:      make(map[uint64]int),
		termSubs:    make(map[string]chan<- protocol.TerminalOutput),
		termExit:    make(map[string]chan<- protocol.TerminalExit),
		scrSubs:     make(map[string]chan<- protocol.ScreenFrame),
		scrErr:      make(map[string]chan<- protocol.ScreenError),
	}
}

// LastSeen, en son mesaj alinma zamani.
func (s *Session) LastSeen() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeen
}

// Send, kontrol mesajini JSON metin cercevesi olarak gonderir.
func (s *Session) Send(ctx context.Context, v any, t protocol.MessageType) error {
	data, err := protocol.Marshal(v, t)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageText, data)
}

// windowUpdateThreshold, biriken kredinin hangi noktada gonderilecegi.
//
// Her okunan bayt icin mesaj gondermek kontrol kanalini bogar; pencerenin
// yarisi birikince tek mesaj gondermek hem trafigi dusuk tutar hem de
// istemcinin bos beklememesi icin yeterli krediyi zamaninda saglar.
const windowUpdateThreshold = protocol.InitialWindowBytes / 2

// sendWindowUpdate, tuketilen baytlari biriktirir ve esigi asinca istemciye
// "bu istek icin N bayt daha gonderebilirsin" der.
//
// Ingress goroutine'inden (govde okunurken) cagrilir; Send zaten writeMu ile
// korundugu icin okuma dongusuyle yarismaz.
func (s *Session) sendWindowUpdate(reqID uint64, n int) {
	if !s.flowControl || n <= 0 {
		return
	}
	s.creditMu.Lock()
	s.credit[reqID] += n
	acc := s.credit[reqID]
	if acc < windowUpdateThreshold {
		s.creditMu.Unlock()
		return
	}
	s.credit[reqID] = 0
	s.creditMu.Unlock()

	// Hata yutulur: baglanti kopmussa istek zaten failAllPending ile sonlanir.
	_ = s.Send(context.Background(), protocol.WindowUpdate{
		Type: protocol.TypeWindowUpdate, ReqID: reqID, Increment: acc,
	}, protocol.TypeWindowUpdate)
}

// SendBinary, govde cercevesini gonderir (Phase 2'de kullanilacak).
func (s *Session) SendBinary(ctx context.Context, frame protocol.BodyFrame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageBinary, frame.Encode())
}

// Run, oturumun okuma dongusunu ve heartbeat'ini calistirir.
// Baglanti kapandiginda veya ctx iptal edildiginde doner.
func (s *Session) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Oturum nasil biterse bitsin, ucusta olan istekler asili kalmamali.
	defer s.failAllPending()
	// Ayni sekilde acik WebSocket akislarini da serbest birak.
	defer s.closeAllWS()

	go s.heartbeat(ctx, cancel)

	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			// Normal kapanislari hata olarak raporlama.
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway ||
				errors.Is(err, context.Canceled) {
				s.log.Info("istemci baglantiyi kapatti", "durum", status)
				return nil
			}
			return err
		}

		s.touch()

		switch typ {
		case websocket.MessageText:
			if err := s.handleControl(ctx, data); err != nil {
				s.log.Warn("kontrol mesaji islenemedi", "hata", err)
				if s.badFrames.Add(1) >= maxConsecutiveBadFrames {
					s.log.Warn("ardisik bozuk cerceve siniri asildi, baglanti kapatiliyor", "bad_frames", s.badFrames.Load())
					s.conn.CloseNow()
					return err
				}
			} else {
				s.badFrames.Store(0)
			}
		case websocket.MessageBinary:
			frame, err := protocol.DecodeBodyFrame(data)
			if err != nil {
				s.log.Warn("bozuk govde cercevesi", "hata", err)
				if s.badFrames.Add(1) >= maxConsecutiveBadFrames {
					s.log.Warn("ardisik bozuk cerceve siniri asildi, baglanti kapatiliyor", "bad_frames", s.badFrames.Load())
					s.conn.CloseNow()
					return err
				}
				continue
			}
			s.badFrames.Store(0)
			s.routeBodyFrame(frame)
		}
	}
}

func (s *Session) handleControl(ctx context.Context, data []byte) error {
	t, err := protocol.PeekType(data)
	if err != nil {
		return err
	}

	switch t {
	case protocol.TypePong:
		s.mu.Lock()
		s.missedPongs = 0
		s.mu.Unlock()

		var pong protocol.Pong
		if err := json.Unmarshal(data, &pong); err == nil && pong.Metrics != nil {
			s.SetMetrics(pong.Metrics)
		}

	case protocol.TypeBye:
		var msg protocol.Bye
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		s.log.Info("istemci veda etti", "sebep", msg.Reason)
		return s.conn.Close(websocket.StatusNormalClosure, "bye")

	case protocol.TypeWSAccept:
		var acc protocol.WSAccept
		if err := json.Unmarshal(data, &acc); err != nil {
			return err
		}
		s.routeWSAccept(acc)

	case protocol.TypeWSClose:
		var wc protocol.WSClose
		if err := json.Unmarshal(data, &wc); err != nil {
			return err
		}
		s.routeWSClose(wc.ReqID)

	case protocol.TypeHTTPResponse:
		var msg protocol.HTTPResponse
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		e, ok := s.lookupExchange(msg.ReqID)
		if !ok {
			// Zaman asimina ugramis veya iptal edilmis istek; yok say.
			s.log.Debug("bilinmeyen req_id icin yanit", "req_id", msg.ReqID)
			return nil
		}
		e.deliverHead(ResponseHead{Status: msg.Status, Headers: msg.Headers, HasBody: msg.HasBody})
		if !msg.HasBody {
			e.closeBody(nil)
		}

	case protocol.TypeTerminalOutput:
		var m protocol.TerminalOutput
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.termMu.Lock()
		ch := s.termSubs[m.SessionID]
		s.termMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default: // yavas dashboard ciktiyi bloklamamali; parca dusurulur
			}
		}

	case protocol.TypeTerminalExit:
		var m protocol.TerminalExit
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.termMu.Lock()
		ch := s.termExit[m.SessionID]
		s.termMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}

	case protocol.TypeScreenFrame:
		var m protocol.ScreenFrame
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.scrMu.Lock()
		ch := s.scrSubs[m.SessionID]
		s.scrMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default: // yavas dashboard kareyi bloklamamali; kare dusurulur
			}
		}

	case protocol.TypeScreenError:
		var m protocol.ScreenError
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.scrMu.Lock()
		ch := s.scrErr[m.SessionID]
		s.scrMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}

	case protocol.TypeHTTPError:
		var msg protocol.HTTPError
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		e, ok := s.lookupExchange(msg.ReqID)
		if !ok {
			return nil
		}
		e.fail(msg.Code, msg.Message, errors.New(msg.Message))

	default:
		s.log.Warn("beklenmeyen mesaj tipi", "tip", t)
	}
	return nil
}

// heartbeat, duzenli ping gonderir ve yanitsiz kalan baglantilari kapatir.
func (s *Session) heartbeat(ctx context.Context, cancel context.CancelFunc) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			s.missedPongs++
			missed := s.missedPongs
			s.mu.Unlock()

			if missed > maxMissedPongs {
				s.log.Warn("heartbeat yanitsiz, baglanti kapatiliyor", "kacirilan", missed)
				s.conn.Close(websocket.StatusPolicyViolation, "heartbeat timeout")
				cancel()
				return
			}

			if err := s.Send(ctx, protocol.Ping{Type: protocol.TypePing, TS: time.Now().UTC()}, protocol.TypePing); err != nil {
				s.log.Warn("ping gonderilemedi", "hata", err)
				cancel()
				return
			}
		}
	}
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now().UTC()
	s.mu.Unlock()
}

// Close, oturumu kapatir.
func (s *Session) Close(reason string) error {
	return s.conn.Close(websocket.StatusNormalClosure, reason)
}

// routeBodyFrame, istemciden gelen yanit govdesi parcasini ilgili istege yazar.
func (s *Session) routeBodyFrame(frame protocol.BodyFrame) {
	if frame.FrameType == protocol.FrameWSData {
		// Yukseltilmis WebSocket akisi: yerel -> tarayici ham baytlari.
		s.routeWSData(frame.ReqID, frame.Payload)
		return
	}
	if frame.FrameType != protocol.FrameResponseBody {
		s.log.Warn("istemciden beklenmeyen cerceve tipi", "tip", frame.FrameType)
		return
	}

	e, ok := s.lookupExchange(frame.ReqID)
	if !ok {
		return // zaman asimina ugramis istek
	}

	if len(frame.Payload) > 0 {
		if err := e.writeBody(frame.Payload); err != nil {
			if errors.Is(err, ErrFlowControlViolation) {
				// Istemci acik penceresinden fazlasini gonderdi. YALNIZCA bu
				// istegi dusuruyoruz; oturum ve diger istekler ayakta kalir.
				// (Eskiden burada bloklanirdik ve TUM oturum dururdu.)
				s.log.Warn("akis kontrolu ihlali; istek dusuruldu", "req_id", frame.ReqID)
				e.fail(protocol.CodeClientOffline, "akis kontrolu penceresi asildi", err)
				return
			}
			// Okuyan taraf gitmis (tarayici baglantiyi kesmis olabilir).
			s.log.Debug("govde yazilamadi", "req_id", frame.ReqID, "hata", err)
			return
		}
	}
	if frame.EOF {
		e.closeBody(nil)
	}
}

// SendRequest, istegi istemciye gonderir ve yeni exchange'i doner.
// Cagiran, is bitince s.FinishExchange(e.reqID) cagirmak ZORUNDADIR.
func (s *Session) SendRequest(ctx context.Context, req protocol.HTTPRequest) (*Exchange, error) {
	e, err := s.newExchange()
	if err != nil {
		return nil, err
	}
	req.ReqID = e.reqID
	req.Type = protocol.TypeHTTPRequest
	if err := s.Send(ctx, req, protocol.TypeHTTPRequest); err != nil {
		s.FinishExchange(e.reqID)
		return nil, err
	}
	return e, nil
}

// ErrBodyTooLarge, govde protocol.MaxBodyBytes sinirini astiginda doner.
var ErrBodyTooLarge = errors.New("govde sinir asimi")

// StreamRequestBody, istek govdesini parca parca binary cerceve olarak istemciye aktarir.
func (s *Session) StreamRequestBody(ctx context.Context, reqID uint64, body io.ReadCloser) error {
	defer body.Close()

	limited := io.LimitReader(body, protocol.MaxBodyBytes+1)
	buf := make([]byte, protocol.BodyChunkSize)
	var seq uint32
	var total int64

	for {
		n, readErr := limited.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > protocol.MaxBodyBytes {
				return ErrBodyTooLarge
			}
			frame := protocol.BodyFrame{
				FrameType: protocol.FrameRequestBody,
				ReqID:     reqID,
				Seq:       seq,
				EOF:       false,
				Payload:   buf[:n],
			}
			if err := s.SendBinary(ctx, frame); err != nil {
				return err
			}
			seq++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	return s.SendBinary(ctx, protocol.BodyFrame{
		FrameType: protocol.FrameRequestBody,
		ReqID:     reqID,
		Seq:       seq,
		EOF:       true,
	})
}

// --- Uzak terminal koprusu -------------------------------------------------

// OpenTerminal, istemcide kabuk acar ve cikti/exit kanallarina abone olur.
// Cagiran, is bitince CloseTerminal cagirmalidir.
func (s *Session) OpenTerminal(ctx context.Context, sessionID string, cols, rows uint16,
	out chan<- protocol.TerminalOutput, exit chan<- protocol.TerminalExit) error {

	s.termMu.Lock()
	s.termSubs[sessionID] = out
	s.termExit[sessionID] = exit
	s.termMu.Unlock()

	return s.Send(ctx, protocol.TerminalOpen{
		Type:      protocol.TypeTerminalOpen,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}, protocol.TypeTerminalOpen)
}

// TerminalInput, kullanicinin tus vuruslarini istemciye iletir.
func (s *Session) TerminalInput(ctx context.Context, sessionID string, data []byte) error {
	return s.Send(ctx, protocol.TerminalInput{
		Type:      protocol.TypeTerminalInput,
		SessionID: sessionID,
		Data:      data,
	}, protocol.TypeTerminalInput)
}

// TerminalResize, pencere boyutunu istemciye bildirir.
func (s *Session) TerminalResize(ctx context.Context, sessionID string, cols, rows uint16) error {
	return s.Send(ctx, protocol.TerminalResize{
		Type:      protocol.TypeTerminalResize,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}, protocol.TypeTerminalResize)
}

// CloseTerminal, oturumu kapatir ve abonelikleri temizler.
func (s *Session) CloseTerminal(ctx context.Context, sessionID string) {
	s.termMu.Lock()
	delete(s.termSubs, sessionID)
	delete(s.termExit, sessionID)
	s.termMu.Unlock()

	_ = s.Send(ctx, protocol.TerminalClose{
		Type:      protocol.TypeTerminalClose,
		SessionID: sessionID,
	}, protocol.TypeTerminalClose)
}

// --- Uzak ekran koprusu ----------------------------------------------------

// OpenScreen, istemcide ekran yakalamayi baslatir ve kare/hata kanallarina
// abone olur. Cagiran, is bitince CloseScreen cagirmalidir.
func (s *Session) OpenScreen(ctx context.Context, sessionID string, fps, quality, maxWidth int, mode string,
	frames chan<- protocol.ScreenFrame, errs chan<- protocol.ScreenError) error {

	s.scrMu.Lock()
	s.scrSubs[sessionID] = frames
	s.scrErr[sessionID] = errs
	s.scrMu.Unlock()

	return s.Send(ctx, protocol.ScreenOpen{
		Type:      protocol.TypeScreenOpen,
		SessionID: sessionID,
		FPS:       fps,
		Quality:   quality,
		MaxWidth:  maxWidth,
		Mode:      mode,
	}, protocol.TypeScreenOpen)
}

// ScreenInput, fare/klavye olayini istemciye iletir.
func (s *Session) ScreenInput(ctx context.Context, in protocol.ScreenInput) error {
	in.Type = protocol.TypeScreenInput
	return s.Send(ctx, in, protocol.TypeScreenInput)
}

// CloseScreen, oturumu kapatir ve abonelikleri temizler.
func (s *Session) CloseScreen(ctx context.Context, sessionID string) {
	s.scrMu.Lock()
	delete(s.scrSubs, sessionID)
	delete(s.scrErr, sessionID)
	s.scrMu.Unlock()

	_ = s.Send(ctx, protocol.ScreenClose{
		Type:      protocol.TypeScreenClose,
		SessionID: sessionID,
	}, protocol.TypeScreenClose)
}

// SetMetrics, istemcinin son bildirdigi metrikleri atomik kaydeder.
func (s *Session) SetMetrics(m *protocol.Metrics) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.latestMetrics = m
}

// Metrics, istemcinin son gonderdigi metrikleri doner.
func (s *Session) Metrics() *protocol.Metrics {
	s.metricsMu.RLock()
	defer s.metricsMu.RUnlock()
	return s.latestMetrics
}
