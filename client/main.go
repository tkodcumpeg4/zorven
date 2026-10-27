// Command zorven-client, kullanicinin makinesinde calisan Zorven tunel ajanidir.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/internal/tui"
	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var version = "dev"

func main() {
	// Dil komutlar kurulmadan secilmeli: yardim metinleri (Short) de cevrilir.
	if cfg, err := config.Load(); err == nil {
		i18n.Set(i18n.Detect(cfg.Language))
	}
	var (
		server         string
		token          string
		local          string
		insecure       bool
		caCert         string
		logLevel       string
		enableTerminal bool
		enableScreen   bool
		noClipboard    bool
		noFileTransfer bool
		fileDir        string
		fileMaxMB      int
		noTerminal     bool
		noScreen       bool
		noAutoUpdate   bool
		plainZsh       bool
		plainBash      bool
		printURL       bool
		noNet          bool
		noPosture      bool
		noP2P          bool
		noTUN          bool
		accessSpecs    []string

		// ephemeralTTLSec (FAZ 2 / F07): yalnizca "ephemeral" alt komutu doldurur.
		// 0 kalirsa acilan tunel kalicidir.
		ephemeralTTLSec int
		persistFlag     bool
	)

	runTunnel := func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			slog.Warn("ayar dosyasi okunamadi, varsayilanlar kullanilacak", "hata", err)
		}

		// Calisan bir istemci varsa (on plan veya servis) ikinci baglanti acma
		// (sunucu 409 verir); izin degisikligini yerel kanaldan ona ilet.
		if done, ferr := tryForwardFlags(enableTerminal, enableScreen, noTerminal, noScreen, persistFlag); done {
			return ferr
		}
		// `zorven --access ad=port` (hedef tunel yok): calisan ajana ilet.
		if len(accessSpecs) > 0 && len(args) == 0 && local == "" {
			if done, aerr := accessViaAgent(accessSpecs); done || aerr != nil {
				return aerr
			}
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
			// Tek kelimelik harf dizisi buyuk olasilikla bu surumde olmayan bir
			// alt komuttur (or. yeni surumdeki "up"): guncellemeyi oner.
			if w := args[0]; len(args) <= 2 && isPlainWord(w) {
				return errors.New(i18n.T("main.err.unknown_cmd", w, version))
			}
			return errors.New(i18n.T("main.err.bad_target", strings.Join(args, " ")))
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
				return errors.New(i18n.T("main.err.no_token"))
			}
			// Basariyla girildiyse ayar dosyasina kaydet
			if err := config.SetToken(activeToken); err == nil {
				cfgPath, _ := config.Path()
				fmt.Print(i18n.T("main.msg.token_saved_path", cfgPath), "\n")
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
		if (enableTerminal || cfg.EnableTerminal) && !noTerminal {
			finalNoTerminal = false
		}
		finalNoScreen := true
		if (enableScreen || cfg.EnableScreen) && !noScreen {
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
			ServerAddr:        activeServer,
			Token:             activeToken,
			LocalURL:          target,
			RequestedTarget:   target,
			RequestedTTLSec:   ephemeralTTLSec,
			Insecure:          activeInsecure,
			CACertPath:        activeCACert,
			Log:               log,
			NoTerminal:        finalNoTerminal,
			NoScreen:          finalNoScreen,
			NoClipboard:       noClipboard,
			NoFileTransfer:    noFileTransfer,
			FileDir:           fileDir,
			FileMaxSize:       int64(fileMaxMB) << 20,
			Version:           version,
			NoAutoUpdate:      noAutoUpdate,
			TerminalPlainZsh:  plainZsh,
			TerminalPlainBash: plainBash,
			LogLevel:          levelVar,
			NoNet:             noNet,
			NoPosture:         noPosture,
			NoP2P:             noP2P,
			NoTUN:             noTUN,
		}
		for _, spec := range accessSpecs {
			tgt, loc, ok := strings.Cut(spec, "=")
			if !ok || strings.TrimSpace(tgt) == "" || strings.TrimSpace(loc) == "" {
				return errors.New(i18n.T("main.err.bad_access", spec))
			}
			loc = strings.TrimSpace(loc)
			if !strings.Contains(loc, ":") {
				loc = "127.0.0.1:" + loc
			}
			ag.NetAccess = append(ag.NetAccess, agent.NetAccessSpec{Target: strings.TrimSpace(tgt), Local: loc})
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
				fmt.Println(i18n.T("conn.connecting", activeServer))
			} else if s.State == agent.StateReconnecting {
				fmt.Println(i18n.T("conn.reconnecting"))
			} else if s.State == agent.StateFatal {
				fmt.Println(i18n.T("conn.fatal", s.Message))
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if uep, uerr := foregroundCtlEndpoint(); uerr == nil {
			ag.OnReady = func(c context.Context) { startLocalCtl(c, ag, uep) }
		}
		err = ag.Run(ctx)
		var fe *agent.FatalError
		if errors.As(err, &fe) && strings.Contains(fe.Msg, "409") {
			fmt.Println(i18n.T("conn.409hint"))
		}
		return err
	}

	root := &cobra.Command{
		Use:          "zorven [port]",
		Aliases:      []string{"zorven-client", "rpshell-client"},
		Short:        i18n.T("main.cmd.root"),
		Version:      version,
		Example:      "  zorven 8080\n  zorven http 3000\n  zorven authtoken zrv_live_...\n  zorven --server zorven.app:443 --token zrv_live_...",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE:         runTunnel,
	}

	// Tunel bayraklari: root, http ve ephemeral icin ayni tanim/anlam.
	addTunnelFlags := func(c *cobra.Command) {
		f := c.Flags()
		f.StringVar(&server, "server", "", i18n.T("main.flag.server"))
		f.StringVar(&token, "token", "", i18n.T("main.flag.token"))
		f.StringVar(&local, "local", "", i18n.T("main.flag.local"))
		f.BoolVar(&insecure, "insecure", false, i18n.T("main.flag.insecure"))
		f.StringVar(&caCert, "ca-cert", "", i18n.T("main.flag.ca_cert"))
		f.StringVar(&logLevel, "log-level", "warn", i18n.T("main.flag.log_level"))
		f.BoolVar(&enableTerminal, "enable-terminal", false, i18n.T("main.flag.enable_terminal"))
		f.BoolVar(&enableScreen, "enable-screen", false, i18n.T("main.flag.enable_screen"))
		f.BoolVar(&noTerminal, "no-terminal", false, i18n.T("main.flag.no_terminal"))
		f.BoolVar(&noScreen, "no-screen", false, i18n.T("main.flag.no_screen"))
		f.BoolVar(&noClipboard, "no-clipboard", false, i18n.T("main.flag.no_clipboard"))
		f.BoolVar(&noFileTransfer, "no-file-transfer", false, i18n.T("main.flag.no_file_transfer"))
		f.StringVar(&fileDir, "file-dir", "", i18n.T("main.flag.file_dir"))
		f.IntVar(&fileMaxMB, "file-max-mb", 0, i18n.T("main.flag.file_max_mb"))
		f.BoolVar(&persistFlag, "persist", false, i18n.T("main.flag.persist"))
		f.BoolVar(&noAutoUpdate, "no-auto-update", false, i18n.T("main.flag.no_auto_update"))
		f.BoolVar(&plainZsh, "terminal-plain-zsh", false, i18n.T("main.flag.plain_zsh"))
		f.BoolVar(&plainBash, "terminal-plain-bash", false, i18n.T("main.flag.plain_bash"))
		f.BoolVar(&noNet, "no-net", false, i18n.T("main.flag.no_net"))
		f.BoolVar(&noPosture, "no-posture", false, i18n.T("main.flag.no_posture"))
		f.BoolVar(&noP2P, "no-p2p", false, i18n.T("main.flag.no_p2p"))
		f.BoolVar(&noTUN, "no-tun", false, i18n.T("main.flag.no_tun"))
		f.StringArrayVar(&accessSpecs, "access", nil, i18n.T("main.flag.access"))
		f.BoolVar(&printURL, "print-url", false, i18n.T("main.flag.print_url"))
	}
	addTunnelFlags(root)

	// --- Alt Komutlar ---

	// http alt komutu (ngrok tarzı: "zorven http 8080")
	httpCmd := &cobra.Command{
		Use:          "http <port>",
		Short:        i18n.T("main.cmd.http"),
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
		Short:        i18n.T("main.cmd.ephemeral"),
		Example:      "  zorven ephemeral 3000\n  zorven ephemeral 3000 --ttl 30m",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := time.ParseDuration(ttlFlag)
			if err != nil {
				return errors.New(i18n.T("main.err.bad_ttl", ttlFlag))
			}
			// Sinirlar sunucuda da uygulanir; burada erken ve anlasilir hata
			// vermek, baglandiktan sonra sessizce baska bir sure almaktan iyi.
			if d < time.Minute || d > 24*time.Hour {
				return errors.New(i18n.T("main.err.ttl_range", d))
			}
			ephemeralTTLSec = int(d.Seconds())
			fmt.Print(i18n.T("main.msg.ephemeral", d))
			return runTunnel(cmd, args)
		},
	}
	addTunnelFlags(ephemeralCmd)
	ephemeralCmd.Flags().StringVar(&ttlFlag, "ttl", "2h", i18n.T("main.flag.ttl"))
	root.AddCommand(ephemeralCmd)

	// authtoken alt komutu (ngrok tarzı: "zorven authtoken <TOKEN>")
	authtokenCmd := &cobra.Command{
		Use:          "authtoken <token>",
		Aliases:      []string{"token"},
		Short:        i18n.T("main.cmd.authtoken"),
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimSpace(args[0])
			if !strings.HasPrefix(t, "zrv_live_") && !strings.HasPrefix(t, "rpsh_live_") {
				return errors.New(i18n.T("main.err.bad_token_format"))
			}
			if err := config.SetToken(t); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("main.err.token_save"), err)
			}
			p, _ := config.Path()
			fmt.Print(i18n.T("main.msg.token_saved", p))
			return nil
		},
	}
	root.AddCommand(authtokenCmd)
	root.AddCommand(newCtlCmds()...)

	// config alt komut grubu
	configCmd := &cobra.Command{
		Use:   "config",
		Short: i18n.T("main.cmd.config"),
	}

	configShowCmd := &cobra.Command{
		Use:   "show",
		Short: i18n.T("main.cmd.config_show"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, _ := config.Path()
			row := func(label, value string) { fmt.Println(padLabel(i18n.T(label), 14) + value) }
			row("main.lbl.file", p)
			row("main.lbl.server", cfg.ServerAddr)
			if cfg.Token != "" {
				masked := cfg.Token
				if len(masked) > 16 {
					masked = masked[:12] + "..." + masked[len(masked)-4:]
				}
				row("main.lbl.token", i18n.T("main.val.token_saved", masked))
			} else {
				row("main.lbl.token", i18n.T("main.val.token_none"))
			}
			row("main.lbl.insecure", fmt.Sprint(cfg.Insecure))
			return nil
		},
	}

	var isSystemServer bool
	configSetServerCmd := &cobra.Command{
		Use:   "set-server <server_addr>",
		Short: i18n.T("main.cmd.set_server"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := strings.TrimSpace(args[0])
			if isSystemServer {
				if err := config.SetServerSystem(s); err != nil {
					return err
				}
				fmt.Print(i18n.T("main.msg.sys_server", config.SystemPath(), s))
				return nil
			}
			if err := config.SetServer(s); err != nil {
				return err
			}
			fmt.Print(i18n.T("main.msg.def_server", s))
			return nil
		},
	}
	configSetServerCmd.Flags().BoolVar(&isSystemServer, "system", false, i18n.T("main.flag.system"))

	var isSystemToken bool
	configSetTokenCmd := &cobra.Command{
		Use:   "set-token <token>",
		Short: i18n.T("main.cmd.set_token"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimSpace(args[0])
			if !strings.HasPrefix(t, "zrv_live_") && !strings.HasPrefix(t, "rpsh_live_") {
				return errors.New(i18n.T("main.err.bad_token_format"))
			}
			if isSystemToken {
				if err := config.SetTokenSystem(t); err != nil {
					return err
				}
				fmt.Print(i18n.T("main.msg.sys_token", config.SystemPath()))
				return nil
			}
			return authtokenCmd.RunE(cmd, args)
		},
	}
	configSetTokenCmd.Flags().BoolVar(&isSystemToken, "system", false, i18n.T("main.flag.system"))

	configCmd.AddCommand(configShowCmd, configSetServerCmd, configSetTokenCmd)
	root.AddCommand(configCmd)
	updateCmd := &cobra.Command{
		Use:   "update",
		Short: i18n.T("cmd.update"),
		RunE: func(cmd *cobra.Command, args []string) error {
			lines, err := runUpdate(cmd.Context(), false, server, insecure, caCert)
			for _, l := range lines {
				fmt.Println(l)
			}
			return err
		},
	}
	root.AddCommand(updateCmd)
	root.AddCommand(newSettingsCmd())

	// version: istemci surumu + protokol surumu (destek/sorun giderme icin).
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: i18n.T("main.cmd.version"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("zorven", version, "("+i18n.T("main.val.proto", protocol.Version)+",", runtime.GOOS+"/"+runtime.GOARCH+")")
		},
	})

	root.AddCommand(newServiceCmd())
	root.AddCommand(newScreenHelperCmd()) // gizli: servis modu ekran yardimcisi
	root.AddCommand(newShortcutCmds()...)
	root.AddCommand(newForwardCmd())
	root.AddCommand(newConnectCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newShareCmd())
	root.AddCommand(newAccessCmd())
	root.AddCommand(newNetCmd())

	// Ust-seviye "zorven status": istemci ayarlarini ve arka plan servisinin
	// durumunu tek bakista gosterir. (Alt komut olarak tanimli olmasaydi cobra
	// "status" kelimesini port argumani sanip "http://status" tuneli acmaya
	// calisirdi — kullanicinin gordugu hatanin sebebi buydu.)
	statusCmd := &cobra.Command{
		Use:          "status",
		Short:        i18n.T("main.cmd.status"),
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			p, _ := config.Path()

			srv := strings.TrimSpace(cfg.ServerAddr)
			if srv == "" || srv == "localhost:8443" {
				srv = "zorven.app:443"
			}

			fmt.Println(i18n.T("main.status.title"))
			fmt.Println(strings.Repeat("-", len([]rune(i18n.T("main.status.title")))))
			row := func(label, value string) { fmt.Println(padLabel(i18n.T(label), 14) + value) }
			row("main.lbl.version", i18n.T("main.val.version", version, protocol.Version))
			row("main.lbl.file", p)
			row("main.lbl.server", srv)
			if cfg.Token != "" {
				masked := cfg.Token
				if len(masked) > 16 {
					masked = masked[:12] + "..." + masked[len(masked)-4:]
				}
				row("main.lbl.token", i18n.T("main.val.token_saved", masked))
			} else {
				row("main.lbl.token", i18n.T("main.val.token_none"))
			}
			row("main.lbl.local", i18n.T("main.val.local_none"))

			s, err := getService()
			if err != nil {
				row("main.lbl.service", i18n.T("main.val.svc_unknown", err))
				return nil
			}
			st, err := s.Status()
			if err != nil {
				row("main.lbl.service", i18n.T("main.val.svc_missing"))
				return nil
			}
			switch st {
			case service.StatusRunning:
				row("main.lbl.service", i18n.T("main.val.svc_running"))
			case service.StatusStopped:
				row("main.lbl.service", i18n.T("main.val.svc_stopped"))
			default:
				row("main.lbl.service", fmt.Sprint(int(st)))
			}
			printLegacyHint()
			return nil
		},
	}
	root.AddCommand(statusCmd)

	addPrivilegeFlags(root)
	localizeCobra(root)
	if err := root.Execute(); err != nil {
		pauseIfElevatedFailed(err)
		os.Exit(1)
	}
}

