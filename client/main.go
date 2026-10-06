// Command zorven-client, kullanicinin makinesinde calisan Zorven tunel ajanidir.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var version = "dev"

func main() {
	var (
		server         string
		token          string
		local          string
		insecure       bool
		caCert         string
		logLevel       string
		enableTerminal bool
		enableScreen   bool
		noTerminal     bool
		noScreen       bool
		noAutoUpdate   bool
		printURL       bool

		// ephemeralTTLSec (FAZ 2 / F07): yalnizca "ephemeral" alt komutu doldurur.
		// 0 kalirsa acilan tunel kalicidir.
		ephemeralTTLSec int
	)

	runTunnel := func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			slog.Warn("ayar dosyasi okunamadi, varsayilanlar kullanilacak", "hata", err)
		}

		// 1. Port veya hedef URL argumanini cozumle (or. "8080", "http://localhost:8080")
		// Hedef YALNIZCA kullanici acikca verdiyse (arguman veya --local) belirlenir.
		// Verilmediyse bos kalir: istemci hicbir yerel port icin tunel acmaz,
		// yalnizca sunucuda tanimli tunelleri alir (config'teki eski local_url
		// ve sabit bir varsayilan port KULLANILMAZ).
		target := resolveTarget(local, args)
		if len(args) > 0 && !looksLikeTarget(target) {
			// "zorven versoin" gibi yazim hatasi/bilinmeyen alt komut, "http://versoin"
			// hedefli bir tunel acmasin.
			return fmt.Errorf("gecersiz hedef %q: bir port (8080), host:port veya URL verin; komutlar icin 'zorven --help'", strings.Join(args, " "))
		}

		// 2. Token Cozumleme: Bayrak > Ortam Degiskeni > Config > Etkilesimli Sorma
		activeToken := strings.TrimSpace(token)
		if activeToken == "" {
			activeToken = strings.TrimSpace(os.Getenv("ZORVEN_TOKEN"))
		}
		if activeToken == "" {
			activeToken = strings.TrimSpace(os.Getenv("ZORVEN_TOKEN"))
		}
		if activeToken == "" {
			activeToken = strings.TrimSpace(cfg.Token)
		}

		// Eger token hala bos ise terminalde kullaniciya sor
		if activeToken == "" {
			activeToken = promptTokenInteractive()
			if activeToken == "" {
				return fmt.Errorf("istemci token'i bulunamadi.\nToken eklemek icin:\n  zorven authtoken <TOKEN>\nveya --token bayragini kullanin")
			}
			// Basariyla girildiyse ayar dosyasina kaydet
			if err := config.SetToken(activeToken); err == nil {
				cfgPath, _ := config.Path()
				fmt.Printf("✓ Token basariyla kaydedildi: %s\n\n", cfgPath)
			}
		}

		// 3. Sunucu Adresi Cozumleme
		activeServer := strings.TrimSpace(server)
		if activeServer == "" {
			activeServer = strings.TrimSpace(os.Getenv("ZORVEN_SERVER"))
		}
		if activeServer == "" {
			activeServer = strings.TrimSpace(cfg.ServerAddr)
		}
		if activeServer == "" || activeServer == "localhost:8443" {
			activeServer = "zorven.app:443"
		}

		// 4. CA Sertifikasi ve Insecure Cozumleme
		activeInsecure := insecure || cfg.Insecure
		activeCACert := caCert
		if activeCACert == "" {
			activeCACert = cfg.CACertPath
		}
		// Lokal gelistirme kolayligi: server/certs/server.crt varsa yalnizca localhost icin otomatik kullan
		if activeCACert == "" && !activeInsecure && (strings.Contains(activeServer, "localhost") || strings.Contains(activeServer, "127.0.0.1")) {
			if _, err := os.Stat("server/certs/server.crt"); err == nil {
				activeCACert = "server/certs/server.crt"
			}
		}

		// 5. Guvenlik Kuralı: CLI ile calisirken Uzaktan Erisim (Terminal ve Ekran Paylasimi) VARSAYILAN OLARAK KAPALIDIR.
		// Yalnizca kullanici --enable-terminal veya --enable-screen bayraklarini verirse acilir.
		finalNoTerminal := true
		if enableTerminal && !noTerminal {
			finalNoTerminal = false
		}
		finalNoScreen := true
		if enableScreen && !noScreen {
			finalNoScreen = false
		}

		var lvl slog.Level
		if err := lvl.UnmarshalText([]byte(logLevel)); err != nil {
			lvl = slog.LevelInfo
		}
		// CLI'da temiz bir UI gosterecegimiz icin slog ciktisini debug modunda degilse yutabilir veya hata seviyesine cekebiliriz
		// LevelVar: panelden gelen log_level ayari calisirken uygulanabilsin.
		levelVar := new(slog.LevelVar)
		levelVar.Set(lvl)
		logHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
		log := slog.New(logHandler)

		ag := &agent.Agent{
			ServerAddr:      activeServer,
			Token:           activeToken,
			LocalURL:        target,
			RequestedTarget: target,
			RequestedTTLSec: ephemeralTTLSec,
			Insecure:        activeInsecure,
			CACertPath:      activeCACert,
			Log:             log,
			NoTerminal:      finalNoTerminal,
			NoScreen:        finalNoScreen,
			Version:         version,
			NoAutoUpdate:    noAutoUpdate,
			LogLevel:        levelVar,
		}

		// Canli HTTP isteklerini terminale bas
		ag.OnRequest = func(method, path string, status int, duration time.Duration) {
			statusColor := "\033[32m" // Yesil (2xx)
			if status >= 400 && status < 500 {
				statusColor = "\033[33m" // Sari (4xx)
			} else if status >= 500 {
				statusColor = "\033[31m" // Kirmizi (5xx)
			}
			resetColor := "\033[0m"

			now := time.Now().Format("15:04:05")
			fmt.Printf("  %s  %-6s %-32s %s%d %s%s  (%s)\n",
				now, method, truncatePath(path, 32), statusColor, status, httpStatusText(status), resetColor, duration.Round(time.Millisecond))
		}

		// Baglanti kuruldugunda ngrok-tarzi guzel bir kart ciz
		ag.OnStatus = func(s agent.Status) {
			if s.State == agent.StateConnected {
				if printURL {
					// SDK'lar icin makine-okur cikti: her hostname'i bir satirda yaz.
					for _, tn := range s.Tunnels {
						for _, h := range tn.Hostnames {
							fmt.Printf("zorven-url: https://%s\n", h)
						}
					}
					return
				}
				printDashboard(s, activeServer, target, activeInsecure, finalNoTerminal, finalNoScreen)
			} else if s.State == agent.StateConnecting {
				fmt.Println("⏳ Zorven sunucusuna baglaniliyor (" + activeServer + ")...")
			} else if s.State == agent.StateReconnecting {
				fmt.Println("⚠️  Baglanti koptu, yeniden baglaniliyor...")
			} else if s.State == agent.StateFatal {
				fmt.Printf("❌ Baglanti hatasi: %s\n", s.Message)
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return ag.Run(ctx)
	}

	root := &cobra.Command{
		Use:          "zorven [port]",
		Aliases:      []string{"zorven-client", "rpshell-client"},
		Short:        "Zorven tunel istemcisi — Tek komutla lokal port paylasimi",
		Version:      version,
		Example:      "  zorven 8080\n  zorven http 3000\n  zorven authtoken zrv_live_...\n  zorven --server zorven.app:443 --token zrv_live_...",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE:         runTunnel,
	}

	// Tunel bayraklari: root, http ve ephemeral icin ayni tanim/anlam.
	addTunnelFlags := func(c *cobra.Command) {
		f := c.Flags()
		f.StringVar(&server, "server", "", "tunel sunucusu adresi (host:port, ornek: zorven.app:443)")
		f.StringVar(&token, "token", "", "istemci token'i (zrv_live_...)")
		f.StringVar(&local, "local", "", "acilacak yerel servis adresi veya portu (verilmezse yalnizca sunucudaki tuneller alinir)")
		f.BoolVar(&insecure, "insecure", false, "TLS yerine duz ws:// kullan (yalnizca lokal gelistirme)")
		f.StringVar(&caCert, "ca-cert", "", "self-signed sunucu sertifikasi dosyasi")
		f.StringVar(&logLevel, "log-level", "warn", "debug | info | warn | error")
		f.BoolVar(&enableTerminal, "enable-terminal", false, "uzaktan terminal (PTY) erisimine izin ver (guvenlik nedeniyle varsayilan kapali)")
		f.BoolVar(&enableScreen, "enable-screen", false, "uzaktan ekran paylasimina izin ver (guvenlik nedeniyle varsayilan kapali)")
		f.BoolVar(&noTerminal, "no-terminal", false, "uzaktan terminal erisimini kapat")
		f.BoolVar(&noScreen, "no-screen", false, "uzaktan ekran paylasimini kapat")
		f.BoolVar(&noAutoUpdate, "no-auto-update", false, "otomatik guncellemeyi kapat")
		f.BoolVar(&printURL, "print-url", false, "gosterge yerine baglantida makine-okur 'zorven-url: <url>' satirlari yaz (SDK'lar icin)")
	}
	addTunnelFlags(root)

	// --- Alt Komutlar ---

	// http alt komutu (ngrok tarzı: "zorven http 8080")
	httpCmd := &cobra.Command{
		Use:          "http <port>",
		Short:        "Yerel bir HTTP servisini internete acar (or. zorven http 8080)",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTunnel(cmd, args)
		},
	}
	addTunnelFlags(httpCmd)
	root.AddCommand(httpCmd)

	// ephemeral alt komutu (FAZ 2 / F07): sure dolunca kendini silen tunel.
	// CI/CD, demo ve webhook testi icin; arkada temizlik borcu birakmaz.
	var ttlFlag string
	ephemeralCmd := &cobra.Command{
		Use:          "ephemeral <port>",
		Aliases:      []string{"temp", "tmp"},
		Short:        "Suresi dolunca kendini silen gecici tunel acar (or. zorven ephemeral 3000)",
		Example:      "  zorven ephemeral 3000\n  zorven ephemeral 3000 --ttl 30m",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := time.ParseDuration(ttlFlag)
			if err != nil {
				return fmt.Errorf("gecersiz --ttl degeri %q: 2h, 30m gibi bir sure verin", ttlFlag)
			}
			// Sinirlar sunucuda da uygulanir; burada erken ve anlasilir hata
			// vermek, baglandiktan sonra sessizce baska bir sure almaktan iyi.
			if d < time.Minute || d > 24*time.Hour {
				return fmt.Errorf("--ttl 1 dakika ile 24 saat arasinda olmali (verilen: %s)", d)
			}
			ephemeralTTLSec = int(d.Seconds())
			fmt.Printf("Gecici tunel: %s sonra kendiliginden kaldirilacak.\n", d)
			return runTunnel(cmd, args)
		},
	}
	addTunnelFlags(ephemeralCmd)
	ephemeralCmd.Flags().StringVar(&ttlFlag, "ttl", "2h", "tunelin omru (1m - 24h arasi, or. 30m)")
	root.AddCommand(ephemeralCmd)

	// authtoken alt komutu (ngrok tarzı: "zorven authtoken <TOKEN>")
	authtokenCmd := &cobra.Command{
		Use:          "authtoken <token>",
		Aliases:      []string{"token"},
		Short:        "İstemci token'ini yerel yapilandirmaya kaydeder",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimSpace(args[0])
			if !strings.HasPrefix(t, "zrv_live_") && !strings.HasPrefix(t, "rpsh_live_") {
				return fmt.Errorf("gecersiz token formati: token 'zrv_live_' ile baslamalidir")
			}
			if err := config.SetToken(t); err != nil {
				return fmt.Errorf("token kaydedilemedi: %w", err)
			}
			p, _ := config.Path()
			fmt.Printf("✓ Zorven istemci token'i kaydedildi: %s\n", p)
			return nil
		},
	}
	root.AddCommand(authtokenCmd)

	// config alt komut grubu
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "İstemci yapilandirmasini goruntuler veya ayarlar",
	}

	configShowCmd := &cobra.Command{
		Use:   "show",
		Short: "Gecerli yapilandirma ayarlarini gosterir",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, _ := config.Path()
			fmt.Printf("Ayar Dosyasi: %s\n", p)
			fmt.Printf("Sunucu:        %s\n", cfg.ServerAddr)
			if cfg.Token != "" {
				masked := cfg.Token
				if len(masked) > 16 {
					masked = masked[:12] + "..." + masked[len(masked)-4:]
				}
				fmt.Printf("Token:         %s (kayitli)\n", masked)
			} else {
				fmt.Printf("Token:         [tanimsiz - 'zorven authtoken <TOKEN>' ile ekleyin]\n")
			}
			fmt.Printf("Insecure:      %v\n", cfg.Insecure)
			return nil
		},
	}

	var isSystemServer bool
	configSetServerCmd := &cobra.Command{
		Use:   "set-server <server_addr>",
		Short: "Varsayilan tunel sunucu adresini ayarlar (or. api.zorven.app:8443)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := strings.TrimSpace(args[0])
			if isSystemServer {
				if err := config.SetServerSystem(s); err != nil {
					return err
				}
				fmt.Printf("✓ Sistem sunucu adresi guncellendi (%s): %s\n", config.SystemPath(), s)
				return nil
			}
			if err := config.SetServer(s); err != nil {
				return err
			}
			fmt.Printf("✓ Varsayilan sunucu adresi guncellendi: %s\n", s)
			return nil
		},
	}
	configSetServerCmd.Flags().BoolVar(&isSystemServer, "system", false, "ayari sistem geneline (servis moduna) yazar")

	var isSystemToken bool
	configSetTokenCmd := &cobra.Command{
		Use:   "set-token <token>",
		Short: "İstemci token'ini ayarlar",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimSpace(args[0])
			if !strings.HasPrefix(t, "zrv_live_") && !strings.HasPrefix(t, "rpsh_live_") {
				return fmt.Errorf("gecersiz token formati: token 'zrv_live_' ile baslamalidir")
			}
			if isSystemToken {
				if err := config.SetTokenSystem(t); err != nil {
					return err
				}
				fmt.Printf("✓ Sistem token'i guncellendi (%s)\n", config.SystemPath())
				return nil
			}
			return authtokenCmd.RunE(cmd, args)
		},
	}
	configSetTokenCmd.Flags().BoolVar(&isSystemToken, "system", false, "ayari sistem geneline (servis moduna) yazar")

	configCmd.AddCommand(configShowCmd, configSetServerCmd, configSetTokenCmd)
	root.AddCommand(configCmd)
	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Guncellemeleri kontrol et ve varsa uygula (yeniden baslatir)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			srv := strings.TrimSpace(server)
			if srv == "" {
				srv = strings.TrimSpace(cfg.ServerAddr)
			}
			if srv == "" || srv == "localhost:8443" {
				srv = "zorven.app:443"
			}
			ag := &agent.Agent{
				ServerAddr: srv,
				Insecure:   insecure || cfg.Insecure,
				CACertPath: caCert,
				Log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
				Version:    version,
			}
			if ag.CACertPath == "" {
				ag.CACertPath = cfg.CACertPath
			}
			fmt.Printf("Guncelleme kontrol ediliyor (mevcut surum: %s, sunucu: %s)...\n", version, srv)
			if err := ag.CheckUpdateNow(cmd.Context()); err != nil {
				return err
			}
			fmt.Println("Guncel. (yeni surum yoktu veya guncelleme uygulanip yeniden baslatildi)")
			return nil
		},
	}
	root.AddCommand(updateCmd)

	// version: istemci surumu + protokol surumu (destek/sorun giderme icin).
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Istemci surumunu gosterir",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("zorven", version, "(protokol "+protocol.Version+",", runtime.GOOS+"/"+runtime.GOARCH+")")
		},
	})

	root.AddCommand(newServiceCmd())
	root.AddCommand(newForwardCmd())
	root.AddCommand(newConnectCmd())

	// Ust-seviye "zorven status": istemci ayarlarini ve arka plan servisinin
	// durumunu tek bakista gosterir. (Alt komut olarak tanimli olmasaydi cobra
	// "status" kelimesini port argumani sanip "http://status" tuneli acmaya
	// calisirdi — kullanicinin gordugu hatanin sebebi buydu.)
	statusCmd := &cobra.Command{
		Use:          "status",
		Short:        "İstemci ayarlarini ve arka plan servisinin durumunu gosterir",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			p, _ := config.Path()

			srv := strings.TrimSpace(cfg.ServerAddr)
			if srv == "" || srv == "localhost:8443" {
				srv = "zorven.app:443"
			}

			fmt.Println("Zorven İstemci Durumu")
			fmt.Println("---------------------")
			fmt.Printf("Surum:        %s (protokol %s)\n", version, protocol.Version)
			fmt.Printf("Ayar Dosyasi: %s\n", p)
			fmt.Printf("Sunucu:       %s\n", srv)
			if cfg.Token != "" {
				masked := cfg.Token
				if len(masked) > 16 {
					masked = masked[:12] + "..." + masked[len(masked)-4:]
				}
				fmt.Printf("Token:        %s (kayitli)\n", masked)
			} else {
				fmt.Println("Token:        [tanimsiz — 'zorven authtoken <TOKEN>' ile ekleyin]")
			}
			fmt.Println("Yerel Hedef:  (yok - yalnizca sunucuda tanimli tuneller; port icin: zorven <port>)")

			fmt.Print("Servis:       ")
			s, err := getService()
			if err != nil {
				fmt.Printf("Bilinmiyor (%v)\n", err)
				return nil
			}
			st, err := s.Status()
			if err != nil {
				fmt.Println("Kurulu degil (arka plan servisi yok)")
				return nil
			}
			switch st {
			case service.StatusRunning:
				fmt.Println("Calisiyor (Running)")
			case service.StatusStopped:
				fmt.Println("Durduruldu (Stopped)")
			default:
				fmt.Printf("%d\n", st)
			}
			return nil
		},
	}
	root.AddCommand(statusCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// promptTokenInteractive, token tanimli degilse terminalden kullaniciya sorar.
func promptTokenInteractive() string {
	fmt.Println("\n==================================================================")
	fmt.Println("  ZORVEN — İstemci Token'ı Gerekli")
	fmt.Println("==================================================================")
	fmt.Println("  Bu cihaz için henüz bir Zorven token'ı yapılandırılmamış.")
	fmt.Println("  Zorven Dashboard'undan (İstemciler sekmesi) aldığınız")
	fmt.Println("  istemci token'ınızı girin (örn: zrv_live_...).")
	fmt.Println("==================================================================")
	fmt.Print("  Token: ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "zrv_live_") || strings.HasPrefix(text, "rpsh_live_") {
			return text
		}
		if text != "" {
			fmt.Println("  ⚠️  Uyarı: Token 'zrv_live_' ile başlamalıdır.")
		}
	}
	return ""
}

