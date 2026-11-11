package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tkodcumpeg4/zorven/server/events"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/abuse"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// helloTimeout, upgrade sonrasi hello mesajini bekleme suresi.
// Bagli kalip hicbir sey gondermeyen istemciler kaynak tutmasin.
const helloTimeout = 10 * time.Second

// Handler, GET /_tunnel/v1/connect ucunu karsilar.
type Handler struct {
	Store store.Store
	Hub   *Hub
	Log   *slog.Logger

	// AuthLimiter, IP basina kimlik dogrulama denemelerini sinirlar.
	// nil ise sinirlama yapilmaz (testler icin).
	AuthLimiter *ratelimit.Limiter

	// PlatformDomain, otomatik olusturulan tunellere hostname atamak icin kullanilir.
	PlatformDomain string

	// Entitlements, otomatik tunel acilirken kota kontrolu yapar (nil ise kontrol atlanir).
	Entitlements *entitlements.Service

	// OnTunnelChange, yeni bir tunel olustugunda Ingress yonlendiricisini (Reload) uyarir.
	OnTunnelChange func()

	// OnClientConnected ve OnClientDisconnected, baglanti yasam dongusu icin istege bagli kancalardir.
	OnClientConnected    func(ctx context.Context, clientID string)
	OnClientDisconnected func(ctx context.Context, clientID string)

	// Events (opsiyonel): istemci cevrimici/cevrimdisi oldugunda panele SSE olayi
	// (client.connected / client.disconnected) yayinlar; nil ise yayinlanmaz.
	Events *events.Broker

	// Net, Zorven Net kontrol duzlemi (nil ise wg_net pazarlanmaz).
	Net *netctl.Manager
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Hiz siniri EN BASTA kontrol edilir — kimlik dogrulamasindan ve
	// ozellikle argon2'den ONCE.
	//
	// Neden: argon2 kasitli olarak pahalidir. Sinir dogrulamadan sonra
	// kontrol edilseydi, saldirgan cop token'lar gondererek sunucuya
	// bedavaya argon2 hesaplatir ve CPU'yu tuketebilirdi (DoS).
	if h.AuthLimiter != nil {
		ip := clientIP(r)
		if !h.AuthLimiter.Allow(ip) {
			// Ornekli: her 429 icin bir satir log seli uretiyordu.
			auth.ReportFailure(h.Log, auth.KindAgent, auth.ReasonRateLimited, ip)
			w.Header().Set("Retry-After", strconv.Itoa(int(h.AuthLimiter.RetryAfter().Seconds())))
			http.Error(w, "cok fazla baglanti denemesi", http.StatusTooManyRequests)
			return
		}
	}

	client, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	if v := r.Header.Get("X-Tunnel-Client-Version"); v != "" && !versionCompatible(v) {
		http.Error(w, "uyumsuz istemci surumu, sunucu: "+protocol.Version, http.StatusUpgradeRequired)
		return
	}

	// Ayni istemcinin yeni dogrulanmis baglantisi eskisini DEVRALIR (uyku/ag
	// degisimi sonrasi yari olu soket yuzunden 409 kilitlenmesi olmasin). Yalniz
	// devralmalar ping-pong yapiyorsa (iki canli istemci ayni anahtarla) 429.
	if h.Hub.TakeoverBlocked(client.ID) {
		h.Log.Warn("devralma ping-pong: ayni anahtar birden fazla istemcide", "client", client.ID)
		w.Header().Set("Retry-After", "30")
		http.Error(w, "bu cihaz anahtari ayni anda birden fazla istemcide kullaniliyor", http.StatusTooManyRequests)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Tarayici kaynakli baglanti beklemiyoruz; istemci bir CLI/agent (Origin
		// gondermez) ve kimlik Bearer anahtariyla dogrulanir, bu yuzden Origin
		// denetimi uygulanmaz. (CSWSH riski yok: capraz sitede Authorization
		// basligi eklenemez.)
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.Log.Warn("websocket upgrade basarisiz", "hata", err)
		return
	}
	// Oturum nasil biterse bitsin alttaki TCP kapatilir. Ajan kapanis cercevesi
	// gondermeden giderse (TCP kesme, cokme, kill -9) Read EOF ile doner ama
	// coder/websocket soketi kapatmaz: fd surec omru boyunca acik kaliyordu
	// (O6: 30 dongude 19 -> 42 fd). Close/CloseNow sonrasi ikinci cagri zararsiz.
	defer conn.CloseNow()

	// Okuma sinirini acikca ayarla: varsayilan 32 KB, govde cercevelerimiz
	// icin yetersiz kalir ve baglanti "message too big" ile kopar.
	conn.SetReadLimit(protocol.WSReadLimit)

	sessionID := "ses_" + randomHex(4)
	sess := NewSession(sessionID, client.ID, client.Name, r.RemoteAddr, conn, h.Log)
	sess.TenantID = client.TenantID
	sess.UserID = client.UserID

	sess.onDeviceUpdate = func(ctx context.Context, upd protocol.DeviceUpdate) {
		// Hello ile ayni sinirlar: cihaz guncellemesi de DB'ye yazilir.
		hh := protocol.Hello{Hostname: upd.Hostname, IPs: upd.IPs}
		sanitizeHello(&hh)
		upd.Hostname, upd.IPs = hh.Hostname, hh.IPs
		if upd.Hostname == "" && len(upd.IPs) == 0 {
			return
		}
		if err := h.Store.UpdateDeviceInfo(ctx, sess.ClientID, store.DeviceInfo{Hostname: upd.Hostname, IPs: upd.IPs}); err != nil {
			h.Log.Warn("cihaz guncellemesi kaydedilemedi", "client", sess.ClientID, "hata", err)
		}
	}

	ctx := r.Context()
	if err := h.handshake(ctx, sess); err != nil {
		h.Log.Warn("el sikismasi basarisiz", "client", client.ID, "hata", err)
		conn.Close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}

	old, err := h.Hub.Takeover(sess)
	if err != nil {
		conn.Close(websocket.StatusTryAgainLater, "takeover throttled")
		return
	}
	if old != nil {
		h.Log.Info("eski oturum yeni baglantiyla degistirildi", "client", client.ID, "eski", old.ID, "yeni", sess.ID)
		old.Evict("replaced by newer connection")
	}
	// Baglan/kop sayaclari (dalgalanma alarmi); devralmada da her oturum bir kez sayilir.
	obs.AgentConnected()
	defer obs.AgentDisconnected()
	if h.OnClientConnected != nil {
		h.OnClientConnected(ctx, client.ID)
	}
	if h.Events != nil {
		h.Events.PublishTenant(sess.TenantID, events.TypeClientConnected, map[string]string{"client_id": client.ID})
	}
	defer func() {
		if !h.Hub.Unregister(sess) {
			// Devralindi: istemci hala bagli (yeni oturum); cevrimdisi olayi
			// YAYILMAZ.
			return
		}
		// Kopus aninda "en son gorulme"yi damgala: cihaz offline oldugunda
		// panelde ne zamandir kayip oldugu gorunsun. Istegin ctx'i iptal olmus
		// olabilecegi icin arka plan ctx kullaniliyor.
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.Store.TouchDeviceSeen(dctx, client.ID); err != nil {
			h.Log.Warn("son gorulme damgalanamadi", "client", client.ID, "hata", err)
		}
		cancel()
		if h.Events != nil {
			h.Events.PublishTenant(sess.TenantID, events.TypeClientDisconnected, map[string]string{"client_id": client.ID})
		}
		if h.OnClientDisconnected != nil {
			h.OnClientDisconnected(context.Background(), client.ID)
		}
	}()

	h.Log.Info("istemci baglandi",
		"client", client.ID, "isim", client.Name, "session", sessionID,
		"surum", sess.Version, "platform", sess.Platform, "adres", r.RemoteAddr)

	if err := sess.Run(ctx); err != nil {
		h.Log.Info("oturum sona erdi", "client", client.ID, "hata", err)
	} else {
		h.Log.Info("oturum sona erdi", "client", client.ID)
	}
}

// authenticate, Authorization: Bearer <token> basligini dogrular.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (store.Client, bool) {
	const kind = auth.KindAgent
	ip := clientIP(r)
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		http.Error(w, "Authorization: Bearer <token> gerekli", http.StatusUnauthorized)
		return store.Client{}, false
	}

	tokenID, secret, err := auth.Split(strings.TrimSpace(raw))
	if err != nil {
		auth.ReportFailure(h.Log, kind, auth.ReasonMalformed, ip)
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}

	client, err := h.Store.GetClientByTokenID(r.Context(), tokenID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			// Veritabani hatasi "token gecersiz" DEGILDIR: 401 istemciyi kalici durdurur
			// (FatalSessionError); 503 ile istemci geri cekilip yeniden dener. Hata
			// tokenID'ye bagli olmadigindan sahte argon2 gerekmez. Log ornekli: DB
			// kesintisinde her ajan denemesi bir ERROR satiri uretiyordu.
			auth.ReportStoreError(h.Log, kind, ip, err)
			w.Header().Set("Retry-After", "5")
			http.Error(w, "gecici hata, tekrar deneyin", http.StatusServiceUnavailable)
			return store.Client{}, false
		}
		// ZAMANLAMA SIZINTISI ONLEMI: kayit bulunamadiginda hemen donersek,
		// gecerli bir tokenID ile gecersizi yanit suresinden ayirt etmek mumkun olur
		// (gecerlide argon2 calisir, ~10ms; gecersizde 0ms). Sahte bir dogrulama
		// yaparak iki yolun maliyetini esitliyoruz. Esitlik dolu kapida da korunur:
		// iki yol da 503 doner.
		auth.ReportFailure(h.Log, kind, auth.ReasonUnknown, ip)
		if errors.Is(auth.VerifyDummyContext(r.Context(), secret), auth.ErrBusy) {
			writeAgentBusy(w)
			return store.Client{}, false
		}
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}

	// IP basina siniri (AuthLimiter, ServeHTTP basinda) farkli IP'lerle asan bir
	// sel argon2 kapisinda durur: tepe bellek kapasite x 32 MB. Toplu yeniden
	// baglanmada (sunucu restart) dolu kapi 503 doner; ajan geri cekilip dener.
	valid, err := auth.VerifyCached(r.Context(), "agent:"+client.ID, secret, client.TokenHash)
	if errors.Is(err, auth.ErrBusy) {
		auth.ReportFailure(h.Log, kind, auth.ReasonBusy, ip)
		writeAgentBusy(w)
		return store.Client{}, false
	}
	if err != nil {
		h.Log.Error("token dogrulanamadi", "client", client.ID, "hata", err)
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}
	if !valid {
		auth.ReportFailure(h.Log, kind, auth.ReasonInvalid, ip)
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}
	return client, true
}

