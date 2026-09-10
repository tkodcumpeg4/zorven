package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/client/metrics"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	backoffMin  = 1 * time.Second
	backoffMax  = 60 * time.Second
	dialTimeout = 15 * time.Second
)

type Agent struct {
	ServerAddr string // "zorven.app:443"
	Token      string
	LocalURL   string
	Insecure   bool   // ws:// kullan (lokal gelistirme)
	CACertPath string // self-signed sunucu sertifikasini guven havuzuna ekle
	Log        *slog.Logger
	NoTerminal bool // true ise uzak kabuk (PTY) erisimi tamamen reddedilir
	NoScreen   bool // true ise uzak ekran paylasimi tamamen reddedilir
	IsService  bool // true ise arka plan sistem servisi olarak calisir

	// RequestedTarget, tek komutla acilacak port/hedef (or. "http://localhost:8080").
	RequestedTarget string

	// OnRequest, gelen HTTP istekleri islendiginde tetiklenir (CLI anlik loglama).
	OnRequest func(method, path string, status int, duration time.Duration)

	// OnStatus, baglanti durumu her degistiginde cagrilir. nil olabilir.
	// Masaustu arayuzu bunu dinleyerek gostergeyi gunceller; CLI kullanmaz.
	OnStatus func(Status)

	// tunnels, hello_ack ile gelen tunel listesi (readLoop'a aktarilir).
	tunnels []protocol.TunnelSpec

	// flowControl, sunucunun hello_ack'te akis kontrolunu etkinlestirip
	// etkinlestirmedigi. readLoop bunu clientSession'a aktarir.
	flowControl bool

	httpClient *http.Client // CA havuzu ayarlandiysa dolu

	mu         sync.Mutex // lastStatus, terminal ve screen koruması
	lastStatus Status     // son emit edilen durum (refreshSessions icin)
	terminal   *terminalManager
	screen     *screenManager
}

// setupTLS, --ca-cert verilmisse o sertifikayi guven havuzuna ekler.
//
// Neden: `rpshell-server cert` ile uretilen sertifika kendi kendini imzalar ve
// sistem guven deposunda bulunmaz. Bu olmadan tek secenek --insecure olurdu;
// o da sifrelemeyi tamamen kapatir. Bu yol sifrelemeyi KORUR, yalnizca bu
// sertifikaya guvenir.
func (a *Agent) setupTLS() error {
	if a.CACertPath == "" {
		return nil
	}
	pem, err := os.ReadFile(a.CACertPath)
	if err != nil {
		return fmt.Errorf("CA sertifikasi okunamadi: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("%s gecerli bir PEM sertifikasi icermiyor", a.CACertPath)
	}
	a.httpClient = &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}},
	}
	a.Log.Info("CA sertifikasi guven havuzuna eklendi", "yol", a.CACertPath)
	return nil
}

// Run, baglanti kopsa da surekli yeniden dener; yalnizca ctx iptal edilince
// veya token kalici olarak gecersizse (401/409/426) durur.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.setupTLS(); err != nil {
		a.emit(Status{State: StateFatal, Message: err.Error()})
		return &FatalError{err.Error()}
	}
	a.emit(Status{State: StateConnecting, Message: "sunucuya baglaniliyor"})
	backoff := backoffMin

	for {
		err := a.connectOnce(ctx)

		if ctx.Err() != nil {
			a.emit(Status{State: StateStopped, Message: "durduruldu"})
			return nil
		}

		// Kalici hatalarda yeniden denemek anlamsiz — kullaniciya soyle ve cik.
		var fatal *FatalError
		if errors.As(err, &fatal) {
			a.emit(Status{State: StateFatal, Message: fatal.Msg})
			return err
		}

		reconnectMsg := fmt.Sprintf("bağlantı koptu, %s sonra yeniden denenecek", backoff.Round(time.Second))
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "first record does not look like a TLS handshake") || strings.Contains(errStr, "oversized record received") {
				reconnectMsg = "Sunucu şifresiz (HTTP) çalışıyor — Ayarlar'dan 'Şifrelemeyi kapat'ı seçin"
			} else if strings.Contains(errStr, "certificate signed by unknown authority") {
				reconnectMsg = "Sertifika doğrulanamadı — Ayarlar'dan CA sertifikası (server.crt) seçin"
			}
		}

		a.emit(Status{
			State:   StateReconnecting,
			Message: reconnectMsg,
		})
		a.Log.Warn("baglanti koptu, yeniden denenecek",
			"hata", err, "bekleme", backoff.Round(time.Millisecond))

		// Jitter: cok sayida istemci ayni anda kopunca sunucuya es zamanli
		// yuklenmesinler diye bekleme suresine %0-25 rastgele ekleniyor.
		wait := backoff + time.Duration(rand.Int64N(int64(backoff/4)+1))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}

		if backoff < backoffMax {
			backoff *= 2
			if backoff > backoffMax {
				backoff = backoffMax
			}
		}
	}
}

