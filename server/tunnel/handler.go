package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/abuse"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
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
			h.Log.Warn("kimlik dogrulama hiz siniri asildi", "ip", ip)
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

	// Ayni istemci ikinci kez baglanamaz (api_contract.md §2: 409).
	// Upgrade'den ONCE kontrol ediyoruz ki duz HTTP hatasi donebilelim.
	if _, already := h.Hub.Get(client.ID); already {
		http.Error(w, "bu istemci zaten bagli", http.StatusConflict)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Tarayici kaynakli baglanti beklemiyoruz; istemci bir CLI.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.Log.Warn("websocket upgrade basarisiz", "hata", err)
		return
	}

	// Okuma sinirini acikca ayarla: varsayilan 32 KB, govde cercevelerimiz
	// icin yetersiz kalir ve baglanti "message too big" ile kopar.
	conn.SetReadLimit(protocol.WSReadLimit)

	sessionID := "ses_" + randomHex(4)
	sess := NewSession(sessionID, client.ID, client.Name, r.RemoteAddr, conn, h.Log)
	sess.TenantID = client.TenantID

	ctx := r.Context()
	if err := h.handshake(ctx, sess); err != nil {
		h.Log.Warn("el sikismasi basarisiz", "client", client.ID, "hata", err)
		conn.Close(websocket.StatusPolicyViolation, "handshake failed")
		return
	}

	if err := h.Hub.Register(sess); err != nil {
		// Upgrade ile handshake arasinda yaris olabilir; ikinci baglantiyi kapat.
		conn.Close(websocket.StatusPolicyViolation, "already connected")
		return
	}
	if h.OnClientConnected != nil {
		h.OnClientConnected(ctx, client.ID)
	}
	defer func() {
		h.Hub.Unregister(sess)
		// Kopus aninda "en son gorulme"yi damgala: cihaz offline oldugunda
		// panelde ne zamandir kayip oldugu gorunsun. Istegin ctx'i iptal olmus
		// olabilecegi icin arka plan ctx kullaniliyor.
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.Store.TouchDeviceSeen(dctx, client.ID); err != nil {
			h.Log.Warn("son gorulme damgalanamadi", "client", client.ID, "hata", err)
		}
		cancel()
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
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		http.Error(w, "Authorization: Bearer <token> gerekli", http.StatusUnauthorized)
		return store.Client{}, false
	}

	tokenID, secret, err := auth.Split(strings.TrimSpace(raw))
	if err != nil {
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}

	client, err := h.Store.GetClientByTokenID(r.Context(), tokenID)
	if err != nil {
		// ZAMANLAMA SIZINTISI ONLEMI: kayit bulunamadiginda hemen donersek,
		// gecerli bir tokenID ile gecersizi yanit suresinden ayirt etmek mumkun olur
		// (gecerlide argon2 calisir, ~10ms; gecersizde 0ms). Sahte bir dogrulama
		// yaparak iki yolun maliyetini esitliyoruz.
		auth.VerifyDummy(secret)
		if !errors.Is(err, store.ErrNotFound) {
			h.Log.Error("istemci sorgulanamadi", "hata", err)
		}
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}

	valid, err := auth.Verify(secret, client.TokenHash)
	if err != nil {
		h.Log.Error("token dogrulanamadi", "client", client.ID, "hata", err)
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}
	if !valid {
		http.Error(w, "gecersiz token", http.StatusUnauthorized)
		return store.Client{}, false
	}
	return client, true
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
	s.Version, s.Platform = hello.ClientVersion, hello.Platform
	s.IsService = hello.IsService
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
	}

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
			if h.Entitlements != nil {
				if err := h.Entitlements.CanCreateTunnel(ctx, s.TenantID); err != nil {
					h.Log.Warn("otomatik tunel olusturulamadi (kota asildi)", "tenant", s.TenantID, "client", s.ClientID, "hata", err)
					canCreate = false
				}
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
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "port"
	}
	return res
}
