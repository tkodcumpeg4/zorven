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
	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/server/obs"
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

	// maxFrameLogs, oturum basina yazilan bozuk/beklenmeyen cerceve WARN siniri.
	// Beklenmeyen tipler ardisik-hata sayacini sifirladigi icin oturum kapanmaz;
	// her cerceveye bir satir yazmak 300 cercevede 300 satir log seli demekti.
	// Fazlasi sayilir, oturum sonunda tek ozet satiri yazilir.
	maxFrameLogs = 10
)

// Donmus ajan tespiti (O15). Bekleyen bir HTTP istegi varken ajandan stallAfter
// boyunca hicbir cerceve gelmediyse araya erken bir ping girer; ping'den sonra
// stallGrace icinde de hicbir sey gelmezse oturum olu sayilip kapatilir ve
// bekleyen istekler hemen hata alir (ziyaretci 30 sn bekleyip 504 almaz, ajan
// yeniden baglaninca trafik doner). Bosta oturumlarda davranis ayni kalir
// (HeartbeatInterval x maxMissedPongs): kimse beklemiyorken yavas/mobil agda
// yanlis pozitif riskine girmeye gerek yok.
//
// Neden yanlis pozitif dusuk: herhangi bir cerceve (govde parcasi, pong, WS
// verisi) canlilik sayilir; yavas ama canli bir ag 10 sn'de en az pong'u
// ulastirir. Tum surumler ping'e pong doner (eski istemciler dahil), yeni
// mesaj ya da alan yok: tel protokolu degismez.
var (
	stallCheckEvery = 2 * time.Second
	stallAfter      = 10 * time.Second
	stallGrace      = 10 * time.Second
)

