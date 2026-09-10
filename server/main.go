// Command zorven-server, tunel sunucusudur.
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/server/api"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/mail"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/session"
	"github.com/tkodcumpeg4/zorven/server/sqliteimport"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tlscert"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var (
	dbDSN    string
	logLevel string

	// tenantID, CLI komutlarinin hangi kiraci adina calisacagi.
	// Kimlik dogrulama henuz kiraci secmiyor (Plan 3); varsayilan kiraci.
	tenantID string
)

func main() {
	root := &cobra.Command{
		Use:          "zorven-server",
		Aliases:      []string{"zorven", "rpshell-server"},
		Short:        "Zorven tunel sunucusu",
		Version:      protocol.Version,
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&dbDSN, "db-dsn", os.Getenv("ZORVEN_DB_DSN"),
		"Postgres DSN (postgres://...); ZORVEN_DB_DSN ortam degiskeniyle de verilebilir")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "debug | info | warn | error")

	root.AddCommand(serveCmd(), clientCmd(), tunnelCmd(), certCmd(), adminKeyCmd(), importCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- serve -----------------------------------------------------------------

func serveCmd() *cobra.Command {
	var addr, tlsCert, tlsKey, httpAddr, acmeEmail string
	var controlHosts []string
	var marketingHosts []string
	var allowInsecure bool
	var adminKey string
	var authRate, reqRate, adminRate float64
	var authBurst, reqBurst, adminBurst int
	var githubClientID, githubClientSecret, githubBaseURL string
	var githubOpenSignup bool
	var githubAllow []string
	var platformDomain string
	var authUpstream string
	var analyticsUpstream string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Tunel sunucusunu baslatir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := newLogger()
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			useTLS := tlsCert != "" && tlsKey != ""

			// TLS FAIL-CLOSED: sertifika verilmediyse sunucu BASLAMAZ.
			//
			// Neden varsayilan olarak zorunlu: tunel trafigi kullanicinin
			// tum HTTP istek/yanitlarini tasir. TLS'siz calismak sessizce
			// guvenli olmayan bir kuruluma yol acardi; hata vermek, kazara
			// sifresiz calismaktan iyidir.
			if !useTLS && !allowInsecure {
				return errors.New(`TLS yapilandirilmadi.

  Sertifika uretin : rpshell-server cert
  Sonra calistirin : rpshell-server serve --tls-cert certs/server.crt --tls-key certs/server.key

Yalnizca lokal gelistirme icin --allow-insecure ile bu kontrolu atlayabilirsiniz.`)
			}

			// Ctrl+C ile duzgun kapanis.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			hub := tunnel.NewHub()

			authLimiter := ratelimit.New(authRate, authBurst)
			defer authLimiter.Close()
			reqLimiter := ratelimit.New(reqRate, reqBurst)
			defer reqLimiter.Close()

			// Yonetim API'si AYRI bir sinirlayici kullanir.
			//
			// Neden auth sinirlayicisi paylasilmaz: o, tunel istemcisinin
			// baglanti denemeleri icin dakikada 10'a ayarli. Dashboard ise
			// sayfa gezinmesi, listeler ve yenilemelerle bundan cok daha fazla
			// istek yapar; paylasilsaydi normal kullanimda 429 alirdi.
			// Yine de sinirsiz degil: admin anahtari da argon2 ile dogrulaniyor.
			adminLimiter := ratelimit.New(adminRate, adminBurst)
			defer adminLimiter.Close()

			// Public kontrol uclari icin (kimlik dogrulamasiz): masaustu cihaz
			// eslestirme (device code/token) ve kurulum scripti/ikili indirmeleri.
			// IP basina comert ama sinirli: device polling ~3sn'de bir, indirmeler
			// seyrek. Amac user_code brute-force ve indirme selini engellemek.
			publicLimiter := ratelimit.New(20, 60)
			defer publicLimiter.Close()

			router := ingress.NewRouter(st)
			if err := router.Reload(cmd.Context()); err != nil {
				return fmt.Errorf("tunel eslestirmeleri yuklenemedi: %w", err)
			}

			requests := reqlog.New(5000)
			broker := events.New()

			// Kalici istek loglari: batch persister Postgres'e yazar. Store
			// desteklemiyorsa ( or. ileride farkli backend) nil kalir.
			var logPersister *reqlog.Persister
			logPersister = reqlog.NewPersister(func(pctx context.Context, batch []reqlog.Entry) error {
				return st.InsertRequestLogs(pctx, batch)
			}, log)
			go logPersister.Run(ctx)

			entitlementSvc := entitlements.NewService(st)
			bandwidthRecorder := bandwidth.NewLocalRecorder(st)
			defer bandwidthRecorder.Close()

			// GitHub OAuth (opsiyonel): bayrak > env. Uc alan da doluysa acilir.
			github := api.GitHubAuth{
				ClientID:     firstNonEmpty(githubClientID, os.Getenv("ZORVEN_GITHUB_CLIENT_ID")),
				ClientSecret: firstNonEmpty(githubClientSecret, os.Getenv("ZORVEN_GITHUB_CLIENT_SECRET")),
				BaseURL:      firstNonEmpty(githubBaseURL, os.Getenv("ZORVEN_GITHUB_BASE_URL")),
				Allow:        parseAllowList(githubAllow, os.Getenv("ZORVEN_GITHUB_ALLOW")),
			}

			// Oturum yoneticisi: imzali cerezler icin gizli anahtari settings'ten
			// yukle; yoksa bir kez uret ve kaydet (yeniden baslatmalarda oturumlar korunur).
			sessions, err := resolveSessionManager(cmd.Context(), st)
			if err != nil {
				return fmt.Errorf("oturum gizli anahtari cozulemedi: %w", err)
			}
			if github.Enabled() {
				log.Info("GitHub ile giris ACIK", "izinli_kullanici_sayisi", len(github.Allow))
				if github.OpenSignup {
					log.Warn("GitHub ACIK KAYIT etkin — GitHub hesabi olan herkes " +
						"kaydolabilir ve kendi kiracisini alir")
				}
			}

			apiSrv := &api.Server{
				Store: st, Hub: hub, Log: requests, Events: broker,
				Logger: log, Version: protocol.Version,
				GitHub: github, Sessions: sessions,
				Tickets:      api.NewTicketStore(),
				Entitlements: entitlementSvc,
				PlatformDomain: strings.ToLower(strings.TrimSpace(
					firstNonEmpty(platformDomain, os.Getenv("ZORVEN_PLATFORM_DOMAIN")))),
				// Tunel degisikliginde yonlendirmeyi HEMEN tazele; yonetim API'si
				// artik ayni surecte oldugu icin 10sn yoklamayi beklemeye gerek yok.
				OnTunnelChange: func() {
					if err := router.Reload(ctx); err != nil {
						log.Warn("tunel eslestirmeleri tazelenemedi", "hata", err)
					}
				},
			}

			// Webmail: ZORVEN_MAIL_DOMAIN ayarliysa gelen mail alicisini baslat ve
			// giden gonderici'yi kur. Bos ise webmail tamamen kapalidir.
			mailDomain := strings.ToLower(strings.TrimSpace(os.Getenv("ZORVEN_MAIL_DOMAIN")))
			if mailDomain != "" {
				relay := firstNonEmpty(os.Getenv("ZORVEN_MAIL_RELAY"), "mail:587")
				inboundAddr := firstNonEmpty(os.Getenv("ZORVEN_MAIL_INBOUND_ADDR"), ":25")
				apiSrv.MailDomain = mailDomain
				apiSrv.MailSender = mail.NewSender(relay, mailDomain)
				// Site sistem posta kutulari (info@, sales@ ... @platformDomain). Owner
				// bunlara gelenleri ten_default'ta gorur ve bunlardan gonderir.
				sysLocalParts := strings.Fields(strings.ReplaceAll(firstNonEmpty(
					os.Getenv("ZORVEN_SYSTEM_MAILBOXES"),
					"info hello contact support sales abuse admin kvkk legal privacy postmaster no-reply",
				), ",", " "))
				if apiSrv.PlatformDomain != "" {
					for _, lp := range sysLocalParts {
						apiSrv.SystemMailboxes = append(apiSrv.SystemMailboxes, lp+"@"+apiSrv.PlatformDomain)
					}
				}
				mailSrv := mail.NewServer(st, mailDomain, apiSrv.PlatformDomain, sysLocalParts, inboundAddr)
				go func() {
					log.Info("webmail: gelen SMTP alicisi baslatildi", "addr", inboundAddr, "domain", mailDomain)
					if err := mailSrv.ListenAndServe(); err != nil {
						log.Warn("webmail: SMTP alicisi durdu", "hata", err)
					}
				}()
				go func() {
					<-ctx.Done()
					_ = mailSrv.Close()
				}()
			}

			tunnelHandler := &tunnel.Handler{
				Store:          st,
				Hub:            hub,
				Log:            log,
				AuthLimiter:    authLimiter,
				PlatformDomain: apiSrv.PlatformDomain,
				Entitlements:   entitlementSvc,
				OnTunnelChange: apiSrv.OnTunnelChange,
			}

			// Kontrol duzlemi: yalnizca control_hostname uzerinden.
			control := http.NewServeMux()
			control.Handle("/_tunnel/v1/connect", tunnelHandler)
			apiRoutes := apiSrv.Routes()
			control.Handle("/api/v1/", apiRoutes)
			control.Handle("/install.sh", apiRoutes)
			control.Handle("/install.ps1", apiRoutes)
			control.Handle("/bin/", apiRoutes)

			// Admin anahtari: bayrak > env > DB > yeni uret (bir kez yazdirilir)
			if adminKey == "" {
				adminKey = os.Getenv("ZORVEN_ADMIN_KEY")
			}
			key, err := api.ResolveAdminKey(cmd.Context(), st, adminKey, log)
			if err != nil {
				return fmt.Errorf("admin anahtari cozulemedi: %w", err)
			}

			var betterAuthVerifier *auth.BetterAuthVerifier
			if pgSt, ok := st.(*pgstore.Store); ok && pgSt.Pool() != nil {
				betterAuthVerifier = auth.NewBetterAuthVerifier(pgSt.Pool(), 30*time.Second)
				apiSrv.BetterAuth = betterAuthVerifier
				log.Info("Better Auth oturum dogrulayici etkinlestirildi")
			}

			ipFilter := ipfilter.NewEngine(st, entitlementSvc)
			if err := ipFilter.Reload(cmd.Context()); err != nil {
				log.Warn("ip izin listesi kurallari ilk yuklemede okunamadi", "hata", err)
			}
			apiSrv.IPFilter = ipFilter

			// /api/v1/* admin anahtariyla korunur; /health ve auth uclari public kalir.
			// GitHub oturum cerezi ve zrv_api_ tokenlari da kabul edilir.
			guarded := &api.Middleware{
				Key:          key,
				Limiter:      adminLimiter,
				Log:          log,
				Next:         control,
				Store:        st,
				Entitlements: entitlementSvc,
				Sessions:     sessions,
				BetterAuth:   betterAuthVerifier,
				PublicPaths: map[string]bool{
					"/api/v1/health":               true,
					"/api/v1/auth/config":          true,
					"/api/v1/auth/github/login":    true,
					"/api/v1/auth/github/callback": true,
					"/api/v1/auth/logout":          true,
				},
			}

			proxy := &ingress.Handler{
				Router: router, Hub: hub, Log: log, ReqLimiter: reqLimiter,
				IPFilter:          ipFilter,
				ReqLog:            requests,
				Events:            broker,
				LogPersister:      logPersister,
				BandwidthRecorder: bandwidthRecorder,
				BandwidthTracker:  bandwidthRecorder,
			}

			// Hostname ayrimi (api_contract.md §0): control_hostname kontrol
			// duzlemine, DIGER TUM hostname'ler tunele gider.
			//
			// Neden path ile degil hostname ile ayiriyoruz: tunellenen servisin
			// kendi /api/v1 uclari olabilir; path ile ayirsaydik onlari ele gecirirdik.
			// Gomulu dashboard SPA'si (public statik). Ayni origin: dashboard,
			// API ve WS hepsi bu sunucudan servis edilir -> dev proxy / wsBase
			// gerekmez, tarayici tek adrese (bu sunucu) baglanir.
			authUpstream = firstNonEmpty(authUpstream, os.Getenv("ZORVEN_AUTH_UPSTREAM"))
			var authProxy http.Handler
			if authUpstream != "" {
				u, err := url.Parse(authUpstream)
				if err != nil {
					return fmt.Errorf("gecersiz auth-upstream adresi: %w", err)
				}
				proxy := httputil.NewSingleHostReverseProxy(u)
				origDirector := proxy.Director
				proxy.Director = func(req *http.Request) {
					origDirector(req)
					req.Host = u.Host
				}
				authProxy = proxy
				log.Info("Better Auth upstream proxy etkin", "upstream", authUpstream)
			}

			// Analytics (self-hosted Umami) upstream: analytics.<domain> HOST'una
			// gelen TUM istekler umami'ye proxy'lenir (Umami v3 alt-yol/basePath'i
			// calisma zamaninda desteklemez; bu yuzden kendi subdomain'inde kok
			// yolda calisir). Orijinal Host korunur ki Next dogru asset uretsin.
			analyticsUpstream = firstNonEmpty(analyticsUpstream, os.Getenv("ZORVEN_ANALYTICS_UPSTREAM"))
			var analyticsProxy http.Handler
			var analyticsHosts []string
			if analyticsUpstream != "" {
				u, err := url.Parse(analyticsUpstream)
				if err != nil {
					return fmt.Errorf("gecersiz analytics-upstream adresi: %w", err)
				}
				analyticsProxy = httputil.NewSingleHostReverseProxy(u) // Host korunur
				if pd := firstNonEmpty(platformDomain, os.Getenv("ZORVEN_PLATFORM_DOMAIN")); pd != "" {
					analyticsHosts = []string{"analytics." + pd}
				}
				log.Info("Analytics (Umami) upstream proxy etkin", "upstream", analyticsUpstream, "hosts", analyticsHosts)
			}

			dash := dashboardHandler()
			marketing := marketingHandler()

			// Tanitim (pazarlama) hostlari: kok domain + www. Bu hostlarda panel
			// yerine tanitim sitesi gosterilir; panel panel.zorven.app'tedir.
			// Bayrak > env; ikisi de bossa platform domaininden turetilir.
			if len(marketingHosts) == 0 {
				if env := strings.TrimSpace(os.Getenv("ZORVEN_MARKETING_HOST")); env != "" {
					for _, h := range strings.Split(env, ",") {
						if h = strings.TrimSpace(h); h != "" {
							marketingHosts = append(marketingHosts, h)
						}
					}
				}
			}
			if pd := firstNonEmpty(platformDomain, os.Getenv("ZORVEN_PLATFORM_DOMAIN")); len(marketingHosts) == 0 && pd != "" {
				marketingHosts = []string{pd, "www." + pd}
			}
			if len(marketingHosts) > 0 {
				log.Info("tanitim sitesi hostlari", "hosts", marketingHosts)
			}

			mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// analytics.<domain>: tum istekler Umami'ye (kok yolda). Control/tunel
				// yonlendirmesinden ONCE, kendi host'unda calisir.
				if analyticsProxy != nil && ingress.HostMatchesAny(r.Host, analyticsHosts) {
					analyticsProxy.ServeHTTP(w, r)
					return
				}
				if ingress.HostMatchesAny(r.Host, controlHosts) || ingress.IsIPHost(r.Host) {
					w.Header().Set("X-Content-Type-Options", "nosniff")
					w.Header().Set("X-Frame-Options", "DENY")
					w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
					switch {
					case strings.HasPrefix(r.URL.Path, "/_tunnel/"):
						// Tunel WSS ucunun KENDI kimlik dogrulamasi var (istemci
						// token'i); admin anahtari middleware'inden gecmemeli.
						control.ServeHTTP(w, r)
					case r.URL.Path == "/install.sh" || r.URL.Path == "/install.ps1" || strings.HasPrefix(r.URL.Path, "/bin/"):
						// Tek komutla kurulum scriptleri ve ikili dagitimi (public).
						// IP basina hiz sinirli: indirme selini engelle.
						if !publicLimiter.Allow(ipOf(r)) {
							tooManyRequests(w)
							return
						}
						control.ServeHTTP(w, r)
					case r.URL.Path == "/api/v1/device/code" || r.URL.Path == "/api/v1/device/token":
						// Masaustu "tarayicidan giris" eslestirmesi: auth'suz (public).
						// device/approve GUARDED kalir (asagidaki /api/v1/ dalinda).
						// IP basina hiz sinirli: user_code brute-force / kod spam'ini engelle.
						if !publicLimiter.Allow(ipOf(r)) {
							tooManyRequests(w)
							return
						}
						apiRoutes.ServeHTTP(w, r)
					case strings.HasPrefix(r.URL.Path, "/api/v1/"):
						guarded.ServeHTTP(w, r)
					case strings.HasPrefix(r.URL.Path, "/api/auth/") && authProxy != nil:
						authProxy.ServeHTTP(w, r)
					case len(marketingHosts) > 0 && ingress.HostMatchesAny(r.Host, marketingHosts):
						// Kok domain / www: herkese acik tanitim sitesi. Public
						// oldugu icin IP basina hiz sinirli (comert; statik + az asset).
						if !publicLimiter.Allow(ipOf(r)) {
							tooManyRequests(w)
							return
						}
						marketing.ServeHTTP(w, r)
					default:
						// Diger control hostlar (panel.zorven.app vb.): panel SPA'si.
						dash.ServeHTTP(w, r)
					}
					return
				}
				proxy.ServeHTTP(w, r)
			})

			srv := &http.Server{
				Addr:              addr,
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
				IdleTimeout:       60 * time.Second,
				MaxHeaderBytes:    1 << 20, // 1 MB baslik siniri (slowloris/DoS korumasi)
				// WriteTimeout YOK: tunel baglantilari uzun omurludur,
				// yazma zaman asimi onlari koparirdi.
			}

			var dualCertMgr *tlscert.DualCertManager
			if useTLS {
				defaultCert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
				if err != nil {
					return fmt.Errorf("TLS sertifikasi yuklenemedi: %w", err)
				}
				var autocertCache autocert.Cache
				if pgSt, ok := st.(*pgstore.Store); ok {
					autocertCache = pgSt.AutocertCache()
				}
				acmeEmail = firstNonEmpty(acmeEmail, os.Getenv("ZORVEN_ACME_EMAIL"))
				platDomain := firstNonEmpty(platformDomain, os.Getenv("ZORVEN_PLATFORM_DOMAIN"))
				dualCertMgr = tlscert.NewDualCertManager(&defaultCert, platDomain, controlHosts, st, acmeEmail, autocertCache)
				srv.TLSConfig = &tls.Config{
					GetCertificate: dualCertMgr.GetCertificate,
					// Custom domain (ozel alan adi) sertifikalari icin TLS-ALPN-01
					// ACME challenge'i: autocert, "acme-tls/1" ALPN'ini gordugunde
					// GetCertificate icinde challenge sertifikasini doner. h2 ve
					// http/1.1 acikca korunur (aksi halde HTTP/2 devre disi kalirdi).
					NextProtos: []string{"h2", "http/1.1", "acme-tls/1"},
				}
			}

			// (ctx yukarida tanimlandi)

			// Tunel eslestirmeleri artik yonetim API'si tarafindan degistigi anda
			// tazeleniyor (api.Server.OnTunnelChange). Yine de dusuk sikliktali bir
			// yoklama tutuluyor: `rpshell-server tunnel add` AYRI bir surecte
			// calisir ve dogrudan veritabanina yazar; o degisikligi yalnizca yoklama
			// yakalar. SQLite WAL es zamanli okuyucu/yaziciya izin verdigi icin guvenli.
			go func() {
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if err := router.Reload(ctx); err != nil {
							log.Warn("tunel eslestirmeleri yenilenemedi", "hata", err)
						}
					}
				}
			}()

			go func() {
				<-ctx.Done()
				log.Info("kapatiliyor...")
				shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				srv.Shutdown(shutCtx)
			}()

			// HTTP -> HTTPS yonlendirmesi (api_contract.md §3).
			// Varsayilan olarak KAPALI: :80 baglamak yonetici yetkisi ister ve
			// lokal gelistirmede gereksizdir. --http-addr ile acikca acilir.
			if httpAddr != "" {
				if !useTLS {
					return fmt.Errorf("--http-addr yonlendirme icindir; --tls-cert ve --tls-key olmadan anlamsiz")
				}
				var redirectHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := "https://" + stripPort(r.Host) + httpsPortSuffix(addr) + r.URL.RequestURI()
					http.Redirect(w, r, target, http.StatusMovedPermanently)
				})
				// Custom domain ACME HTTP-01 challenge'lari :80 uzerinde servis
				// edilmeli. autocert HTTPHandler, /.well-known/acme-challenge/
				// disindaki her istegi redirect handler'ina devreder; challenge
				// isteklerini ise kendisi yanitlar. Bu olmadan Let's Encrypt
				// dogrulamasi 301 https redirect'e takilir ve ozel alan adlari
				// sertifika alamaz (TLS handshake basarisiz olur).
				if dualCertMgr != nil && dualCertMgr.AutocertManager != nil {
					redirectHandler = dualCertMgr.AutocertManager.HTTPHandler(redirectHandler)
				}
				redirect := &http.Server{
					Addr:              httpAddr,
					ReadHeaderTimeout: 10 * time.Second,
					Handler:           redirectHandler,
				}
				go func() {
					log.Info("HTTP yonlendirme sunucusu basliyor", "adres", httpAddr)
					if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
						log.Error("HTTP yonlendirme sunucusu durdu", "hata", err)
					}
				}()
				go func() {
					<-ctx.Done()
					shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					redirect.Shutdown(shutCtx)
				}()
			}
			log.Info("sunucu basliyor", "adres", addr, "tls", useTLS, "depolama", "postgres",
				"control_hosts", controlHosts)
			if !useTLS {
				log.Warn("TLS KAPALI (--allow-insecure) — tunel trafigi SIFRELENMEMIS. " +
					"Yalnizca lokal gelistirme icin.")
			}
			log.Info("hiz sinirlari",
				"auth_sn", authRate, "auth_burst", authBurst,
				"istek_sn", reqRate, "istek_burst", reqBurst,
				"admin_sn", adminRate, "admin_burst", adminBurst)

			var srvErr error
			if useTLS {
				srvErr = srv.ListenAndServeTLS("", "")
			} else {
				srvErr = srv.ListenAndServe()
			}
			if errors.Is(srvErr, http.ErrServerClosed) {
				return nil
			}
			return srvErr
		},
	}

	cmd.Flags().StringVar(&addr, "addr", ":8443", "dinlenecek adres")
	cmd.Flags().StringSliceVar(&controlHosts, "control-host", ingress.DefaultControlHosts,
		"kontrol duzlemi hostname(ler)i; diger TUM hostname'ler tunele yonlendirilir")
	cmd.Flags().StringSliceVar(&marketingHosts, "marketing-host", nil,
		"tanitim sitesinin gosterilecegi host(lar) (or. zorven.app,www.zorven.app); "+
			"bos ise platform domaini + www'dan turetilir (env: ZORVEN_MARKETING_HOST)")
	cmd.Flags().StringVar(&platformDomain, "platform-domain", "",
		"kiraci subdomainlerinin uretilecegi domain (or. zorven.app); "+
			"bos ise subdomain tahsisi kapali")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "TLS sertifika dosyasi")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "TLS anahtar dosyasi")
	cmd.Flags().StringVar(&adminKey, "admin-key", "",
		"admin anahtari; verilmezse ZORVEN_ADMIN_KEY, o da yoksa ilk calistirmada uretilir")
	cmd.Flags().BoolVar(&allowInsecure, "allow-insecure", false,
		"TLS olmadan calismaya izin ver (YALNIZCA lokal gelistirme)")
	cmd.Flags().Float64Var(&authRate, "auth-rate", 10.0/60.0,
		"IP basina saniyedeki kimlik dogrulama denemesi (varsayilan: dakikada 10)")
	cmd.Flags().IntVar(&authBurst, "auth-burst", 10,
		"IP basina ani kimlik dogrulama denemesi kapasitesi")
	cmd.Flags().Float64Var(&adminRate, "admin-rate", 10,
		"yonetim API'si icin IP basina saniyedeki istek siniri")
	cmd.Flags().IntVar(&adminBurst, "admin-burst", 40,
		"yonetim API'si icin ani istek kapasitesi")
	// NOT: Modern web uygulamalari (ozellikle Vite/Nuxt/Next dev sunuculari)
	// tek bir sayfa yuklemesinde yuzlerce modul/asset istegini AYNI ANDA yapar.
	// Dusuk bir burst (eski varsayilan 200) bu istekleri 429 ile reddeder ve
	// site "yarim yuklenir" — hatta 429 govdesi JS modulu olarak parse edilip
	// SyntaxError'a yol acar. Bu yuzden varsayilan cok daha comert tutuldu;
	// amac hala kotu niyetli seli engellemek, mesru bir siteyi degil.
	cmd.Flags().Float64Var(&reqRate, "req-rate", 2000,
		"tunel basina saniyedeki istek siniri")
	cmd.Flags().IntVar(&reqBurst, "req-burst", 4000,
		"tunel basina ani istek kapasitesi")
	cmd.Flags().StringVar(&httpAddr, "http-addr", "",
		"verilirse bu adreste duz HTTP dinlenir ve tum istekler HTTPS'e 301 ile yonlendirilir (or: :80)")
	cmd.Flags().StringVar(&githubClientID, "github-client-id", "",
		"GitHub OAuth App Client ID (env: ZORVEN_GITHUB_CLIENT_ID)")
	cmd.Flags().StringVar(&githubClientSecret, "github-client-secret", "",
		"GitHub OAuth App Client Secret (env: ZORVEN_GITHUB_CLIENT_SECRET)")
	cmd.Flags().StringSliceVar(&githubAllow, "github-allow", nil,
		"GitHub ile girisine izin verilen kullanici adlari (virgulle; env: ZORVEN_GITHUB_ALLOW)")
	cmd.Flags().BoolVar(&githubOpenSignup, "github-open-signup", false,
		"GitHub hesabi olan HERKESIN kaydolmasina izin ver (varsayilan: yalnizca --github-allow listesi)")
	cmd.Flags().StringVar(&githubBaseURL, "github-base-url", "",
		"OAuth callback icin sabit taban URL (or. https://sunucu:8443); bos ise istekten turetilir (env: ZORVEN_GITHUB_BASE_URL)")
	cmd.Flags().StringVar(&authUpstream, "auth-upstream", "",
		"Better Auth (Nitro) sunucu adresi (env: ZORVEN_AUTH_UPSTREAM; or. http://localhost:3000)")
	cmd.Flags().StringVar(&analyticsUpstream, "analytics-upstream", "",
		"Analytics (Umami) sunucu adresi; /_a/* buraya proxy'lenir (env: ZORVEN_ANALYTICS_UPSTREAM; or. http://umami:3000)")
	cmd.Flags().StringVar(&acmeEmail, "acme-email", "",
		"Let's Encrypt / ACME hesabi icin e-posta adresi (custom domainler icin; env: ZORVEN_ACME_EMAIL)")
	return cmd
}