// fatalError, yeniden denenmemesi gereken hatalari isaretler.
type FatalError struct{ Msg string }

// State, baglantinin o anki durumu.
type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
	StateStopped      State = "stopped"
	StateFatal        State = "fatal" // yeniden denenmeyecek (401/409/426)
)

// Status, arayuze bildirilen durum anlik goruntusu.
type Status struct {
	State          State     `json:"state"`
	Message        string    `json:"message"`
	ClientID       string    `json:"client_id"`
	Tunnels        []Tunnel  `json:"tunnels"`
	Since          time.Time `json:"since"`
	TerminalActive int       `json:"terminal_active"` // aktif terminal oturumu sayisi
	ScreenActive   int       `json:"screen_active"`   // aktif ekran paylasim oturumu sayisi
}

// Tunnel, arayuzde gosterilecek tunel ozeti.
//
// Hostnames COGULDUR: bir tunel birden cok adla yayinlanabilir (kisa global ad
// + kiraci kapsamli ad).
type Tunnel struct {
	Hostnames []string `json:"hostnames"`
	Target    string   `json:"target"`
}

// emit, durum degisikligini bildirir (OnStatus nil ise sessizce gecer).
func (a *Agent) emit(s Status) {
	s.Since = time.Now()
	a.mu.Lock()
	a.lastStatus = s
	a.mu.Unlock()
	if a.OnStatus != nil {
		a.OnStatus(s)
	}
}

func (e *FatalError) Error() string { return e.Msg }

func (a *Agent) connectOnce(ctx context.Context) error {
	scheme := "wss"
	if a.Insecure {
		scheme = "ws"
	}
	url := fmt.Sprintf("%s://%s/_tunnel/v1/connect", scheme, a.ServerAddr)

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPClient: a.httpClient, // nil ise varsayilan kullanilir
		HTTPHeader: http.Header{
			// Token query string'e DEGIL basliga konur: query string sunucu
			// erisim loglarina ve ara proxy loglarina sizar.
			"Authorization":           {"Bearer " + a.Token},
			"X-Tunnel-Client-Version": {protocol.Version},
		},
	})
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return &FatalError{"token gecersiz — sunucu 401 dondu"}
			case http.StatusUpgradeRequired:
				return &FatalError{"istemci surumu sunucuyla uyumsuz (sunucu 426 dondu), guncelleyin"}
			case http.StatusConflict:
				return &FatalError{"bu token ile baska bir istemci zaten bagli (409)"}
			}
		}
		return fmt.Errorf("baglanilamadi: %w", err)
	}
	defer conn.CloseNow()

	// Varsayilan 32 KB limit govde cerceveleri icin yetersiz (bkz. WSReadLimit).
	conn.SetReadLimit(protocol.WSReadLimit)

	if err := a.handshake(ctx, conn); err != nil {
		return fmt.Errorf("el sikismasi basarisiz: %w", err)
	}

	return a.readLoop(ctx, conn)
}

func (a *Agent) handshake(ctx context.Context, conn *websocket.Conn) error {
	hello, err := protocol.Marshal(protocol.Hello{
		Type:            protocol.TypeHello,
		ClientVersion:   protocol.Version,
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		// Akis kontrolunu destekledigimizi bildir. Sunucu desteklemiyorsa
		// hello_ack'te geri onaylamaz ve eski davranis surer.
		Features:        []string{protocol.FeatureFlowControl},
		RequestedTarget: a.RequestedTarget,
		IsService:       a.IsService,
		Metrics:         metrics.Collect(),
	}, protocol.TypeHello)
	if err != nil {
		return err
	}

	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, hello); err != nil {
		return err
	}

	rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
	defer rcancel()
	_, data, err := conn.Read(rctx)
	if err != nil {
		return err
	}

	var ack protocol.HelloAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return err
	}
	if ack.Type != protocol.TypeHelloAck {
		return fmt.Errorf("hello_ack bekleniyordu, alinan: %s", ack.Type)
	}

	// Sunucunun ETKINLESTIRDIGI yetenekler. Eski sunucular Features gondermez
	// -> flowControl false kalir ve govde kredisiz gonderilir (eski davranis).
	a.flowControl = false
	for _, f := range ack.Features {
		if f == protocol.FeatureFlowControl {
			a.flowControl = true
		}
	}

	a.tunnels = ack.Tunnels
	tunnels := make([]Tunnel, 0, len(ack.Tunnels))
	for _, tn := range ack.Tunnels {
		tunnels = append(tunnels, Tunnel{Hostnames: tn.Hostnames, Target: tn.Target})
	}
	a.emit(Status{
		State:    StateConnected,
		Message:  "baglandi",
		ClientID: ack.ClientID,
		Tunnels:  tunnels,
	})

	a.Log.Info("el sikismasi basarili", "client", ack.ClientID, "session", ack.SessionID,
		"heartbeat_sn", ack.HeartbeatIntervalS, "tunel_sayisi", len(ack.Tunnels))

	if len(ack.Tunnels) == 0 {
		a.Log.Warn("bu istemciye tanimli aktif tunel yok — " +
			"sunucuda 'rpshell-server tunnel add <hostname> <client-id> <target>' calistirin")
	}
	for _, t := range ack.Tunnels {
		a.Log.Info("tunel aktif", "adlar", strings.Join(t.Hostnames, ", "), "hedef", t.Target)
	}
	return nil
}

