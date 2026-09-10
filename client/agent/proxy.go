package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// localTimeout, yerel servisin yanit vermesi icin taninan sure.
// Sunucu tarafindaki 30sn'den biraz kisa tutuldu ki hata sunucuya yetissin
// ve tarayici jenerik 504 yerine acik bir sebep gorsun.
const localTimeout = 25 * time.Second

// clientSession, tek bir WSS baglantisinin durumunu tutar.
type clientSession struct {
	conn     *websocket.Conn
	log      *slog.Logger
	localURL string
	http     *http.Client

	// coder/websocket ayni anda tek yazar kaldirir.
	writeMu sync.Mutex

	mu       sync.Mutex
	inflight map[uint64]*inflight

	// targets, tunnel_id -> yerel hedef eslestirmesi.
	//
	// SUNUCU OTORITERDIR: her istek hangi tunelden geldigini soyler ve o
	// tunelin hedefine yonlendirilir. Boylece TEK bir istemci birden fazla
	// tuneli farkli portlara acabilir (api -> :8000, admin -> :9000).
	// localURL yalnizca eslesme bulunamazsa yedek olarak kullanilir.
	targets map[string]string

	// flowControl, sunucu hello_ack'te akis kontrolunu ETKINLESTIRDIYSE true.
	// Eski sunucularda false kalir ve govde eskisi gibi kredisiz gonderilir.
	flowControl bool
	// windows, istek basina gonderim kredisi (bkz. flowcontrol.go).
	windows *windowTable

	// wsConns, aktif WebSocket passthrough akislari: req_id -> yerel TCP conn.
	wsMu    sync.Mutex
	wsConns map[uint64]net.Conn

	onRequest func(method, path string, status int, duration time.Duration)
}

// SetTargets, sunucudan gelen tunel listesini uygular (hello_ack / config_update).
func (cs *clientSession) SetTargets(specs []protocol.TunnelSpec) {
	m := make(map[string]string, len(specs))
	for _, s := range specs {
		m[s.ID] = s.Target
	}
	cs.mu.Lock()
	cs.targets = m
	cs.mu.Unlock()
}

// targetFor, istegin gidecegi yerel adresi doner.
func (cs *clientSession) targetFor(tunnelID string) string {
	cs.mu.Lock()
	target, ok := cs.targets[tunnelID]
	cs.mu.Unlock()
	if ok && target != "" {
		return strings.TrimSuffix(target, "/")
	}
	// Sunucu bu tunel icin hedef bildirmediyse yedege dus.
	return cs.localURL
}

// inflight, islenmekte olan tek bir istegi temsil eder.
type inflight struct {
	bodyW  *io.PipeWriter
	bodyR  *io.PipeReader
	cancel context.CancelFunc
	once   sync.Once
}

func (i *inflight) closeBody(err error) {
	i.once.Do(func() {
		if err != nil {
			i.bodyW.CloseWithError(err)
		} else {
			i.bodyW.Close()
		}
	})
}

func newClientSession(conn *websocket.Conn, localURL string, log *slog.Logger) *clientSession {
	return &clientSession{
		conn:     conn,
		log:      log,
		localURL: strings.TrimSuffix(localURL, "/"),
		inflight: make(map[uint64]*inflight),
		targets:  make(map[string]string),
		wsConns:  make(map[uint64]net.Conn),
		windows:  newWindowTable(),
		http: &http.Client{
			// Yonlendirmeleri TAKIP ETME: 301/302 oldugu gibi tarayiciya
			// donmeli, yoksa yerel servisin yonlendirme mantigi bozulur.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: localTimeout,
			Transport: &http.Transport{
				DialContext:     safeDialContext,
				MaxIdleConns:    100,
				IdleConnTimeout: 90 * time.Second,
			},
		},
	}
}

// --- yazma ----------------------------------------------------------------

func (cs *clientSession) sendControl(ctx context.Context, v any, t protocol.MessageType) error {
	data, err := protocol.Marshal(v, t)
	if err != nil {
		return err
	}
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()

	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return cs.conn.Write(wctx, websocket.MessageText, data)
}

func (cs *clientSession) sendFrame(ctx context.Context, f protocol.BodyFrame) error {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()

	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return cs.conn.Write(wctx, websocket.MessageBinary, f.Encode())
}

// --- istek yasam dongusu ---------------------------------------------------