// promptTokenInteractive, token tanimli degilse terminalden kullaniciya sorar.
func promptTokenInteractive() string {
	bar := strings.Repeat("=", 66)
	fmt.Println("\n" + bar)
	fmt.Println("  " + i18n.T("main.prompt.title"))
	fmt.Println(bar)
	fmt.Println("  " + i18n.T("main.prompt.l1"))
	fmt.Println("  " + i18n.T("main.prompt.l2"))
	fmt.Println("  " + i18n.T("main.prompt.l3"))
	fmt.Println(bar)
	fmt.Print("  " + i18n.T("main.prompt.token"))

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(text, "zrv_live_") || strings.HasPrefix(text, "rpsh_live_") {
			return text
		}
		if text != "" {
			fmt.Println("  " + i18n.T("main.prompt.warn"))
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

	const width = 61
	line := func(label, value string) {
		fmt.Println("|  " + padLabel(label, 13) + " " + padVisible(value, width-17) + "|")
	}
	border := "+" + strings.Repeat("-", width) + "+"
	fmt.Println("\n" + border)
	fmt.Println("|  " + padVisible(i18n.T("dash.title", version), width-3) + " |")
	fmt.Println(border)
	line(i18n.T("dash.status"), "\033[32m"+i18n.T("dash.online")+"\033[0m")
	line(i18n.T("dash.server"), server)
	line(i18n.T("dash.client"), s.ClientID)
	if target == "" {
		target = i18n.T("dash.target.none")
	}
	line(i18n.T("dash.target"), target)
	line("", "")
	if len(publicURLs) > 0 {
		for _, u := range publicURLs {
			line(i18n.T("dash.url"), "\033[36m\033[1m"+u+"\033[0m")
		}
		line("", i18n.T("dash.url.note"))
	} else {
		line(i18n.T("dash.url"), i18n.T("dash.url.wait"))
	}
	line("", "")
	if noTerm && noScreen {
		line(i18n.T("dash.remote"), "\033[33m"+i18n.T("dash.remote.off")+"\033[0m")
	} else {
		line(i18n.T("dash.remote"), i18n.T("dash.remote.on", onOffText(!noTerm), onOffText(!noScreen)))
	}
	fmt.Println("|  " + padVisible(i18n.T("dash.stop"), width-3) + " |")
	fmt.Println(border)
	fmt.Println("\n" + i18n.T("dash.live"))
}

// padVisible, ANSI dizilerini ve genis (CJK) karakterleri hesaba katarak
// metni w gorunur sutuna tamamlar; tasarsa oldugu gibi birakir.
func padVisible(s string, w int) string { return tui.PadRight(s, w) }

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

// isPlainWord, yalniz kucuk harf ve tireden olusan (port, nokta, iki nokta
// icermeyen) bir sozcuk mu: bilinmeyen alt komut ipucu icin.
func isPlainWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !(r >= 'a' && r <= 'z') && r != '-' {
			return false
		}
	}
	return true
}