// Session, bagli tek bir istemciyi temsil eder.
type Session struct {
	ID         string
	ClientID   string
	ClientName string
	TenantID   string
	// UserID: istemcinin sahibi kullanici (varsa); ag paylasimlarinda cihaz, sahibine
	// acilan paylasimlari da gorur.
	UserID      string
	Version     string
	Platform    string
	RemoteAddr  string
	ConnectedAt time.Time
	IsService   bool
	AppKind     string // "desktop" | "" (hello.app_kind)

	// Shells / DefaultShell, ajanin hello ile bildirdigi uzak terminal kabuklari.
	// Eski istemcilerde bos (kabuk secimi sunulmaz). El sikismada bir kez yazilir.
	Shells       []protocol.ShellInfo
	DefaultShell string

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

	// frameLogs / frameLogsDropped: warnFrame log siniri (maxFrameLogs).
	frameLogs        atomic.Int32
	frameLogsDropped atomic.Int64

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

	// httpTrailers, ajan http_trailers (yanit trailer'i / gRPC) yetenegini
	// pazarladiysa true (el sikismada yazilir, sonra yalniz okunur).
	httpTrailers bool

	// screenInputStatus, istemci screen_input_status gonderebiliyorsa true
	// (el sikismada yazilir, sonra yalniz okunur).
	screenInputStatus bool

	// screen2, istemci screen2 (ikili karo) modunu destekliyorsa true
	// (el sikismada yazilir, sonra yalniz okunur).
	screen2 bool
	// screen2Layers, istemci katmanli screen2'yi (karo hizali, izleyici basina
	// ayar) pazarladiysa true.
	screen2Layers bool

	// screenCursor / screenClipboard, istemci imlec katmani / pano yetenegini
	// pazarladiysa true (el sikismada yazilir, sonra yalniz okunur).
	screenCursor    bool
	screenClipboard bool
	// screen2Stats, istemci saniyelik canli olcum ozetini pazarladiysa true.
	screen2Stats bool
	// screen2Feedback, istemci izleyici teslim geri bildirimini kabul ediyorsa true.
	screen2Feedback bool
	// screen2Video, istemci H.264 video modunu (FrameVideo) pazarladiysa true.
	screen2Video bool
	// screen2FPS60, istemci 30 fps ustu yakalamayi (60'a kadar) pazarladiysa true.
	screen2FPS60 bool
	// screenFiles, istemci ekran oturumu dosya aktarimini pazarladiysa true.
	screenFiles bool

	// legacyBodyStall: ajan govde okuma hatasini bildirmeyen eski surum (<0.3.8);
	// el sikismada yazilir, sonra yalniz okunur (legacybody.go).
	legacyBodyStall bool

	// features, el sikismada pazarlanan (hello_ack ile yankilanan) yetenekler.
	features []string

	// deviceUpdate: istemci device_update'i ilan etti ve sunucu onayladi;
	// onDeviceUpdate cihaz kaydini gunceller (Handler kurar). lastDevUpd, hiz siniri.
	deviceUpdate   bool
	onDeviceUpdate func(ctx context.Context, upd protocol.DeviceUpdate)
	lastDevUpd     time.Time
	sendHook       func(ctx context.Context, v any, t protocol.MessageType) error // testler
	// Dosya aktarimlari (files.go); fileMu altinda, tembel olusturulur.
	fileMu   sync.Mutex
	files    map[uint64]*srvXfer
	fileList map[uint64]chan protocol.FileListResult
	fileSeq  atomic.Uint64

	// creditMu, istek basina biriken kredi iadesini korur.
	creditMu sync.Mutex
	credit   map[uint64]int
	// reqCreditMode, ajanin istek govdesi kredisini (body_credit) destekleyip
	// desteklemedigi: reqCreditUnknown/Yes/No (reqcredit.go).
	reqCreditMode atomic.Int32

	// Uzak terminal aboneleri: session_id -> ciktinin gonderilecegi kanal.
	// Dashboard koprusu (server/api) buraya abone olur.
	termMu   sync.Mutex
	termSubs map[string]chan<- protocol.TerminalOutput
	termExit map[string]chan<- protocol.TerminalExit

	// Uzak ekran aboneleri: session_id -> kare/hata kanallari.
	scrMu   sync.Mutex
	scrSubs map[string]chan<- protocol.ScreenFrame
	scrErr  map[string]chan<- protocol.ScreenError
	scrInp  map[string]chan<- protocol.ScreenInputStatus
	// screen2 akislari: stream id -> abone; session_id -> stream id.
	scr2    map[uint64]*screen2Sub
	scr2Ids map[string]uint64
	// imlec/pano mesajlari: session_id -> abone (bkz. screen_aux.go).
	scrAux map[string]chan<- ScreenAux

	// Aktif WebSocket passthrough akislari: req_id -> koprü.
	wsMu      sync.Mutex
	wsBridges map[uint64]*wsBridge

	// Aktif ham TCP/UDP tunel akislari: req_id -> koprü (FAZ 3 / D2).
	rawMu      sync.Mutex
	rawBridges map[uint64]*rawBridge
	// streamMem, ham/WS akis kuyruklarinda bekleyen toplam bellek (streamq.go).
	streamMem atomic.Int64

	// UDP tunel olcumu (FeatureUDPProbe; bkz. udpprobe.go). udpProbe yalnizca
	// el sikismada yazilir.
	udpProbe  bool
	probes    *ProbeTracker
	probeOnce sync.Once

	// Zorven Net (FeatureWGNet + FeatureWGRelay; bkz. net.go). netMgr yalnizca
	// el sikismada yazilir; netConn ilk net_register ile kurulur.
	netMgr  *netctl.Manager
	netMu   sync.Mutex
	netConn *netctl.NodeConn
	// netPunch: wg_punch (W2 dogrudan yol) pazarlandi.
	netPunch bool
	// netTun: wg_tun (W3 Mod B) pazarlandi.
	netTun bool
	// netALog: wg_alog (Z5 erisim gunlugu) pazarlandi.
	netALog bool
}

// HTTPTrailers, ajanin yanit trailer'larini (gRPC) tasiyabildigini soyler
// (FeatureHTTPTrailers pazarlandi). Eski ajanlarda false.
func (s *Session) HTTPTrailers() bool { return s.httpTrailers }

// Features, el sikismada pazarlanan yeteneklerin kopyasi.
func (s *Session) Features() []string { return append([]string{}, s.features...) }

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
		scrInp:      make(map[string]chan<- protocol.ScreenInputStatus),
		scr2:        make(map[uint64]*screen2Sub),
		scrAux:      make(map[string]chan<- ScreenAux),
		scr2Ids:     make(map[string]uint64),
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
	if s.sendHook != nil { // yalniz testler
		return s.sendHook(ctx, v, t)
	}
	data, err := protocol.Marshal(v, t)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	ctx, cancel, err := writeCtx(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageText, data)
}