// handleRequest, gelen istegi ayri bir goroutine'de isler; okuma dongusu
// bloklanmaz, boylece es zamanli istekler paralel akar.
func (cs *clientSession) handleRequest(parent context.Context, req protocol.HTTPRequest) {
	ctx, cancel := context.WithCancel(parent)
	pr, pw := io.Pipe()
	fl := &inflight{bodyW: pw, bodyR: pr, cancel: cancel}

	cs.mu.Lock()
	cs.inflight[req.ReqID] = fl
	cs.mu.Unlock()

	if !req.HasBody {
		fl.closeBody(nil)
	}

	go func() {
		defer cancel()
		defer cs.forget(req.ReqID)
		cs.doLocal(ctx, req, fl)
	}()
}

func (cs *clientSession) forget(reqID uint64) {
	cs.mu.Lock()
	fl, ok := cs.inflight[reqID]
	delete(cs.inflight, reqID)
	cs.mu.Unlock()
	if ok {
		fl.closeBody(nil)
	}
}

func (cs *clientSession) doLocal(ctx context.Context, req protocol.HTTPRequest, fl *inflight) {
	start := time.Now()
	target := cs.targetFor(req.TunnelID) + req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	u, err := url.Parse(target)
	if err != nil {
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, "gecersiz hedef URL: "+err.Error())
		return
	}
	if isBlockedTargetHost(u.Hostname()) {
		cs.log.Warn("SSRF girisimi engellendi", "req_id", req.ReqID, "hedef", target)
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, "SSRF korumasi: hedef adres engellendi")
		return
	}

	var body io.Reader
	if req.HasBody {
		body = fl.bodyR
	}

	hreq, err := http.NewRequestWithContext(ctx, req.Method, target, body)
	if err != nil {
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, err.Error())
		return
	}
	for k, vals := range req.Headers {
		for _, v := range vals {
			hreq.Header.Add(k, v)
		}
	}
	// Host basligini yerel servise oldugu gibi tasima: yerel servis kendi
	// adresini beklemeli. Orijinal hostname X-Forwarded-Host'ta zaten var.
	hreq.Host = ""

	resp, err := cs.http.Do(hreq)
	if err != nil {
		duration := time.Since(start)
		if cs.onRequest != nil {
			cs.onRequest(req.Method, req.Path, 502, duration)
		}
		code, msg := classifyLocalError(err)
		cs.log.Warn("yerel servise ulasilamadi",
			"req_id", req.ReqID, "hedef", target, "hata", err)
		cs.sendError(ctx, req.ReqID, code, msg)
		return
	}
	duration := time.Since(start)
	if cs.onRequest != nil {
		cs.onRequest(req.Method, req.Path, resp.StatusCode, duration)
	}
	defer resp.Body.Close()

	hasBody := resp.ContentLength != 0
	if err := cs.sendControl(ctx, protocol.HTTPResponse{
		Type:    protocol.TypeHTTPResponse,
		ReqID:   req.ReqID,
		Status:  resp.StatusCode,
		Headers: resp.Header,
		HasBody: hasBody,
	}, protocol.TypeHTTPResponse); err != nil {
		cs.log.Warn("yanit basligi gonderilemedi", "req_id", req.ReqID, "hata", err)
		return
	}
	if !hasBody {
		return
	}

	if err := cs.streamResponse(ctx, req.ReqID, resp.Body); err != nil {
		cs.log.Warn("yanit govdesi gonderilemedi", "req_id", req.ReqID, "hata", err)
	}
}

