package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// localTimeout, yerel servisin yanit vermesi icin taninan sure.
// Sunucu tarafindaki 30sn'den biraz kisa tutuldu ki hata sunucuya yetissin
// ve tarayici jenerik 504 yerine acik bir sebep gorsun.
const localTimeout = 25 * time.Second

// respIdleTimeout, yerel yanit govdesinde okumalar arasi en uzun sessizlik.
// Toplam sure SINIRLI DEGILDIR; yalniz takilan/olu bir yerel baglanti kesilir.
const respIdleTimeout = 5 * time.Minute

// bufPool, yanit govdesi kopyalama tamponlari (istek basina 64 KB tahsisini onler).
var bufPool = sync.Pool{New: func() any { b := make([]byte, protocol.BodyChunkSize); return &b }}

// clientSession, tek bir WSS baglantisinin durumunu tutar.
type clientSession struct {
	// Z4: cihaz sertifikasi (bkz. devicecert.go).
	devCert         *deviceCertManager
	deviceChallenge string
	certReply       chan protocol.DeviceCert
	postureKick     chan struct{}

	conn     *websocket.Conn
	log      *slog.Logger
	localURL string
	http     *http.Client

	// zapp, "zapp://<app>/<servis>" tunel hedeflerini (Zorven Apps) canli
	// LocalAddr'a cozer (apps_zapp.go). nil olabilir.
	zapp *zappTable

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

	// screenInputStatus, girdi durumu bildirimi pazarlandiysa true (conn.go).
	screenInputStatus bool
	// udpProbe, UDP tunel olcumu pazarlandiysa true (conn.go).
	udpProbe bool
	// httpTrailers, yanit trailer'i / gRPC pazarlandiysa true (conn.go).
	httpTrailers bool
	// originProto, yerel servise HTTP surumu politikasi (origin.go).
	originProto string
	// origin, h2 yerel istemcileri (tembel kurulur; origin.go).
	origin originClients
	// screen2, screen2 modu pazarlandiysa true (conn.go).
	screen2 bool
	// screen2Layers, katmanli screen2 pazarlandiysa true (conn.go).
	screen2Layers bool
	// screen2Stats, saniyelik canli olcum ozeti pazarlandiysa true (conn.go).
	screen2Stats bool
	// screen2Video, H.264 video modu pazarlandiysa true (conn.go).
	screen2Video bool
	// screen2Feedback, sunucu izleyici geri bildirimi pazarlandiysa true (conn.go).
	screen2Feedback bool
	// screen2FPS60, sunucu 30 fps ustu yakalamayi (60'a kadar) onayladiysa true (conn.go).
	// Onaylanmadiysa ajan 30'un ustune CIKMAZ (eski sunucu o degeri hic gondermez).
	screen2FPS60 bool
	// screenCursor / screenClipboard, imlec katmani / pano pazarlandiysa true (conn.go).
	screenCursor    bool
	screenClipboard bool
	// screenFiles, dosya aktarimi pazarlandiysa ve yerelde kapatilmadiysa true (conn.go).
	screenFiles bool
	// windows, istek basina gonderim kredisi (bkz. flowcontrol.go).
	windows *windowTable

	// wsConns, aktif WebSocket passthrough akislari: req_id -> yerel TCP conn.
	wsMu    sync.Mutex
	wsConns map[uint64]net.Conn

	// streamConns, aktif ham TCP/UDP tunel akislari: req_id -> yerel conn (FAZ 3 / D2).
	streamMu    sync.Mutex
	streamConns map[uint64]net.Conn
	// streamTunnels, UDP akislarinin tunel kimligi (dusurme sayaci icin).
	streamTunnels map[uint64]string
	udpDrops      udpDropCounter

	onRequest func(method, path string, status int, duration time.Duration)

	// tm, tunel basina olcumler (ajan omrunce yasar; nil olabilir).
	tm *tunnelMetrics

	// Yazma kuyruklari (writeq.go): WS / ham TCP akislarinin yerel yazimi.
	// wsMu / streamMu ile korunur; tembel olusur (testler sadece conn haritasini doldurabilir).
	wsQ     map[uint64]*writeQueue
	streamQ map[uint64]*writeQueue

	// connTunnel: ws/akis req_id -> tunel kimligi (olcum + tunel kaldirilinca kapatma).
	tmu        sync.Mutex
	connTunnel map[uint64]string
	// dialCancel: yerel dial surerken gelen ws_close / stream_close'un dial'i iptal etmesi icin.
	dialMu     sync.Mutex
	dialCancel map[uint64]context.CancelFunc
}