func (a *Agent) readLoop(ctx context.Context, conn *websocket.Conn) error {
	cs := newClientSession(conn, a.LocalURL, a.Log)
	cs.flowControl = a.flowControl // el sikismada pazarlandi
	cs.onRequest = a.OnRequest
	cs.SetTargets(a.tunnels)
	tm := newTerminalManager(cs)
	sm := newScreenManager(cs)

	a.mu.Lock()
	a.terminal = tm
	a.screen = sm
	a.mu.Unlock()

	// refreshSessions, terminal/ekran oturumu acilip kapandiginda son emiti
	// guncel sayilarla tekrarlar; boylece masaustu arayuzu anlik gorur.
	refreshSessions := func() {
		a.mu.Lock()
		s := a.lastStatus
		a.mu.Unlock()
		s.TerminalActive = tm.count()
		s.ScreenActive = sm.count()
		a.emit(s)
	}
	tm.onChanged = refreshSessions
	sm.onChanged = refreshSessions

	defer func() {
		a.mu.Lock()
		a.terminal = nil
		a.screen = nil
		a.mu.Unlock()
		tm.closeAll()
		sm.closeAll()
		cs.closeAll()
		refreshSessions()
	}()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			// Upgrade + handshake ile Hub.Register arasindaki yaris: iki instance
			// ayni anda baglanirsa ikisi de upgrade'i gecer, sonra biri
			// Hub.Register'da kaybeder ve sunucu onu "already connected"
			// (StatusPolicyViolation) ile kapatir. Bu baglanti Dial'dan SONRA
			// koptugu icin 409 olarak algilanmaz; kalici hata sayilmazsa istemci
			// bosuna backoff'lu reconnect dongusune girer. On-kontrol yolundaki
			// (handler.go:83) 409 ile ayni sekilde: aninda net mesajla cik.
			var ce websocket.CloseError
			if errors.As(err, &ce) && ce.Code == websocket.StatusPolicyViolation &&
				strings.Contains(ce.Reason, "already connected") {
				return &FatalError{"bu token ile baska bir istemci zaten bagli (409)"}
			}
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		if typ == websocket.MessageBinary {
			frame, derr := protocol.DecodeBodyFrame(data)
			if derr != nil {
				a.Log.Warn("bozuk govde cercevesi", "hata", derr)
				continue
			}
			if frame.FrameType == protocol.FrameWSData {
				cs.routeWSData(frame.ReqID, frame.Payload)
				continue
			}
			cs.routeBody(frame)
			continue
		}

		mt, err := protocol.PeekType(data)
		if err != nil {
			a.Log.Warn("cozulemeyen mesaj", "hata", err)
			continue
		}

		switch mt {
		case protocol.TypePing:
			m := metrics.Collect()
			if err := cs.sendControl(ctx, protocol.Pong{
				Type:    protocol.TypePong,
				TS:      time.Now().UTC(),
				Metrics: m,
			}, protocol.TypePong); err != nil {
				return fmt.Errorf("pong gonderilemedi: %w", err)
			}
			a.Log.Debug("ping/pong", "cpu", m.CPUPercent, "mem", m.MemoryPercent)

		case protocol.TypeHTTPRequest:
			var req protocol.HTTPRequest
			if err := json.Unmarshal(data, &req); err != nil {
				a.Log.Warn("http_request cozulemedi", "hata", err)
				continue
			}
			a.Log.Debug("istek alindi",
				"req_id", req.ReqID, "metot", req.Method, "path", req.Path)
			cs.handleRequest(ctx, req)

		case protocol.TypeWSOpen:
			var m protocol.WSOpen
			if err := json.Unmarshal(data, &m); err != nil {
				a.Log.Warn("ws_open cozulemedi", "hata", err)
				continue
			}
			go cs.handleWSOpen(m)

		case protocol.TypeWSClose:
			var m protocol.WSClose
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			cs.handleWSClose(m.ReqID)

		case protocol.TypeTerminalOpen:
			var m protocol.TerminalOpen
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if a.NoTerminal {
				a.Log.Warn("terminal acma istegi reddedildi (--no-terminal aktif)", "session", m.SessionID)
				tm.sendExit(ctx, m.SessionID, 1, "terminal erisimi bu istemcide kapatilmistir (--no-terminal)")
				continue
			}
			a.Log.Info("uzak terminal acildi", "session", m.SessionID)
			tm.open(ctx, m)
			refreshSessions()

		case protocol.TypeTerminalInput:
			var m protocol.TerminalInput
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if a.NoTerminal {
				continue
			}
			tm.input(m)

		case protocol.TypeTerminalResize:
			var m protocol.TerminalResize
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if a.NoTerminal {
				continue
			}
			tm.resize(m)

		case protocol.TypeTerminalClose:
			var m protocol.TerminalClose
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			tm.closeSession(m.SessionID)
			refreshSessions()

		case protocol.TypeScreenOpen:
			var m protocol.ScreenOpen
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if a.NoScreen {
				a.Log.Warn("ekran paylasim istegi reddedildi (--no-screen aktif)", "session", m.SessionID)
				sm.sendError(ctx, m.SessionID, "ekran paylasimi bu istemcide kapatilmistir (--no-screen)")
				continue
			}
			a.Log.Info("uzak ekran acildi", "session", m.SessionID)
			sm.open(ctx, m)
			refreshSessions()

		case protocol.TypeScreenInput:
			var m protocol.ScreenInput
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if a.NoScreen {
				continue
			}
			sm.input(m)

		case protocol.TypeScreenClose:
			var m protocol.ScreenClose
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			sm.closeSession(m.SessionID)
			refreshSessions()

		case protocol.TypeCancel:
			var c protocol.Cancel
			if err := json.Unmarshal(data, &c); err != nil {
				continue
			}
			cs.cancelRequest(c.ReqID, c.Reason)

		case protocol.TypeWindowUpdate:
			// Sunucu yanit govdesini tuketti; o istek icin kredi iade ediyor.
			var wu protocol.WindowUpdate
			if err := json.Unmarshal(data, &wu); err != nil {
				continue
			}
			cs.windows.add(wu.ReqID, wu.Increment)

		case protocol.TypeConfigUpdate:
			var cu protocol.ConfigUpdate
			if err := json.Unmarshal(data, &cu); err != nil {
				a.Log.Warn("config_update cozulemedi", "hata", err)
				continue
			}
			cs.SetTargets(cu.Tunnels)
			// Guncel tunel listesini arayuze YENIDEN YAY: masaustu app panelde
			// tunel eklenince/silinince listeyi anlik tazelesin (aksi halde
			// yalnizca ilk baglantidaki tuneller gorunurdu).
			a.tunnels = cu.Tunnels
			newTunnels := make([]Tunnel, 0, len(cu.Tunnels))
			for _, tn := range cu.Tunnels {
				newTunnels = append(newTunnels, Tunnel{Hostnames: tn.Hostnames, Target: tn.Target})
			}
			a.mu.Lock()
			cfgStatus := a.lastStatus
			a.mu.Unlock()
			cfgStatus.Tunnels = newTunnels
			a.emit(cfgStatus)
			a.Log.Info("tunel yapilandirmasi guncellendi", "tunel_sayisi", len(cu.Tunnels))
			for _, tn := range cu.Tunnels {
				a.Log.Info("tunel aktif", "adlar", strings.Join(tn.Hostnames, ", "), "hedef", tn.Target)
			}

		case protocol.TypeBye:
			var b protocol.Bye
			json.Unmarshal(data, &b)
			a.Log.Info("sunucu baglantiyi sonlandirdi", "sebep", b.Reason)
			if strings.Contains(b.Reason, "token") {
				return &FatalError{"sunucu token'i gecersiz kildi: " + b.Reason}
			}
			return nil

		default:
			a.Log.Warn("beklenmeyen mesaj tipi", "tip", mt)
		}
	}
}

// CloseSessions, tüm aktif uzak terminal ve ekran oturumlarını anında sonlandırır.
func (a *Agent) CloseSessions() {
	a.mu.Lock()
	tm := a.terminal
	sm := a.screen
	a.mu.Unlock()

	if tm != nil {
		tm.closeAll()
	}
	if sm != nil {
		sm.closeAll()
	}
}