// writeAgentBusy, argon2 kapisi doluyken 503 yazar. 401 degil: ajan 401'i
// kalici hata sayar; 503'te geri cekilip yeniden baglanir.
func writeAgentBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	http.Error(w, "sunucu yogun, tekrar deneyin", http.StatusServiceUnavailable)
}

// handshake, hello mesajini bekler ve hello_ack ile yanit verir.
func (h *Handler) handshake(ctx context.Context, s *Session) error {
	hctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()

	typ, data, err := s.conn.Read(hctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return errors.New("ilk mesaj metin cercevesi olmali")
	}

	mt, err := protocol.PeekType(data)
	if err != nil {
		return err
	}
	if mt != protocol.TypeHello {
		return errors.New("ilk mesaj hello olmali, alinan: " + string(mt))
	}

	var hello protocol.Hello
	if err := json.Unmarshal(data, &hello); err != nil {
		return err
	}
	sanitizeHello(&hello)
	// ClientVersion = ikilinin GERCEK surumu (panelde gosterilir); protokol
	// uyumlulugu X-Tunnel-Client-Version basligiyla ayri denetlenir.
	s.Version, s.Platform = hello.ClientVersion, hello.Platform
	s.AppKind = hello.AppKind
	s.IsService = hello.IsService
	s.Shells, s.DefaultShell = sanitizeShells(hello.Shells, hello.DefaultShell)
	if hello.Metrics != nil {
		s.SetMetrics(hello.Metrics)
	}

	// Cihaz bilgisini kalicilastir (FAZ 3 / F14). Basarisiz olursa el sikismasi
	// DURMAZ: cihaz envanteri guzel bir sey ama tunelin calismasinin sarti degil.
	goos, goarch := splitPlatform(hello.Platform)
	if err := h.Store.UpdateDeviceInfo(ctx, s.ClientID, store.DeviceInfo{
		Hostname:     hello.Hostname,
		OS:           goos,
		Arch:         goarch,
		IPs:          hello.IPs,
		AgentVersion: hello.ClientVersion,
		Metrics:      hello.Metrics,
	}); err != nil {
		h.Log.Warn("cihaz bilgisi kaydedilemedi", "client", s.ClientID, "hata", err)
	}

	// Yetenek pazarligi: istemci akis kontrolunu destekliyorsa etkinlestir.
	// Eski istemciler Features gondermez -> flowControl false kalir ve davranis
	// eski surumlerdeki gibi surer (geriye donuk uyumluluk).
	//
	// s.flowControl yalnizca BURADA (el sikisma, tek goroutine) yazilir ve
	// sonrasinda yalnizca okunur; ek kilit gerekmez.
	var enabled []string
	for _, f := range hello.Features {
		if f == protocol.FeatureFlowControl {
			s.flowControl = true
			enabled = append(enabled, protocol.FeatureFlowControl)
		}
		if f == protocol.FeatureHTTPTrailers {
			s.httpTrailers = true
			enabled = append(enabled, protocol.FeatureHTTPTrailers)
		}
		if f == protocol.FeatureScreenInputStatus {
			s.screenInputStatus = true
			enabled = append(enabled, protocol.FeatureScreenInputStatus)
		}
		if f == protocol.FeatureScreen2 {
			s.screen2 = true
			enabled = append(enabled, protocol.FeatureScreen2)
		}
		if f == protocol.FeatureScreen2Layers {
			s.screen2Layers = true
			enabled = append(enabled, protocol.FeatureScreen2Layers)
		}
		if f == protocol.FeatureScreenCursor {
			s.screenCursor = true
			enabled = append(enabled, protocol.FeatureScreenCursor)
		}
		if f == protocol.FeatureScreen2Stats {
			s.screen2Stats = true
			enabled = append(enabled, protocol.FeatureScreen2Stats)
		}
		if f == protocol.FeatureScreen2Feedback {
			s.screen2Feedback = true
			enabled = append(enabled, protocol.FeatureScreen2Feedback)
		}
		if f == protocol.FeatureScreen2Video {
			s.screen2Video = true
			enabled = append(enabled, protocol.FeatureScreen2Video)
		}
		if f == protocol.FeatureScreen2FPS60 {
			s.screen2FPS60 = true
			enabled = append(enabled, protocol.FeatureScreen2FPS60)
		}
		if f == protocol.FeatureScreenFiles {
			s.screenFiles = true
			enabled = append(enabled, protocol.FeatureScreenFiles)
		}
		if f == protocol.FeatureUDPProbe {
			s.SetUDPProbe(true)
			enabled = append(enabled, protocol.FeatureUDPProbe)
		}
		if f == protocol.FeatureDeviceUpdate {
			s.deviceUpdate = true
			enabled = append(enabled, protocol.FeatureDeviceUpdate)
		}
		if f == protocol.FeatureScreenClipboard {
			s.screenClipboard = true
			enabled = append(enabled, protocol.FeatureScreenClipboard)
		}
	}

	enabled = append(enabled, s.negotiateNet(h.Net, hello.Features)...)
	s.features = append([]string(nil), enabled...)

	tunnels, err := h.Store.ListTunnelsByClient(ctx, s.ClientID)
	if err != nil {
		return err
	}
	// Adlar ayri tabloda ve bir tunelin birden cok adi olabilir; tunel basina
	// sorgu (N+1) yerine hepsini tek sorguda alip grupluyoruz.
	names, err := h.Store.ListHostnamesByClient(ctx, s.ClientID)
	if err != nil {
		return err
	}

	reqTarget := normalizeTarget(hello.RequestedTarget)
	if reqTarget != "" && s.TenantID != "" {
		// Gecici tunel istegi (FAZ 2 / F07). Sinirlar SUNUCUDA uygulanir;
		// istemcinin gonderdigi degere oldugu gibi guvenilmez.
		ttl := clampTunnelTTL(hello.RequestedTTLSec)

		var found bool
		for _, t := range tunnels {
			if strings.TrimRight(t.Target, "/") != reqTarget || !t.Enabled {
				continue
			}
			// TTL istenmisse yalnizca GECICI bir tunel yeniden kullanilir.
			// Kalici bir tuneli "gecici" diye devralmak, kullanicinin kalici
			// yayinini sessizce silinebilir yapardi. Tersi de gecerli: TTL
			// istenmemisse gecici bir tunel kalici gibi kullanilmaz.
			if (ttl > 0) != t.Ephemeral {
				continue
			}
			found = true
			break
		}
		if !found {
			canCreate := true
			// Plan limiti: say + ekle bolumu kiraci basina kilitli (O1). API yolu
			// zaten kilitliydi; ajan el sikismasi kilitsiz oldugu icin ayni kiracinin
			// paralel baglanan N cihazi limiti asabiliyordu. Kilit yalniz denetim +
			// ekleme icin tutulur; ad ekleme ve yonlendirme tazeleme disarida.
			unlockQuota := func() {}
			if h.Entitlements != nil {
				unlock, lerr := h.Entitlements.LockQuota(ctx, s.TenantID, entitlements.QuotaTunnels)
				if lerr != nil {
					h.Log.Warn("otomatik tunel olusturulamadi (kota kilidi alinamadi)", "tenant", s.TenantID, "client", s.ClientID, "hata", lerr)
					canCreate = false
				} else {
					unlockQuota = unlock
					if err := h.Entitlements.CanCreateTunnel(ctx, s.TenantID); err != nil {
						h.Log.Warn("otomatik tunel olusturulamadi (kota asildi)", "tenant", s.TenantID, "client", s.ClientID, "hata", err)
						canCreate = false
					}
				}
			}
			if !canCreate {
				unlockQuota()
			}
			if canCreate {
				var newTun store.Tunnel
				var err error
				if ttl > 0 {
					expires := time.Now().UTC().Add(time.Duration(ttl) * time.Second)
					newTun, err = h.Store.CreateEphemeralTunnel(ctx, s.TenantID, s.ClientID, reqTarget, "", expires)
				} else {
					newTun, err = h.Store.CreateTunnel(ctx, s.TenantID, s.ClientID, reqTarget)
				}
				unlockQuota()
				if err != nil {
					h.Log.Warn("otomatik tunel acilamadi", "hata", err)
				} else {
					tunnels = append(tunnels, newTun)
					plat := h.PlatformDomain
					if plat == "" {
						plat = "zorven.app"
					}
					tenantSlug := "default"
					if ten, err := h.Store.GetTenant(ctx, s.TenantID); err == nil && ten.Slug != "" {
						tenantSlug = ten.Slug
					}

					tunnelName := s.ClientName
					if tunnelName == "" {
						tunnelName = "tunnel"
					}
					if u, err := url.Parse(reqTarget); err == nil && u.Port() != "" {
						tunnelName = fmt.Sprintf("%s-%s", tunnelName, u.Port())
					}
					tunnelName = sanitizeTunnelName(tunnelName)
					fqdn := fmt.Sprintf("%s--%s.%s", tunnelName, tenantSlug, plat)

					hname, err := h.Store.AddHostname(ctx, s.TenantID, newTun.ID, fqdn, store.HostTypeScoped)
					if err != nil {
						fqdn = fmt.Sprintf("%s-%s--%s.%s", tunnelName, randomHex(2), tenantSlug, plat)
						hname, _ = h.Store.AddHostname(ctx, s.TenantID, newTun.ID, fqdn, store.HostTypeScoped)
					}
					if hname.FQDN != "" {
						names = append(names, hname)
						// Otomatik tarama: istemci-adı türevli ad phishing örüntüsüne
						// uyuyorsa admin incelemesi için işaretle (NON-BLOCKING).
						if res := abuse.ScanFQDN(hname.FQDN, plat); res.Suspicious {
							h.Log.Warn("otomatik tarama supheli otomatik-ad isaretledi",
								"fqdn", hname.FQDN, "score", res.Score, "reason", res.Reason)
							if _, cerr := h.Store.CreateAbuseReport(ctx, hname.FQDN, res.Reason, "auto-scan"); cerr != nil {
								h.Log.Error("otomatik tarama raporu olusturulamadi", "fqdn", hname.FQDN, "err", cerr)
							}
						}
					}
					if h.OnTunnelChange != nil {
						h.OnTunnelChange()
					}
					h.Log.Info("tek komutla otomatik tunel acildi",
						"client", s.ClientID, "target", reqTarget, "fqdn", fqdn)
				}
			}
		}
	}

	byTunnel := make(map[string][]string, len(tunnels))
	for _, n := range names {
		byTunnel[n.TunnelID] = append(byTunnel[n.TunnelID], n.FQDN)
	}

	specs := make([]protocol.TunnelSpec, 0, len(tunnels))
	for _, t := range tunnels {
		if t.Enabled {
			specs = append(specs, protocol.TunnelSpec{
				ID: t.ID, Hostnames: byTunnel[t.ID], Target: t.Target,
			})
		}
	}

	// Kayitli uzak ayarlar (FAZ 3 / F15). Okunamazsa ajan kendi
	// varsayilanlariyla calisir: ayar okunamamasi baglantiyi engellememeli.
	settings, err := h.Store.GetDeviceConfigByClient(ctx, s.ClientID)
	if err != nil {
		h.Log.Warn("cihaz ayarlari okunamadi, varsayilanlarla devam", "client", s.ClientID, "hata", err)
		settings = nil
	}

	return s.Send(ctx, protocol.HelloAck{
		Type:               protocol.TypeHelloAck,
		ClientID:           s.ClientID,
		SessionID:          s.ID,
		HeartbeatIntervalS: int(HeartbeatInterval / time.Second),
		Tunnels:            specs,
		Features:           enabled,
		Settings:           settings,
	}, protocol.TypeHelloAck)
}