// SetTargets, sunucudan gelen tunel listesini uygular (hello_ack / config_update).
// Hedefi degisen veya listeden kalkan tunellerin kimliklerini doner; cagiran
// bunlara ait acik WS/akis baglantilarini kapatir (closeTunnelConns).
func (cs *clientSession) SetTargets(specs []protocol.TunnelSpec) (changed []string) {
	m := make(map[string]string, len(specs))
	for _, s := range specs {
		m[s.ID] = s.Target
	}
	cs.mu.Lock()
	old := cs.targets
	for id, tgt := range old {
		if nt, ok := m[id]; !ok || nt != tgt {
			changed = append(changed, id)
		}
	}
	cs.targets = m
	cs.mu.Unlock()
	return changed
}

// noteConn, bir ws/akisin hangi tunele ait oldugunu kaydeder.
func (cs *clientSession) noteConn(reqID uint64, tunnelID string) {
	cs.tmu.Lock()
	if cs.connTunnel == nil {
		cs.connTunnel = make(map[uint64]string)
	}
	cs.connTunnel[reqID] = tunnelID
	cs.tmu.Unlock()
}

func (cs *clientSession) forgetConn(reqID uint64) {
	cs.tmu.Lock()
	delete(cs.connTunnel, reqID)
	cs.tmu.Unlock()
}

func (cs *clientSession) tunnelOfConn(reqID uint64) string {
	cs.tmu.Lock()
	defer cs.tmu.Unlock()
	return cs.connTunnel[reqID]
}

// closeTunnelConns, verilen tunellere ait acik WS ve ham akis baglantilarini kapatir
// (tunel kaldirildi veya hedefi degisti: eski hedefe baglanti acik kalmamali).
func (cs *clientSession) closeTunnelConns(tunnelIDs []string) {
	if len(tunnelIDs) == 0 {
		return
	}
	set := make(map[string]bool, len(tunnelIDs))
	for _, id := range tunnelIDs {
		set[id] = true
	}
	cs.tmu.Lock()
	var ids []uint64
	for rid, tid := range cs.connTunnel {
		if set[tid] {
			ids = append(ids, rid)
		}
	}
	cs.tmu.Unlock()
	for _, rid := range ids {
		cs.closeWS(rid)
		cs.closeStream(rid)
		cs.cancelDial(rid)
	}
}

func (cs *clientSession) beginDial(reqID uint64, parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	cs.dialMu.Lock()
	if cs.dialCancel == nil {
		cs.dialCancel = make(map[uint64]context.CancelFunc)
	}
	cs.dialCancel[reqID] = cancel
	cs.dialMu.Unlock()
	return ctx, func() {
		cs.dialMu.Lock()
		delete(cs.dialCancel, reqID)
		cs.dialMu.Unlock()
		cancel()
	}
}