// parseTarget, verilen argumani (or. "8080", ":3000", "http://localhost:5173") standart HTTP URL'e cevirir.
func resolveTarget(local string, args []string) string {
	if len(args) > 0 {
		raw := strings.TrimSpace(args[0])
		if raw == "http" && len(args) > 1 {
			raw = strings.TrimSpace(args[1])
		}
		if raw == "" {
			return ""
		}
		return parseTarget(raw)
	}
	if local = strings.TrimSpace(local); local != "" {
		return parseTarget(local)
	}
	return ""
}

// looksLikeTarget, cozulmus hedefin gercekten bir port/host:port/URL olup
// olmadigini soyler. Noktasiz ve portsuz tek kelime ("versoin") hedef degildir;
// "localhost" ve IP/alan adlari kabul edilir.
func looksLikeTarget(t string) bool {
	if t == "" {
		return false
	}
	u, err := url.Parse(t)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := u.Hostname()
	return u.Port() != "" || strings.Contains(h, ".") || strings.Contains(h, ":") || h == "localhost"
}

// parseTarget, "8080" / ":8080" / "host:port" / URL girdisini hedef URL'sine cevirir.
func parseTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, ":")
	if _, err := strconv.Atoi(raw); err == nil {
		return "http://localhost:" + raw
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

// printDashboard, tünel başarıyla bağlandığında terminale şık bir durum panosu çizer.
func printDashboard(s agent.Status, server, target string, insecure, noTerm, noScreen bool) {
	scheme := "https"
	if insecure {
		scheme = "http"
	}

	var publicURLs []string
	for _, tn := range s.Tunnels {
		for _, h := range tn.Hostnames {
			publicURLs = append(publicURLs, fmt.Sprintf("%s://%s", scheme, h))
		}
	}

	fmt.Println("\n+-------------------------------------------------------------+")
	fmt.Printf("|  %-59s|\n", "ZORVEN v"+version+" - Guvenli Tunel")
	fmt.Println("+-------------------------------------------------------------+")
	fmt.Printf("|  Durum:       \033[32mCevrimici (Online)\033[0m                            |\n")
	fmt.Printf("|  Sunucu:      %-45s |\n", server)
	fmt.Printf("|  Istemci:     %-45s |\n", s.ClientID)
	if target == "" {
		target = "(sunucudaki tuneller)"
	}
	fmt.Printf("|  Yerel Hedef: %-45s |\n", target)
	fmt.Println("|                                                             |")

	if len(publicURLs) > 0 {
		for _, u := range publicURLs {
			fmt.Printf("|  Genel URL:   \033[36m\033[1m%-45s\033[0m |\n", u)
		}
		fmt.Println("|               (Internet uzerinden erisilebilir)             |")
	} else {
		fmt.Println("|  Genel URL:   [Tunel kaydi bekleniyor...]                   |")
	}

	fmt.Println("|                                                             |")
	if noTerm && noScreen {
		fmt.Println("|  Uzak Erisim: \033[33mKapali (Salt Port Tuneli - Guvenli Mod)\033[0m       |")
	} else {
		fmt.Printf("|  Uzak Erisim: Terminal: %v, Ekran: %v               |\n", !noTerm, !noScreen)
	}
	fmt.Println("|  Durdurmak icin Ctrl+C tuslarina basin                      |")
	fmt.Println("+-------------------------------------------------------------+")
	fmt.Println("\nCanli HTTP Istekleri (Trafik):")
}

func truncatePath(p string, maxLen int) string {
	if len(p) <= maxLen {
		return p
	}
	return p[:maxLen-3] + "..."
}

func httpStatusText(status int) string {
	switch status {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 301:
		return "Moved"
	case 302:
		return "Found"
	case 304:
		return "Not Modified"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 429:
		return "Rate Limited"
	case 500:
		return "Server Error"
	case 502:
		return "Bad Gateway"
	case 504:
		return "Gateway Timeout"
	default:
		return ""
	}
}