// ipOf, istegin istemci IP'sini (host kismi) doner; hiz sinirlama anahtari olur.
// Sunucu :443'u dogrudan dinledigi icin RemoteAddr gercek istemci IP'sidir.
func ipOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tooManyRequests, 429 yaniti yazar (public uc hiz siniri asildiginda).
func tooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"cok fazla istek, lutfen biraz sonra tekrar deneyin"}}`))
}

// firstNonEmpty, verilen degerlerden ilk bos olmayani doner (bayrak > env icin).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseAllowList, izinli GitHub kullanici adlarini kucuk harfe normalize edip
// kume olarak doner. Hem bayraktan (dilim) hem env'den (virgulle) toplar.
func parseAllowList(flag []string, env string) map[string]bool {
	out := map[string]bool{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			out[s] = true
		}
	}
	for _, v := range flag {
		add(v)
	}
	for _, v := range strings.Split(env, ",") {
		add(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveSessionManager, imzali oturum cerezleri icin gizli anahtari settings'ten
// yukler; yoksa bir kez uretip kaydeder. Boylece sunucu yeniden baslatildiginda
// mevcut oturumlar gecerli kalir (anahtar degismedigi surece).
func resolveSessionManager(ctx context.Context, st store.Store) (*session.Manager, error) {
	secret, err := st.GetSetting(ctx, settingSessionSecret)
	if errors.Is(err, store.ErrNotFound) {
		secret, err = session.GenerateSecret()
		if err != nil {
			return nil, err
		}
		if err := st.SetSetting(ctx, settingSessionSecret, secret); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return nil, fmt.Errorf("oturum gizli anahtari bozuk: %w", err)
	}
	return session.NewManager(raw), nil
}

// settingSessionSecret, oturum imza anahtarinin settings'teki adi.
const settingSessionSecret = "session_secret"

// --- client ----------------------------------------------------------------

// --- import-sqlite -----------------------------------------------------------

// importCmd, eski SQLite veritabanini Postgres'e tasir. TEK SEFERLIKTIR.
//
// Istemci token HASH'LERI ve id'ler AYNEN tasinir; boylece daha once kurulmus
// istemciler yeniden token girmeden calismaya devam eder.
func importCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "import-sqlite",
		Short: "Eski SQLite veritabanini Postgres'e tasir (tek seferlik)",
		Long: `Eski SQLite dosyasindaki istemci, tunel ve ayarlari Postgres'e kopyalar.