func (cs *clientSession) cancelDial(reqID uint64) {
	cs.dialMu.Lock()
	cancel := cs.dialCancel[reqID]
	cs.dialMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// targetFor, istegin gidecegi yerel adresi doner.
func (cs *clientSession) targetFor(tunnelID string) string {
	cs.mu.Lock()
	target, ok := cs.targets[tunnelID]
	cs.mu.Unlock()
	if ok && target != "" {
		t := strings.TrimSuffix(target, "/")
		if strings.HasPrefix(t, zappScheme) {
			return zappBase(cs.zapp, t)
		}
		return t
	}
	// Sunucu bu tunel icin hedef bildirmediyse yedege dus. Yedek yalnizca
	// kullanici acikca bir hedef verdiyse (RequestedTarget) dolu olur; aksi
	// halde "" doner ve cagiranlar istegi acik bir hatayla reddeder (bos hedef
	// kendiliginden bir yerel porta yonlenmez).
	return cs.localURL
}

// inflight, islenmekte olan tek bir istegi temsil eder.
type inflight struct {
	bodyW  *io.PipeWriter
	bodyR  *io.PipeReader
	cancel context.CancelFunc
	once   sync.Once
	// q, istek govdesinin SINIRLI yazma kuyrugu (yalniz govdeli isteklerde): okuma
	// dongusu yavas yerel servis yuzunden bloklanmaz (writeq.go).
	q *writeQueue
	// failMsg, kuyruk tasmasi/yazim zaman asimi nedeniyle iptal sebebi.
	failMsg atomic.Pointer[string]
	// idle, yanit govdesi bosta zaman asimi icin son okuma hareketi.
	tunnelID string
}

func (i *inflight) closeBody(err error) {
	i.once.Do(func() {
		if i.q != nil {
			i.q.stop()
		}
		if err != nil {
			i.bodyW.CloseWithError(err)
		} else {
			i.bodyW.Close()
		}
	})
}

func newClientSession(conn *websocket.Conn, localURL string, log *slog.Logger) *clientSession {
	return &clientSession{
		certReply:     make(chan protocol.DeviceCert, 1),
		postureKick:   make(chan struct{}, 1),
		conn:          conn,
		log:           log,
		localURL:      strings.TrimSuffix(localURL, "/"),
		inflight:      make(map[uint64]*inflight),
		targets:       make(map[string]string),
		wsConns:       make(map[uint64]net.Conn),
		streamConns:   make(map[uint64]net.Conn),
		streamTunnels: make(map[uint64]string),
		windows:       newWindowTable(),
		http: &http.Client{
			// Yonlendirmeleri TAKIP ETME: 301/302 oldugu gibi tarayiciya
			// donmeli, yoksa yerel servisin yonlendirme mantigi bozulur.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			// Genel Timeout YOK: http.Client.Timeout govde okumayi da kapsar ve uzun
			// yanitlari/akislari (SSE, buyuk indirme) 25 sn'de keser. Bunun yerine
			// baslik bekleme + dial zaman asimi ve govde bosta zaman asimi (idleBody).
			Transport: &http.Transport{
				DialContext:           safeDialContext,
				MaxIdleConns:          128,
				MaxIdleConnsPerHost:   64,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: localTimeout,
			},
		},
	}
}

// --- yazma ----------------------------------------------------------------

// connWriteCtx, sunucu baglantisina yazim baglami. Istek baglami yazim
// SIRASINDA iptal edilirse coder/websocket tum baglantiyi kapatir (tek istegin
// iptali tuneli dusurur); zaten iptal edilmisse yazmadan doner, aksi halde
// iptali yazima tasimaz, yalniz zaman asimi uygular.
func connWriteCtx(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), d)
	return c, cancel, nil
}