// Hello alan sinirlari (A4 #11). Onceden 500 000 karakterlik client_version
// DB'ye yaziliyor, gecersiz UTF-8 platform kabul ediliyordu. Gercek degerler
// bu sinirlarin cok altinda (surum "0.3.7", platform "windows/amd64");
// asiri uzun deger REDDEDILMEZ, kesilir: bozuk/eski bir istemci baglanmaya
// devam eder, yalniz panelde/DB'de kisaltilmis gorunur.
const (
	maxHelloShort    = 64  // client_version, platform, protocol_version, app_kind
	maxHelloHostname = 255 // DNS ad siniri
	maxHelloIP       = 64  // IPv6 + bolge kimligi icin yeterli
	maxHelloIPs      = 64
	maxHelloPrevNode = 128
	maxHelloTarget   = 2048
	maxHelloFeatures = 128
)

// cleanHelloField, gecersiz UTF-8 dizilerini ve kontrol karakterlerini atar
// (log/panel enjeksiyonu), sonra rune sinirinda en fazla max bayta keser.
func cleanHelloField(s string, max int) string {
	if s == "" {
		return s
	}
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// sanitizeHello, istemciden gelen ve saklanan/loglanan hello alanlarini sinirlar.
func sanitizeHello(h *protocol.Hello) {
	h.ClientVersion = cleanHelloField(h.ClientVersion, maxHelloShort)
	h.Platform = cleanHelloField(h.Platform, maxHelloShort)
	h.ProtocolVersion = cleanHelloField(h.ProtocolVersion, maxHelloShort)
	h.AppKind = cleanHelloField(h.AppKind, maxHelloShort)
	h.Hostname = cleanHelloField(h.Hostname, maxHelloHostname)
	h.PrevNode = cleanHelloField(h.PrevNode, maxHelloPrevNode)
	h.RequestedTarget = cleanHelloField(h.RequestedTarget, maxHelloTarget)
	h.DefaultShell = cleanHelloField(h.DefaultShell, maxHelloShort)
	if len(h.IPs) > maxHelloIPs {
		h.IPs = h.IPs[:maxHelloIPs]
	}
	ips := h.IPs[:0]
	for _, ip := range h.IPs {
		if ip = cleanHelloField(ip, maxHelloIP); ip != "" {
			ips = append(ips, ip)
		}
	}
	h.IPs = ips
	if len(h.Features) > maxHelloFeatures {
		h.Features = h.Features[:maxHelloFeatures]
	}
}

// versionCompatible, MVP'de yalnizca major.minor esitligine bakar.
func versionCompatible(clientVersion string) bool {
	majorMinor := func(v string) string {
		parts := strings.SplitN(v, ".", 3)
		if len(parts) < 2 {
			return v
		}
		return parts[0] + "." + parts[1]
	}
	return majorMinor(clientVersion) == majorMinor(protocol.Version)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("rastgele deger uretilemedi: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// clientIP, hiz sinirlama anahtari olarak kullanilacak istemci adresini doner.
//
// X-Forwarded-For BILEREK kullanilmiyor: bu uc dogrudan internete acik ve
// baslik saldirgan tarafindan uydurulabilir; ona guvenmek hiz sinirlamasini
// tamamen etkisiz kilardi. Sunucu bir ters proxy arkasina alinirsa burasi
// guvenilen proxy listesiyle birlikte yeniden degerlendirilmelidir.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func normalizeTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(raw, ":")
	if _, err := strconv.Atoi(raw); err == nil {
		return "http://localhost:" + raw
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

// Gecici tunel TTL sinirlari (FAZ 2 / F07). REST ucundaki sinirlarla ayni
// olmalidir; iki giris yolu ayni sozlesmeyi uygular.
const (
	minTunnelTTLSec = 60
	maxTunnelTTLSec = 24 * 60 * 60
)

// splitPlatform, "windows/amd64" bicimini isletim sistemi ve mimariye ayirir.
// Bicim beklenmedikse ikisi de bos doner — yanlis bir deger kaydetmektense
// hic kaydetmemek yeglenir (bos alanlar mevcut kaydi ezmez).
func splitPlatform(p string) (goos, goarch string) {
	p = strings.TrimSpace(p)
	os, arch, ok := strings.Cut(p, "/")
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(os), strings.TrimSpace(arch)
}

// clampTunnelTTL, istemciden gelen TTL'i gecerli araliga sikistirir.
// 0 veya negatif => kalici tunel. Araligin disindaki degerler REDDEDILMEZ,
// siniga cekilir: el sikismasi sirasinda hata dondurmek yerine calisir bir
// tunel vermek, tek komutluk akis icin daha iyi bir davranis.
func clampTunnelTTL(sec int) int {
	if sec <= 0 {
		return 0
	}
	if sec < minTunnelTTLSec {
		return minTunnelTTLSec
	}
	if sec > maxTunnelTTLSec {
		return maxTunnelTTLSec
	}
	return sec
}

func sanitizeTunnelName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	res := b.String()
	// Ardisik tireler teke iner: "--" kiraci ayracidir (ad--<slug>), ad
	// kismi bunu iceremez (F-15).
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	res = strings.Trim(res, "-")
	if res == "" {
		return "port"
	}
	return res
}