Istemci token hash'leri ve id'ler aynen korunur; kurulu istemciler yeniden
yapilandirma gerektirmez. Eski dosya DEGISTIRILMEZ (salt okunur).

Ornek:
  rpshell-server import-sqlite --from rpshell.db     --db-dsn "postgres://rpshell:rpshell@localhost:5432/rpshell"`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := sqliteimport.Read(from)
			if err != nil {
				return err
			}
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			ctx := cmd.Context()
			for _, c := range d.Clients {
				if err := st.ImportClient(ctx, c); err != nil {
					return fmt.Errorf("istemci %s tasinamadi: %w", c.ID, err)
				}
			}
			for _, lt := range d.Tunnels {
				if err := st.ImportTunnel(ctx, lt.Tunnel); err != nil {
					return fmt.Errorf("tunel %s tasinamadi: %w", lt.Tunnel.ID, err)
				}
				// Eski semada hostname tunel satirindaydi; artik ayri tabloda.
				// 'legacy': platform ad kurallari bunlara uygulanmaz, boylece
				// "api.localhost" gibi mevcut adlar gocte kirilmaz.
				if lt.Hostname == "" {
					continue
				}
				_, err := st.AddHostname(ctx, store.DefaultTenantID, lt.Tunnel.ID,
					lt.Hostname, store.HostTypeLegacy)
				if err != nil && !errors.Is(err, store.ErrHostnameTaken) {
					return fmt.Errorf("hostname %s tasinamadi: %w", lt.Hostname, err)
				}
			}
			for k, v := range d.Settings {
				if err := st.SetSetting(ctx, k, v); err != nil {
					return fmt.Errorf("ayar %s tasinamadi: %w", k, err)
				}
			}

			fmt.Printf("Tasindi: %d istemci, %d tunel, %d ayar.\n",
				len(d.Clients), len(d.Tunnels), len(d.Settings))
			fmt.Println("Eski dosya degistirilmedi; dogruladiktan sonra elle silebilirsiniz.")
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "rpshell.db", "eski SQLite dosyasi")
	return cmd
}

func clientCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "Istemci yonetimi"}
	cmd.PersistentFlags().StringVar(&tenantID, "tenant", store.DefaultTenantID,
		"islemin yapilacagi kiraci id'si")

	cmd.AddCommand(&cobra.Command{
		Use:   "add <isim>",
		Short: "Yeni istemci olusturur ve token uretir",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			full, tokenID, hash, err := auth.GenerateClient()
			if err != nil {
				return err
			}
			c, err := st.CreateClient(cmd.Context(), tenantID, args[0], tokenID, hash)
			if err != nil {
				return err
			}

			fmt.Printf("Istemci olusturuldu\n  id   : %s\n  isim : %s\n\n", c.ID, c.Name)
			fmt.Printf("Token (BIR KEZ gosterilir, sunucu yalnizca argon2 hash'ini sakladi):\n\n  %s\n\n", full)
			fmt.Printf("Baglanma komutu:\n  rpshell-client --server localhost:8443 --token %s --local http://localhost:8000\n", full)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Istemcileri listeler",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			list, err := st.ListClients(cmd.Context(), tenantID)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("Kayitli istemci yok.")
				return nil
			}
			fmt.Printf("%-16s %-20s %s\n", "ID", "ISIM", "OLUSTURMA")
			for _, c := range list {
				fmt.Printf("%-16s %-20s %s\n", c.ID, c.Name, c.CreatedAt.Format(time.RFC3339))
			}
			return nil
		},
	})

	return cmd
}

// --- tunnel ----------------------------------------------------------------

func tunnelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tunnel", Short: "Tunel yonetimi"}
	cmd.PersistentFlags().StringVar(&tenantID, "tenant", store.DefaultTenantID,
		"islemin yapilacagi kiraci id'si")

	cmd.AddCommand(&cobra.Command{
		Use:   "add <hostname> <client-id> <target>",
		Short: "Yeni tunel eslestirmesi olusturur",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			t, err := st.CreateTunnel(cmd.Context(), tenantID, args[1], args[2])
			if err != nil {
				return err
			}
			// CLI TAM hostname alir ve 'legacy' olarak baglar: platform
			// domaini burada bilinmez ve "api.localhost" gibi gelistirme
			// adlarinin calismasi gerekir. Platform isim-uzayi kurallari
			// dashboard/API tarafinda uygulanir.
			if _, err := st.AddHostname(cmd.Context(), tenantID, t.ID,
				args[0], store.HostTypeLegacy); err != nil {
				// Adsiz bir tunel geride birakmayalim: bu komutun tum amaci
				// hostname -> hedef eslestirmesi kurmak.
				if delErr := st.DeleteTunnel(cmd.Context(), tenantID, t.ID); delErr != nil {
					fmt.Fprintf(os.Stderr,
						"uyari: ad baglanamadi ve tunel %s temizlenemedi: %v\n", t.ID, delErr)
				}
				if errors.Is(err, store.ErrHostnameTaken) {
					return fmt.Errorf("%s zaten bir tunele bagli", args[0])
				}
				return err
			}
			fmt.Printf("Tunel olusturuldu: %s  %s -> %s\n", t.ID, args[0], t.Target)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Tunelleri listeler",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			list, err := st.ListTunnels(cmd.Context(), tenantID)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Println("Tanimli tunel yok.")
				return nil
			}
			// Adlar ayri tabloda; hepsini tek sorguda alip grupluyoruz.
			names, err := st.ListHostnames(cmd.Context(), tenantID)
			if err != nil {
				return err
			}
			byTunnel := make(map[string][]string, len(list))
			for _, n := range names {
				byTunnel[n.TunnelID] = append(byTunnel[n.TunnelID], n.FQDN)
			}

			fmt.Printf("%-16s %-34s %-16s %-28s %s\n", "ID", "ADLAR", "CLIENT", "HEDEF", "DURUM")
			for _, t := range list {
				durum := "aktif"
				if !t.Enabled {
					durum = "pasif"
				}
				adlar := strings.Join(byTunnel[t.ID], ", ")
				if adlar == "" {
					adlar = "(ad yok)"
				}
				fmt.Printf("%-16s %-34s %-16s %-28s %s\n", t.ID, adlar, t.ClientID, t.Target, durum)
			}
			return nil
		},
	})

	return cmd
}

// --- ortak -----------------------------------------------------------------

// openStore, Postgres baglantisini acar.
//
// SQLITE KALDIRILDI: cok kiracili ve ileride multi-node yon, paylasimli bir
// veritabani gerektiriyor; iki implementasyonu senkron tutmanin bakim maliyeti
// de gerekcesiz kalmisti. Eski SQLite verisi icin "import-sqlite" komutu var.
func openStore(ctx context.Context) (store.Store, error) {
	if dbDSN == "" {
		return nil, errors.New(`veritabani yapilandirilmadi.

  Postgres baslatin : docker compose -f docker-compose.dev.yml up -d
  Sonra calistirin  : --db-dsn "postgres://rpshell:rpshell@localhost:5432/rpshell"
  Ortam degiskeni   : ZORVEN_DB_DSN