func (cs *clientSession) streamResponse(ctx context.Context, reqID uint64, body io.Reader) error {
	// Istek bitince kredi kaydini temizle (hangi yoldan cikilirsa cikilsin).
	if cs.flowControl {
		defer cs.windows.release(reqID)
	}

	limited := io.LimitReader(body, protocol.MaxBodyBytes+1)
	buf := make([]byte, protocol.BodyChunkSize)
	var seq uint32
	var total int64

	for {
		n, readErr := limited.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > protocol.MaxBodyBytes {
				return cs.sendError(ctx, reqID, protocol.CodeBodyTooLarge,
					"yerel servis yaniti 32 MB sinirini asti")
			}
			// Akis kontrolu: sunucu bu istek icin yer acana kadar bekle.
			// YALNIZCA bu akis bekler — her istek kendi goroutine'inde oldugu
			// icin diger istekler akmaya devam eder.
			if cs.flowControl {
				if err := cs.windows.acquire(ctx, reqID, n); err != nil {
					return err
				}
			}
			if err := cs.sendFrame(ctx, protocol.BodyFrame{
				FrameType: protocol.FrameResponseBody,
				ReqID:     reqID,
				Seq:       seq,
				Payload:   buf[:n],
			}); err != nil {
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

	return cs.sendFrame(ctx, protocol.BodyFrame{
		FrameType: protocol.FrameResponseBody,
		ReqID:     reqID,
		Seq:       seq,
		EOF:       true,
	})
}

func (cs *clientSession) sendError(ctx context.Context, reqID uint64, code, msg string) error {
	return cs.sendControl(ctx, protocol.HTTPError{
		Type:    protocol.TypeHTTPError,
		ReqID:   reqID,
		Code:    code,
		Message: msg,
	}, protocol.TypeHTTPError)
}

// routeBody, sunucudan gelen istek govdesi parcasini ilgili istege yazar.
func (cs *clientSession) routeBody(frame protocol.BodyFrame) {
	if frame.FrameType != protocol.FrameRequestBody {
		cs.log.Warn("sunucudan beklenmeyen cerceve tipi", "tip", frame.FrameType)
		return
	}

	cs.mu.Lock()
	fl, ok := cs.inflight[frame.ReqID]
	cs.mu.Unlock()
	if !ok {
		return
	}

	if len(frame.Payload) > 0 {
		if _, err := fl.bodyW.Write(frame.Payload); err != nil {
			cs.log.Debug("istek govdesi yazilamadi", "req_id", frame.ReqID, "hata", err)
			return
		}
	}
	if frame.EOF {
		fl.closeBody(nil)
	}
}

// cancelRequest, sunucu iptal bildirdiginde yerel istegi durdurur.
func (cs *clientSession) cancelRequest(reqID uint64, reason string) {
	cs.mu.Lock()
	fl, ok := cs.inflight[reqID]
	cs.mu.Unlock()
	if ok {
		cs.log.Debug("istek iptal edildi", "req_id", reqID, "sebep", reason)
		fl.cancel()
	}
}

// closeAll, baglanti koptugunda tum ucustaki istekleri sonlandirir.
func (cs *clientSession) closeAll() {
	cs.mu.Lock()
	all := cs.inflight
	cs.inflight = make(map[uint64]*inflight)
	cs.mu.Unlock()

	for _, fl := range all {
		fl.cancel()
		fl.closeBody(errors.New("baglanti koptu"))
	}
	cs.closeAllWS()
}

// classifyLocalError, yerel servis hatasini kontrat hata koduna cevirir.
func classifyLocalError(err error) (code, msg string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return protocol.CodeUpstreamTimeout, "yerel servis zamaninda yanit vermedi"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return protocol.CodeUpstreamTimeout, "yerel servis zamaninda yanit vermedi"
	}
	return protocol.CodeLocalUnreachable, err.Error()
}

// restrictedPorts, ters proxy ile acilmamasi gereken hassas yonetim ve veritabani portlari.
var restrictedPorts = map[int]bool{
	22:    true, // SSH
	23:    true, // Telnet
	25:    true, // SMTP
	135:   true, // Windows RPC
	137:   true, // NetBIOS
	138:   true, // NetBIOS
	139:   true, // NetBIOS
	445:   true, // SMB
	3389:  true, // RDP
	5432:  true, // PostgreSQL
	3306:  true, // MySQL
	6379:  true, // Redis
	27017: true, // MongoDB
}

func isRestrictedPort(port int) bool {
	return restrictedPorts[port]
}

// isBlockedIP, tek bir IP adresinin bulut metadata, link-local veya
// gecersiz bir blokta olup olmadigini test eder.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || (!ip.IsLoopback() && ip.IsUnspecified()) {
		return true
	}
	if ip.String() == "169.254.169.254" {
		return true
	}
	return false
}

// isBlockedTargetHost, AWS/GCP/Azure gibi bulut metadata servislerine veya
// link-local adreslere SSRF yapilmasini engeller.
func isBlockedTargetHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "169.254.169.254" || h == "instance-data" || h == "metadata.google.internal" || h == "metadata" {
		return true
	}
	ip := net.ParseIP(h)
	if ip != nil {
		return isBlockedIP(ip)
	}
	return false
}

// safeDialContext, DNS Rebinding ve Port Scanning saldirilarina karsi
// hem host adi hem de cozumlenen tum IP adreslerini ve portu dogrular.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	if isBlockedTargetHost(host) {
		return nil, errors.New("SSRF korumasi: yasakli hedef adres: " + host)
	}

	p, err := strconv.Atoi(portStr)
	if err == nil && isRestrictedPort(p) {
		return nil, errors.New("port korumasi: hassas servis portuna tunelleme engellendi: " + portStr)
	}

	// DNS Rebinding korumasi: eger host dogrudan IP degilse, DNS cozumlemesi yap
	if net.ParseIP(host) == nil {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err == nil {
			for _, ip := range ips {
				if isBlockedIP(ip.IP) {
					return nil, errors.New("SSRF korumasi: hedef adres bulut metadata veya link-local IP'ye cozumleniyor: " + ip.IP.String())
				}
			}
		}
	}

	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