func (cs *clientSession) sendControl(ctx context.Context, v any, t protocol.MessageType) error {
	data, err := protocol.Marshal(v, t)
	if err != nil {
		return err
	}
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()

	wctx, cancel, err := connWriteCtx(ctx, 10*time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	return cs.conn.Write(wctx, websocket.MessageText, data)
}

// sendBinary, hazir kodlanmis ikili mesaji (screen2 kareleri) gonderir.
func (cs *clientSession) sendBinary(ctx context.Context, b []byte) error {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()

	wctx, cancel, err := connWriteCtx(ctx, 15*time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	return cs.conn.Write(wctx, websocket.MessageBinary, b)
}

func (cs *clientSession) sendFrame(ctx context.Context, f protocol.BodyFrame) error {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()

	wctx, cancel, err := connWriteCtx(ctx, 30*time.Second)
	if err != nil {
		return err
	}
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

	fl.tunnelID = req.TunnelID
	if !req.HasBody {
		fl.closeBody(nil)
	} else {
		fl.q = newWriteQueue(func(b []byte, d time.Duration) error {
			// Pipe yazimi yerel servis okuyana kadar bloklar: zaman asiminda okuyucuyu kapat.
			t := time.AfterFunc(d, func() { pr.CloseWithError(errWriteStalled) })
			_, err := pw.Write(b)
			t.Stop()
			return err
		}, func() { fl.closeBody(nil) }, func(err error) {
			if errors.Is(err, io.ErrClosedPipe) {
				// Yerel servis govdeyi okumayi birakti (erken yanit, or. 413): normal;
				// yanit akmaya devam eder, yalniz govde yazimi durur.
				return
			}
			msg := "istek govdesi yerel servise iletilemedi: " + err.Error()
			fl.failMsg.Store(&msg)
			cs.log.Warn("istek govdesi kuyrugu basarisiz, istek iptal ediliyor", "req_id", req.ReqID, "hata", err)
			fl.cancel()
			fl.closeBody(err)
		})
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
	cs.tm.request(req.TunnelID)
	base := cs.targetFor(req.TunnelID)
	if base == "" {
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, "bu tunel icin yerel hedef tanimli degil")
		return
	}
	if base == zappUnavailable {
		cs.respondAppUnavailable(ctx, req, start)
		return
	}
	if !validLocalPath(req.Path) {
		cs.log.Warn("gecersiz istek yolu reddedildi", "req_id", req.ReqID)
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, "gecersiz istek yolu")
		return
	}
	target := base + req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	u, err := url.Parse(target)
	if err != nil {
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, "gecersiz hedef URL: "+err.Error())
		return
	}
	if bu, berr := url.Parse(base); berr != nil || u.Host != bu.Host {
		cs.log.Warn("istek yolu yerel hedef host'unu degistiriyor", "req_id", req.ReqID)
		cs.sendError(ctx, req.ReqID, protocol.CodeLocalUnreachable, "gecersiz istek yolu")
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
	// Yerel servise HTTP surumu: gRPC (ve h2/h2c zorlamasi) icin HTTP/2.
	grpc := isGRPCRequest(hreq.Header)
	client, useH2 := cs.originClient(u.Scheme, grpc)
	if useH2 && (grpc || req.TETrailers) {
		// gRPC sunucularinin bekledigi; yalniz "trailers" gecerli TE degeridir.
		hreq.Header.Set("Te", "trailers")
	}
	// Govde uzunlugu biliniyorsa Content-Length ile gonder (chunked yerine).
	// Aksi halde bilinmeyen-uzunlukta io.Reader Go'yu Transfer-Encoding: chunked'a
	// zorlar ve Content-Length bekleyen backend'ler bos govde gorur.
	// http.Client, Content-Length'i Header'dan degil hreq.ContentLength alanindan
	// yazar; bu yuzden alani acikca ayarliyoruz ve olasi cift basligi temizliyoruz.
	if req.HasBody && req.ContentLength > 0 {
		hreq.ContentLength = req.ContentLength
		hreq.Header.Del("Content-Length")
	}
	// Host basligini yerel servise oldugu gibi tasima: yerel servis kendi
	// adresini beklemeli. Orijinal hostname X-Forwarded-Host'ta zaten var.
	hreq.Host = ""

	resp, err := client.Do(hreq)
	if err != nil {
		duration := time.Since(start)
		if cs.onRequest != nil {
			cs.onRequest(req.Method, req.Path, 502, duration)
		}
		cs.tm.addError(req.TunnelID)
		code, msg := classifyLocalError(err)
		if fm := fl.failMsg.Load(); fm != nil {
			code, msg = protocol.CodeLocalUnreachable, *fm
		}
		cs.log.Warn("yerel servise ulasilamadi",
			"req_id", req.ReqID, "hedef", target, "hata", err)
		cs.sendError(ctx, req.ReqID, code, msg)
		return
	}
	// Baslik gecikmesi onRequest'e; govde dahil toplam sure olcume (defer).
	headerDur := time.Since(start)
	if cs.onRequest != nil {
		cs.onRequest(req.Method, req.Path, resp.StatusCode, headerDur)
	}
	defer func() { cs.tm.observe(req.TunnelID, time.Since(start)) }()
	if resp.StatusCode >= 500 {
		cs.tm.addError(req.TunnelID)
	}
	defer resp.Body.Close()

	// Akis kontrolu yoksa (eski sunucu) govde kredisiz akar; 32 MB tavani KORU ama
	// uzunluk biliniyorsa baslik gonderilmeden REDDET (basliktan sonra kesmek
	// istemciye bozuk/yarim yanit verirdi).
	if !cs.flowControl && resp.ContentLength > protocol.MaxBodyBytes {
		cs.tm.addError(req.TunnelID)
		cs.sendError(ctx, req.ReqID, protocol.CodeBodyTooLarge, "yerel servis yaniti 32 MB sinirini asti")
		return
	}

	hasBody := resp.ContentLength != 0
	// h2 yanitta trailer olabilir: govde yolu (EOF cercevesi) her zaman acilir ki
	// trailer'lar govdeden once/EOF'ta tasinabilsin (yalniz yetenek pazarlandiysa).
	if cs.httpTrailers && resp.ProtoMajor == 2 {
		hasBody = true
	}
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

	var rbody io.Reader = newIdleBody(resp.Body, respIdleTimeout, fl.cancel)
	if grpc {
		// gRPC akislari uzun sure sessiz kalabilir (abonelik); olu baglantiyi
		// h2 PING denetimi (origin.go) yakalar, bosta zaman asimi uygulanmaz.
		rbody = resp.Body
	}
	var trailers func() http.Header
	if cs.httpTrailers {
		// HTTP/1.1 chunked trailer'lari (Trailer: bildirimli) ve h2 trailer'lari.
		trailers = func() http.Header { return resp.Trailer }
	}
	if err := cs.streamResponseTT(ctx, req.ReqID, req.TunnelID, rbody, trailers); err != nil {
		cs.tm.addError(req.TunnelID)
		cs.log.Warn("yanit govdesi gonderilemedi", "req_id", req.ReqID, "hata", err)
	}
}

// idleBody, her Read icin bosta zaman asimi uygular: sure dolarsa istek baglami
// iptal edilir (alttaki Read hata ile doner). Toplam sureyi SINIRLAMAZ.
type idleBody struct {
	r      io.Reader
	d      time.Duration
	cancel context.CancelFunc
}

func newIdleBody(r io.Reader, d time.Duration, cancel context.CancelFunc) io.Reader {
	return &idleBody{r: r, d: d, cancel: cancel}
}

func (b *idleBody) Read(p []byte) (int, error) {
	t := time.AfterFunc(b.d, b.cancel)
	n, err := b.r.Read(p)
	t.Stop()
	return n, err
}

// streamResponse, olcumsuz kisa yol (apps_zapp.go kullanir).
func (cs *clientSession) streamResponse(ctx context.Context, reqID uint64, body io.Reader) error {
	return cs.streamResponseT(ctx, reqID, "", body)
}

func (cs *clientSession) streamResponseT(ctx context.Context, reqID uint64, tunnelID string, body io.Reader) error {
	return cs.streamResponseTT(ctx, reqID, tunnelID, body, nil)
}

// streamResponseTT, streamResponseT'nin trailer destekli halidir: trailers
// nil degilse govde EOF'a ulasinca cagrilir ve doluysa EOF cercevesinden
// ONCE http_trailers kontrol mesaji olarak yollanir.
func (cs *clientSession) streamResponseTT(ctx context.Context, reqID uint64, tunnelID string, body io.Reader, trailers func() http.Header) error {
	// Istek bitince kredi kaydini temizle (hangi yoldan cikilirsa cikilsin).
	if cs.flowControl {
		defer cs.windows.release(reqID)
	}

	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	var seq uint32
	var total int64

	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			total += int64(n)
			// Kredi yokken (eski sunucu) tavan korunur; akis kontrolu varsa sinir
			// yok (sunucu penceresi hizi zaten sinirlar, bellek buyumez).
			if !cs.flowControl && total > protocol.MaxBodyBytes {
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
			cs.tm.addOut(tunnelID, n)
			seq++
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if trailers != nil {
		if tr := trailers(); len(tr) > 0 {
			if err := cs.sendControl(ctx, protocol.HTTPTrailers{
				Type:     protocol.TypeHTTPTrailers,
				ReqID:    reqID,
				Trailers: tr,
			}, protocol.TypeHTTPTrailers); err != nil {
				return err
			}
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

	if fl.q == nil { // govdesiz istek: gelen govde yok sayilir
		return
	}
	if len(frame.Payload) > 0 {
		cs.tm.addIn(fl.tunnelID, len(frame.Payload))
		// Kuyruga al; BLOKLAMAZ. Tasarsa yalniz bu istek iptal edilir.
		if err := fl.q.push(frame.Payload); err != nil {
			cs.log.Debug("istek govdesi kuyruga alinamadi", "req_id", frame.ReqID, "hata", err)
			return
		}
	}
	if frame.EOF {
		fl.q.finish()
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
	cs.closeAllStreams()
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

// validLocalPath, sunucudan gelen istek yolunun yerel hedef URL'sinin host'unu
// degistirememesini saglar: "/" ile baslamali; "@", "\", sema/host ve kontrol
// karakteri icermemeli.
func validLocalPath(p string) bool {
	if p == "" || p[0] != '/' {
		return false
	}
	// "@" yolda serbesttir (or. /u/@kullanici, /%40scope/paket): yol "/" ile
	// basladigi icin authority'ye karisamaz; asagida doLocal hedef host'un
	// degismedigini ayrica dogrular. Ters egik cizgi ve "://" reddedilir.
	if strings.Contains(p, "\\") || strings.Contains(p, "://") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] <= 0x20 || p[i] == 0x7f {
			return false
		}
	}
	return true
}

// safeDialContext, DNS Rebinding ve Port Scanning saldirilarina karsi
// hem host adi hem de cozumlenen tum IP adreslerini ve portu dogrular.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return guardedDial(ctx, network, addr, true, 0)
}

// streamDialContext, TCP/UDP akislari icin ayni hedef kisitlamasini uygular
// (metadata/link-local, DNS cozumleme sonucu) ancak hassas port listesini
// UYGULAMAZ: RDP/SSH/DB gibi portlara tunelleme akis tunellerinin asil amacidir
// ve hedef kullanicinin kendi tunel tanimindan gelir.
func streamDialContext(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error) {
	return guardedDial(ctx, network, addr, false, timeout)
}

func guardedDial(ctx context.Context, network, addr string, checkPort bool, timeout time.Duration) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	if isBlockedTargetHost(host) {
		return nil, errors.New("SSRF korumasi: yasakli hedef adres: " + host)
	}

	if checkPort {
		p, err := strconv.Atoi(portStr)
		if err == nil && isRestrictedPort(p) {
			return nil, errors.New("port korumasi: hassas servis portuna tunelleme engellendi: " + portStr)
		}
	}

	d := net.Dialer{Timeout: timeout}
	if net.ParseIP(host) != nil {
		return d.DialContext(ctx, network, addr)
	}

	// DNS Rebinding / TOCTOU korumasi: adi BIR kez coz, TUM adresleri denetle ve
	// baglantiyi cozulmus, denetlenmis IP'ye dogrudan kur (ikinci bir cozumleme
	// yok). TLS SNI / Host basligi ust katmanda (http.Transport) orijinal adla kalir.
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("hedef adres cozumlenemedi: %w", err)
	}
	if len(ips) == 0 {
		return nil, errors.New("hedef adres cozumlenemedi: kayit yok")
	}
	for _, ip := range ips {
		if isBlockedIP(ip.IP) {
			return nil, errors.New("SSRF korumasi: hedef adres bulut metadata veya link-local IP'ye cozumleniyor: " + ip.IP.String())
		}
	}
	var lastErr error
	for _, ip := range ips {
		c, derr := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if derr == nil {
			return c, nil
		}
		lastErr = derr
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}