Eski SQLite verisini tasimak icin:
  rpshell-server import-sqlite --from rpshell.db --db-dsn "postgres://..."`)
	}
	st, err := pgstore.Open(ctx, dbDSN)
	if err != nil {
		return nil, fmt.Errorf("postgres acilamadi: %w", err)
	}
	return st, nil
}

func newLogger() *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(logLevel)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// --- cert ------------------------------------------------------------------

func certCmd() *cobra.Command {
	var certPath, keyPath string
	var hosts []string

	cmd := &cobra.Command{
		Use:   "cert",
		Short: "Lokal gelistirme icin self-signed TLS sertifikasi uretir",
		Long: `Self-signed sertifika ve anahtar uretir (openssl gerekmez).

Uretilen sertifika kendi kendini imzalar; istemci --ca-cert ile guven
havuzuna ekleyerek --insecure kullanmadan baglanabilir.

UYARI: yalnizca lokal gelistirme icindir. Uretimde Let's Encrypt kullanin (R2).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := tlscert.Generate(hosts, certPath, keyPath); err != nil {
				return err
			}
			gun := int(tlscert.Validity.Hours() / 24)
			fmt.Printf("Sertifika uretildi (%d gun gecerli)\n", gun)
			fmt.Printf("  sertifika : %s\n", certPath)
			fmt.Printf("  anahtar   : %s\n", keyPath)
			fmt.Printf("  hostname  : %s\n\n", strings.Join(hosts, ", "))
			fmt.Printf("Sunucu:\n  rpshell-server serve --tls-cert %s --tls-key %s\n\n", certPath, keyPath)
			fmt.Printf("Istemci:\n  rpshell-client --server localhost:8443 --ca-cert %s --token ...\n", certPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&certPath, "cert", "certs/server.crt", "sertifika cikti yolu")
	cmd.Flags().StringVar(&keyPath, "key", "certs/server.key", "anahtar cikti yolu")
	cmd.Flags().StringSliceVar(&hosts, "host",
		[]string{"localhost", "127.0.0.1", "::1"},
		"sertifikanin kapsayacagi hostname/IP listesi")
	return cmd
}