// writeCtx, ajan baglantisina yazim icin baglam uretir. Cagiranin baglami
// (tarayici istegi, aktarim) YAZIM SIRASINDA iptal edilirse coder/websocket
// TUM baglantiyi kapatir; bu da tek bir istegin iptaliyle ajanin oturumunu
// dusurur. Bu yuzden zaten iptal edilmisse yazmadan doner, aksi halde iptali
// yazima tasimaz (yalniz writeTimeout uygulanir).
func writeCtx(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	return c, cancel, nil
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

	ctx, cancel, err := writeCtx(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	return s.conn.Write(ctx, websocket.MessageBinary, frame.Encode())
}

// Run, oturumun okuma dongusunu ve heartbeat'ini calistirir.
// Baglanti kapandiginda veya ctx iptal edildiginde doner.
func (s *Session) Run(ctx context.Context) (err error) {
	// Panik sonrasi temizlik bekcisi: en son defer, tum kapanis yolu bitince durur.
	var stopWatchdog func()
	defer func() {
		if stopWatchdog != nil {
			stopWatchdog()
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Oturum nasil biterse bitsin, ucusta olan istekler asili kalmamali.
	defer s.failAllPending()
	// Ayni sekilde acik WebSocket akislarini da serbest birak.
	defer s.closeAllWS()
	defer s.closeAllRaw()
	defer s.closeAllFiles()
	defer s.closeNet()
	defer s.flushFrameLogs()
	// B2: okuma dongusunde (kontrol isleyicileri dahil) bir panik eskiden HTTP
	// isleyici goroutine'inde kalsa da sayilmiyor, yalniz stderr'e dusuyordu.
	// Recover EN SON ertelenir ki ILK calissin: paniği durdurur, baglantiyi
	// kapatir; ardindan yukaridaki normal kapanis defer'lari (bekleyenler, akislar,
	// bellek sayaclari) panik olmadan calisir. Panikleyen goroutine bir kilidi
	// tutuyor idiyse temizlik orada takilir: bekci sureci kontrollu sonlandirir.
	defer obs.Recover(s.log, "tunnel.session", func(any) {
		err = errSessionPanic
		stopWatchdog = obs.PanicWatchdog(s.log, "tunnel.session")
		s.closeNowSafe()
	})

	s.goSafe("tunnel.heartbeat", cancel, func() { s.heartbeat(ctx, cancel) })

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
				s.warnFrame("kontrol mesaji islenemedi", "hata", err)
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
				s.warnFrame("bozuk govde cercevesi", "hata", err)
				if s.badFrames.Add(1) >= maxConsecutiveBadFrames {
					s.log.Warn("ardisik bozuk cerceve siniri asildi, baglanti kapatiliyor", "bad_frames", s.badFrames.Load())
					s.conn.CloseNow()
					return err
				}
				continue
			}
			s.badFrames.Store(0)
			if frame.FrameType == protocol.FrameScreen {
				s.routeScreen2(data)
				continue
			}
			if frame.FrameType == protocol.FrameVideo {
				s.routeScreen2Video(data)
				continue
			}
			if frame.FrameType == protocol.FrameFile {
				s.routeFileData(frame)
				continue
			}
			if frame.FrameType == protocol.FrameWG {
				s.routeNetFrame(frame)
				continue
			}
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

	case protocol.TypeUDPProbeEcho:
		now := time.Now()
		var m protocol.UDPProbeEcho
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.handleUDPProbeEcho(m, now)

	case protocol.TypePostureReport, protocol.TypeDeviceCertRequest:
		return nil // acik surumde durus/cihaz sertifikasi pazarlanmaz: yok say

	case protocol.TypeDeviceUpdate:
		// Yalniz pazarlandiysa; hiz siniri (en fazla 5 sn'de bir) yazma yukunu sinirlar.
		if !s.deviceUpdate || s.onDeviceUpdate == nil {
			return nil
		}
		var m protocol.DeviceUpdate
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.mu.Lock()
		tooSoon := time.Since(s.lastDevUpd) < 5*time.Second
		if !tooSoon {
			s.lastDevUpd = time.Now()
		}
		s.mu.Unlock()
		if tooSoon {
			return nil
		}
		s.onDeviceUpdate(ctx, sanitizeDeviceUpdate(m))

	case protocol.TypeUDPAgentStats:
		var m protocol.UDPAgentStats
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.handleUDPAgentStats(m)

	case protocol.TypeNetRegister, protocol.TypeNetResync, protocol.TypeNetShareCreate, protocol.TypeNetShareDelete,
		protocol.TypeNetEndpoints, protocol.TypeNetPunchReq, protocol.TypeNetStatus, protocol.TypeNetMode, protocol.TypeNetAccessLog:
		return s.handleNet(ctx, t, data)

	case protocol.TypeBye:
		var msg protocol.Bye
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		s.log.Info("istemci veda etti", "neden", msg.Reason)
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

	case protocol.TypeStreamAck:
		var ack protocol.StreamAck
		if err := json.Unmarshal(data, &ack); err != nil {
			return err
		}
		s.routeStreamAck(ack)

	case protocol.TypeStreamClose:
		var sc protocol.StreamClose
		if err := json.Unmarshal(data, &sc); err != nil {
			return err
		}
		s.routeStreamClose(sc.ReqID)

	case protocol.TypeWindowUpdate:
		// Ham/WS akisinda kredi iadesi (akis basina pazarlik; bkz. protocol/streamflow.go).
		// Pazarlanmamis akis icin gelirse creditGate kapali oldugundan yok sayilir.
		var wu protocol.WindowUpdate
		if err := json.Unmarshal(data, &wu); err != nil {
			return err
		}
		// Once HTTP istek govdesi kredisi (body_credit), degilse ham/WS akisi.
		if !s.routeReqCredit(wu.ReqID, wu.Increment) {
			s.routeStreamWindow(wu.ReqID, wu.Increment)
		}

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
		s.noteReqHead(e)
		if msg.HasBody && s.legacyBodyStall {
			// B5: eski ajan govde hatasini bildirmez; bkz. legacybody.go.
			if cl := declaredLength(msg.Headers); cl > 0 {
				e.stallCL = cl
			}
		}
		e.deliverHead(ResponseHead{Status: msg.Status, Headers: msg.Headers, HasBody: msg.HasBody})
		if !msg.HasBody {
			e.closeBody(nil)
		}

	case protocol.TypeHTTPTrailers:
		var msg protocol.HTTPTrailers
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		// Yalniz yetenek pazarlandiysa kabul edilir (pazarlanmadiysa yok sayilir).
		if !s.httpTrailers {
			return nil
		}
		if e, ok := s.lookupExchange(msg.ReqID); ok {
			e.setTrailers(msg.Trailers)
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
		errCh := s.scrErr[m.SessionID]
		s.scrMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
				// Kuyruk dolu: yavas dashboard okuma dongusunu bloklamamali.
				//
				// MJPEG'de kareyi DUSURMEK dogrudur — her kare bagimsizdir,
				// bir sonraki zaten tam goruntuyu tasir.
				//
				// H.264'te DUSURULEMEZ: Data, fragmented-MP4 akisinin sirali
				// bir parcasidir. Bir parca eksilirse MSE SourceBuffer bunu
				// KURTARAMAZ; goruntu donar veya bozulur ve akis bir daha
				// duzelmez. Sessizce bozuk bir goruntu gostermektense oturumu
				// acik bir hatayla kapatiyoruz; dashboard yeniden baglanir.
				if m.Codec == "h264" && errCh != nil {
					select {
					case errCh <- protocol.ScreenError{
						Type:      protocol.TypeScreenError,
						SessionID: m.SessionID,
						Message:   "video akisi yetisemedi (ag yavas); yeniden baglanin veya daha dusuk cozunurluk secin",
					}:
					default:
					}
				}
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

	case protocol.TypeScreenInputStatus:
		var m protocol.ScreenInputStatus
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		s.scrMu.Lock()
		ch := s.scrInp[m.SessionID]
		s.scrMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}

	case protocol.TypeScreenCursor, protocol.TypeScreenCursorShape, protocol.TypeScreenClipboard, protocol.TypeScreen2Stats:
		// Bozuk/asiri buyuk mesaj okuma dongusunu dusurmez; yalnizca atilir.
		s.routeScreenAux(t, data)

	case protocol.TypeFileOpenAck, protocol.TypeFileAck, protocol.TypeFileEnd,
		protocol.TypeFileDone, protocol.TypeFileAbort, protocol.TypeFileListResult:
		// Bozuk/beklenmeyen dosya mesaji okuma dongusunu dusurmez; atilir.
		s.routeFileCtl(t, data)

	case protocol.TypeHTTPError:
		var msg protocol.HTTPError
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		e, ok := s.lookupExchange(msg.ReqID)
		if !ok {
			return nil
		}
		s.noteReqHead(e)
		e.fail(msg.Code, msg.Message, errors.New(msg.Message))

	default:
		s.warnFrame("beklenmeyen mesaj tipi", "tip", t)
	}
	return nil
}

// heartbeat, duzenli ping gonderir ve yanitsiz kalan baglantilari kapatir.
func (s *Session) heartbeat(ctx context.Context, cancel context.CancelFunc) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	// Esikler bir kez okunur (testler paket degiskenlerini degistirir).
	every, after, grace := stallParams()
	stall := time.NewTicker(every)
	defer stall.Stop()
	var probeAt time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-stall.C:
			var probe, dead bool
			probeAt, probe, dead = s.stallStep(now, probeAt, after, grace)
			if dead {
				s.log.Warn("ajan yanit vermiyor (bekleyen istek var), baglanti kapatiliyor",
					"bekleyen", s.PendingCount(), "sessiz_sn", int(now.Sub(s.LastSeen()).Seconds()))
				// CloseNow: donmus es kapanis cercevesine yanit vermez; Close burada
				// saniyelerce beklerdi. Run'in Read'i hata alir, bekleyenler hemen dusurulur.
				s.conn.CloseNow()
				cancel()
				return
			}
			if probe {
				// Ayri goroutine: yazma kilidi/tamponu doluysa (donmus es) Send
				// writeTimeout'a kadar bloklayabilir; tespit saati bundan etkilenmemeli.
				s.goSafe("tunnel.ping", nil, func() {
					_ = s.Send(ctx, protocol.Ping{Type: protocol.TypePing, TS: time.Now().UTC()}, protocol.TypePing)
				})
			}
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

// stallStep, donmus ajan tespitinin tek adimi (bkz. stallAfter). probeAt sifir
// degilse erken ping o anda gonderilmistir. Donus: yeni probeAt, simdi ping
// gonderilmeli mi, oturum olu mu.
func (s *Session) stallStep(now, probeAt time.Time, after, grace time.Duration) (time.Time, bool, bool) {
	last := s.LastSeen()
	if !probeAt.IsZero() {
		if last.After(probeAt) {
			return time.Time{}, false, false // ping'den sonra bir sey geldi: canli
		}
		if now.Sub(probeAt) >= grace {
			return probeAt, false, true
		}
		return probeAt, false, false
	}
	if now.Sub(last) < after || s.PendingCount() == 0 {
		return time.Time{}, false, false
	}
	return now, true, false
}

// warnFrame, bozuk/beklenmeyen cerceve uyarisini oturum basina maxFrameLogs
// satirla sinirlar; fazlasi sayilir ve flushFrameLogs ile ozetlenir.
func (s *Session) warnFrame(msg string, args ...any) {
	if s.frameLogs.Add(1) > maxFrameLogs {
		s.frameLogsDropped.Add(1)
		return
	}
	s.log.Warn(msg, args...)
}

// flushFrameLogs, bastirilan cerceve uyarilarini tek satirda ozetler.
func (s *Session) flushFrameLogs() {
	if n := s.frameLogsDropped.Load(); n > 0 {
		s.log.Warn("bozuk/beklenmeyen cerceve uyarilari bastirildi", "bastirilan", n, "ilk", maxFrameLogs)
	}
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now().UTC()
	s.mu.Unlock()
}

// Evict, oturumu hemen sonlandirir (yerine yeni baglanti gecti). Kapanis
// el sikismasini beklemez: yari olu soket cevap vermeyebilir.
func (s *Session) Evict(reason string) {
	s.goSafe("tunnel.evict", nil, func() {
		// Canli bir cift kopya varsa nedenini bilsin (kisa sureli, bloklamaz).
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		_ = s.Send(ctx, protocol.Bye{Type: protocol.TypeBye, Reason: reason, Code: protocol.ByeCodeReplaced}, protocol.TypeBye)
		cancel()
		s.conn.CloseNow()
	})
}

// errSessionPanic, oturum okuma dongusu panikleyip kurtarildiginda Run'in hatasi.
var errSessionPanic = errors.New("oturum panik nedeniyle kapatildi")

// goSafe, oturum kapsamli bir goroutine baslatir. Panikte surec degil yalniz bu
// oturum duser: baglanti kapatilir (Run'in okumasi hata alir ve normal kapanis
// yolu bekleyenleri/akislari temizler), varsa cancel cagrilir.
func (s *Session) goSafe(where string, cancel context.CancelFunc, fn func()) {
	go func() {
		defer obs.Recover(s.log, where, func(any) {
			if cancel != nil {
				cancel()
			}
			s.closeNowSafe()
		})
		fn()
	}()
}

// closeNowSafe, baglantiyi beklemeden kapatir (testlerde conn nil olabilir).
func (s *Session) closeNowSafe() {
	if s.conn != nil {
		_ = s.conn.CloseNow()
	}
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
	if frame.FrameType == protocol.FrameStreamData || frame.FrameType == protocol.FrameDatagram {
		// Ham TCP/UDP tunel: yerel -> ziyaretci baytlari/datagrami.
		s.routeStreamData(frame.ReqID, frame.Payload)
		return
	}
	if frame.FrameType != protocol.FrameResponseBody {
		s.warnFrame("istemciden beklenmeyen cerceve tipi", "tip", frame.FrameType)
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
				s.warnFrame("akis kontrolu ihlali; istek dusuruldu", "req_id", frame.ReqID)
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
	// Govdeli istekte istek govdesi kredisi onerilir (reqcredit.go).
	e, err := s.newExchangeCredit(s.offerReqCredit(&req))
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
	return s.streamRequestBody(ctx, reqID, body, protocol.MaxBodyBytes)
}

// StreamRequestBodyUnbounded, toplam boyut tavani OLMADAN govde aktarir. Yalniz
// uzun omurlu istek akislari (gRPC client-streaming/bidi) icindir: akis
// sonsuz surebilir, kotayi bant genisligi olcumu/siniri korur.
func (s *Session) StreamRequestBodyUnbounded(ctx context.Context, reqID uint64, body io.ReadCloser) error {
	return s.streamRequestBody(ctx, reqID, body, 0)
}

// streamRequestBody: max <= 0 ise tavan yok.
func (s *Session) streamRequestBody(ctx context.Context, reqID uint64, body io.ReadCloser, max int64) error {
	defer body.Close()

	// Istek govdesi kredisi (body_credit): ajan pencereyi verdikce gonder.
	// Beklerken ziyaretciden okunmaz; geri basinc TCP/H2 ile ziyaretciye yansir.
	var credit *reqBodyCredit
	if e, ok := s.lookupExchange(reqID); ok {
		credit = e.reqCredit
	}

	var limited io.Reader = body
	if max > 0 {
		limited = io.LimitReader(body, max+1)
	}
	buf := make([]byte, protocol.BodyChunkSize)
	var seq uint32
	var total int64

	for {
		n, readErr := limited.Read(buf)
		if n > 0 {
			total += int64(n)
			if max > 0 && total > max {
				return ErrBodyTooLarge
			}
			if credit != nil {
				if err := credit.acquire(ctx, n+protocol.StreamFrameCost, &s.reqCreditMode); err != nil {
					return err
				}
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
func (s *Session) OpenTerminal(ctx context.Context, sessionID, shell string, cols, rows uint16,
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
		Shell:     shell,
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
		FPS:       s.ClampFPS(fps),
		Quality:   quality,
		MaxWidth:  maxWidth,
		Mode:      mode,
	}, protocol.TypeScreenOpen)
}

// SubscribeScreenInputStatus, oturumun girdi durumu bildirimlerine abone olur
// ve istemcinin bu yetenegi destekleyip desteklemedigini doner. CloseScreen
// aboneligi temizler.
func (s *Session) SubscribeScreenInputStatus(sessionID string, ch chan<- protocol.ScreenInputStatus) (supported bool) {
	s.scrMu.Lock()
	s.scrInp[sessionID] = ch
	s.scrMu.Unlock()
	return s.screenInputStatus
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
	delete(s.scrInp, sessionID)
	delete(s.scrAux, sessionID)
	if id, ok := s.scr2Ids[sessionID]; ok {
		delete(s.scr2, id)
		delete(s.scr2Ids, sessionID)
	}
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

// stallMu, donmus ajan esiklerini korur: testler esikleri kisaltirken onceki
// testten kalan bir oturumun heartbeat goroutine'i ayni anda okuyabilir (race).
var stallMu sync.RWMutex

func stallParams() (every, after, grace time.Duration) {
	stallMu.RLock()
	defer stallMu.RUnlock()
	return stallCheckEvery, stallAfter, stallGrace
}