// foregroundCtlEndpoint, on plan ajanin kontrol soketi. Eski surumlerin
// kurdugu systemd birimleri ("ExecStart=zorven --enable-terminal", 'service
// run' degil) ajani root olarak HOME'suz baslatir: kullanici soketi
// acilamaz ve 'zorven status/settings' istemciyi bulamazdi. Servis yoneticisi
// altinda root olarak calisiyorsak servis soketi kullanilir.
func foregroundCtlEndpoint() (localctl.Endpoint, error) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 && !service.Interactive() {
		return localctl.ServiceEndpoint(), nil
	}
	return localctl.UserEndpoint()
}

// runUpdate, 'zorven update' mantigi: diskteki ikiliyi gunceller, ardindan
// arka planda calisan (eski surumdeki) istemciyi de gunceller. quiet=true iken
// hicbir sey yazmaz; sonuc satirlari doner (zorven settings).
func runUpdate(ctx context.Context, quiet bool, server string, insecure bool, caCert string) ([]string, error) {
	var lines []string
	cfg, _ := config.Load()
	srv := strings.TrimSpace(server)
	if srv == "" {
		srv = strings.TrimSpace(cfg.ServerAddr)
	}
	if srv == "" || srv == "localhost:8443" {
		srv = "zorven.app:443"
	}
	logOut := io.Writer(os.Stderr)
	if quiet {
		logOut = io.Discard
	}
	ag := &agent.Agent{
		ServerAddr: srv,
		Insecure:   insecure || cfg.Insecure,
		CACertPath: caCert,
		Log:        slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Version:    version,
	}
	if ag.CACertPath == "" {
		ag.CACertPath = cfg.CACertPath
	}
	if !quiet {
		fmt.Println(i18n.T("upd.checking", version, srv))
	}
	msg, err := ag.CheckUpdateQuiet(ctx)
	if err != nil {
		return lines, fmt.Errorf("%s: %w", i18n.T("upd.failed"), err)
	}
	if msg != "" {
		lines = append(lines, msg)
	}
	// Diskteki ikili guncel; arka planda calisan istemci (servis ya da
	// on plan) eski surumle calisiyor olabilir. Yerel kanaldan bulup
	// ona da guncellemeyi tetikle: panelde eski surum gorunmesin.
	if ep, r, _ := localctl.Find(); ep != nil && r != nil && r.Version != "" && r.Version != version {
		if _, err := localctl.Call(*ep, localctl.Request{Op: "update"}); err != nil {
			lines = append(lines, i18n.T("upd.running.fail", r.Version, err))
			if ep.Service {
				lines = append(lines, i18n.T("upd.running.hint"))
			}
		} else {
			kind := i18n.T("set.run.fg")
			if ep.Service {
				kind = i18n.T("set.run.service")
			}
			lines = append(lines, i18n.T("upd.running.ok", kind, r.Version))
		}
	}
	return lines, nil
}