// stripPort, Host basligindan port kismini atar.
func stripPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.LastIndex(host, "]"); end > 0 {
			return host[:end+1]
		}
	}
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i+1:], ":") {
		return host[:i]
	}
	return host
}

// httpsPortSuffix, standart 443 disinda bir portta dinleniyorsak yonlendirme
// hedefine ":port" ekler. Aksi halde tarayici yanlis porta gider.
func httpsPortSuffix(addr string) string {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return ""
	}
	port := addr[i+1:]
	if port == "" || port == "443" {
		return ""
	}
	return ":" + port
}

// --- admin-key --------------------------------------------------------------

func adminKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin-key",
		Short: "Admin anahtari yonetimi",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "reset",
		Short: "Yeni admin anahtari uretilmesini saglar (istemciler ve tuneller korunur)",
		Long: `Kayitli admin anahtarini siler. Sunucu bir sonraki baslatmada YENI bir
anahtar uretip bir kez ekrana yazdirir.

Anahtarin gizli kismi hicbir yerde saklanmadigi icin kaybedilen bir anahtar geri
getirilemez; tek cozum yenisini uretmektir. Istemciler ve tuneller etkilenmez.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()

			ctx := cmd.Context()
			for _, k := range []string{store.SettingAdminTokenID, store.SettingAdminTokenHash} {
				if err := st.DeleteSetting(ctx, k); err != nil {
					return err
				}
			}

			fmt.Println("Admin anahtari sifirlandi.")
			fmt.Println("Sunucuyu yeniden baslatin; yeni anahtar bir kez ekrana yazdirilacak.")
			return nil
		},
	})
	return cmd
}
