package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

type serviceProgram struct {
	cancel context.CancelFunc
}

func (p *serviceProgram) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.run(ctx)
	return nil
}

func (p *serviceProgram) run(ctx context.Context) {
	cfg, err := config.Load()
	if err != nil || cfg.Token == "" {
		slog.Error("servis baslatilamadi: gecerli token bulunamadi", "hata", err)
		return
	}

	serverAddr := cfg.ServerAddr
	if serverAddr == "" || serverAddr == "localhost:8443" {
		serverAddr = "zorven.app:443"
	}

	// Servis HICBIR yerel hedef varsaymaz: yalnizca sunucuda tanimli
	// tunelleri (hello_ack / config_update) isler. RequestedTarget de bos
	// kalir; boylece sunucu bu istemci icin kendiliginden tunel acmaz.

	// Servis modunda stderr hicbir yere gitmez (Windows servisi): kayitlar ayrica
	// dosyaya yazilir ve Go cokme ciktisi (panic/fatal yigini) ayni dosyaya duser.
	logOut := openServiceLog()
	logger := slog.New(slog.NewJSONHandler(logOut, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Izin tavani yalnizca sistem ayar dosyasindaki kalici alanlardan gelir
	// (varsayilan kapali); calisirken `zorven enable ...` ile de degisebilir.
	sysCfg, _ := config.LoadFile(config.SystemPath())

	ag := &agent.Agent{
		ServerAddr: serverAddr,
		Token:      cfg.Token,
		Insecure:   cfg.Insecure,
		CACertPath: cfg.CACertPath,
		Log:        logger,
		IsService:  true,
		Version:    version,
		// Servis modunda varsayilan guvenlik: uzaktan shell ve ekran kapali
		NoTerminal: !sysCfg.EnableTerminal,
		NoScreen:   !sysCfg.EnableScreen,
		// Mod B (TUN) servis modunda acilabilir; ZORVEN_NO_TUN=1 ile kapatilir.
		NoTUN: os.Getenv("ZORVEN_NO_TUN") == "1",
	}
	ag.OnReady = func(c context.Context) { startLocalCtl(c, ag, localctl.ServiceEndpoint()) }

	if err := ag.Run(ctx); err != nil {
		slog.Error("ajan calismasi durdu", "hata", err)
	}
}

func (p *serviceProgram) Stop(s service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func getService() (service.Service, error) {
	svcConfig := &service.Config{
		Name:        "zorven",
		DisplayName: "Zorven Tunnel Service",
		Description: "Zorven ters proxy ve guvenli tunel arka plan servisi.",
		Arguments:   []string{"service", "run"},
		// Oto-update sonrasi/beklenmedik cikista servis yoneticisi ajani
		// yeniden baslatsin: systemd (Restart), launchd (KeepAlive), Windows SCM
		// (OnFailure recovery). Yeni kurulumlarda gecerli olur.
		Option: service.KeyValue{
			"Restart":                "always",
			"KeepAlive":              true,
			"RunAtLoad":              true, // macOS: sistem acilisinda baslat
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"OnFailureResetPeriod":   10,
		},
	}
	for k, v := range platformServiceOptions() {
		svcConfig.Option[k] = v
	}
	prog := &serviceProgram{}
	return service.New(prog, svcConfig)
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: i18n.T("svc.cmd.service"),
	}

	var (
		tokenFlag  string
		serverFlag string
		instTerm   bool
		instScreen bool
	)

	var tokenFileFlag string
	installCmd := &cobra.Command{
		PreRunE: requireAdmin,
		Use:     "install",
		Short:   i18n.T("svc.cmd.install"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tokenFileFlag != "" {
				t, err := readTokenFile(tokenFileFlag)
				if err != nil {
					return err
				}
				tokenFlag = t
			}
			o := installOpts{token: tokenFlag, server: serverFlag, terminal: instTerm, screen: instScreen}
			if lu, ok := detectLegacyUnit(); ok {
				if err := migrateLegacyUnit(lu, o, true); err != nil {
					return err
				}
				return nil
			}
			if err := installService(o); err != nil {
				return err
			}
			fmt.Println(i18n.T("svc.installed"))
			fmt.Println(i18n.T("svc.installed.hint"))
			return nil
		},
	}
	installCmd.Flags().StringVarP(&tokenFlag, "token", "t", "", i18n.T("svc.flag.token"))
	installCmd.Flags().StringVar(&tokenFileFlag, "token-file", "", i18n.T("svc.flag.tokenfile"))
	installCmd.Flags().BoolVar(&instTerm, "enable-terminal", false, i18n.T("svc.flag.terminal.p"))
	installCmd.Flags().BoolVar(&instScreen, "enable-screen", false, i18n.T("svc.flag.screen.p"))
	installCmd.Flags().StringVarP(&serverFlag, "server", "s", "", i18n.T("svc.flag.server"))

	uninstallCmd := &cobra.Command{
		PreRunE: requireAdmin,
		Use:     "uninstall",
		Short:   i18n.T("svc.cmd.uninstall"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			_ = s.Stop()
			if err := s.Uninstall(); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("svc.err.uninstall"), err)
			}
			fmt.Println(i18n.T("svc.uninstalled"))
			return nil
		},
	}

	startCmd := &cobra.Command{
		PreRunE: requireAdmin,
		Use:     "start",
		Short:   i18n.T("svc.cmd.start"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if err := s.Start(); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("svc.err.start"), err)
			}
			fmt.Println(i18n.T("svc.started"))
			return nil
		},
	}

	stopCmd := &cobra.Command{
		PreRunE: requireAdmin,
		Use:     "stop",
		Short:   i18n.T("svc.cmd.stop"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if err := s.Stop(); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("svc.err.stop"), err)
			}
			fmt.Println(i18n.T("svc.stopped"))
			return nil
		},
	}

	restartCmd := &cobra.Command{
		PreRunE: requireAdmin,
		Use:     "restart",
		Short:   i18n.T("svc.cmd.restart"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if err := s.Restart(); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("svc.err.restart"), err)
			}
			fmt.Println(i18n.T("svc.restarted"))
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: i18n.T("svc.cmd.status"),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			st, err := s.Status()
			if err != nil {
				fmt.Println(i18n.T("svc.st.unknown", err))
				return nil
			}
			switch st {
			case service.StatusRunning:
				fmt.Println(i18n.T("svc.st.running"))
			case service.StatusStopped:
				fmt.Println(i18n.T("svc.st.stopped"))
			default:
				fmt.Println(i18n.T("svc.st.other", int(st)))
			}
			printLegacyHint()
			return nil
		},
	}

	runCmd := &cobra.Command{
		Use:    "run",
		Hidden: true, // OS servis yoneticisi tarafindan cagirilir
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			return s.Run()
		},
	}

	cmd.AddCommand(installCmd, uninstallCmd, startCmd, stopCmd, restartCmd, statusCmd, runCmd)
	return cmd
}

// installOpts, servis kurulumunda kalici olarak yazilacak ayarlar.
type installOpts struct {
	token, server    string
	terminal, screen bool
}

// installService, kalici ayarlari sistem dosyasina yazar ve servisi kaydeder.
// Servis yoneticisi (systemd/Windows SCM/launchd) acilista otomatik baslatir.
func installService(o installOpts) error {
	if err := saveServiceOpts(o); err != nil {
		return err
	}
	cfg, _ := config.Load()
	if cfg.Token == "" {
		return errors.New(i18n.T("svc.err.notoken"))
	}
	s, err := getService()
	if err != nil {
		return err
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("svc.err.install"), err)
	}
	ensureWintunBestEffort(cfg)
	return nil
}

// ensureWintunBestEffort, Windows'ta Mod B icin wintun.dll'i (imzali manifest
// dogrulamasiyla) zorven.exe'nin yanina indirir. Basarisizlik kurulumu bozmaz
// (Mod A etkilenmez); `zorven net up` sirasinda yeniden denenir.
func ensureWintunBestEffort(cfg config.Config) {
	if runtime.GOOS != "windows" {
		return
	}
	srv := cfg.ServerAddr
	if sc, err := config.LoadFile(config.SystemPath()); err == nil && sc.ServerAddr != "" {
		srv = sc.ServerAddr
	}
	ag := &agent.Agent{ServerAddr: srv, Insecure: cfg.Insecure, CACertPath: cfg.CACertPath,
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil)), Version: version}
	if err := ag.EnsureWintun(context.Background()); err != nil {
		fmt.Print(i18n.T("svc.warn.wintun", err))
	}
}

// saveServiceOpts, verilen kalici ayarlari sistem ayar dosyasina yazar.
func saveServiceOpts(o installOpts) error {
	if o.token != "" || o.server != "" || o.terminal || o.screen {
		sysCfg, _ := config.LoadFile(config.SystemPath())
		if o.terminal {
			sysCfg.EnableTerminal = true
		}
		if o.screen {
			sysCfg.EnableScreen = true
		}
		if o.token != "" {
			sysCfg.Token = strings.TrimSpace(o.token)
		}
		if o.server != "" {
			sysCfg.ServerAddr = strings.TrimSpace(o.server)
		}
		if err := config.SaveSystem(sysCfg); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("svc.err.savecfg"), err)
		}
	}
	return nil
}

// serviceInstalled, servisin sisteme kayitli olup olmadigini soyler.
func serviceInstalled(s service.Service) bool {
	_, err := s.Status()
	return err == nil || !errors.Is(err, service.ErrNotInstalled)
}

// setAutostart, servisin sistem acilisinda baslamasini acar/kapatir.
func setAutostart(on bool) error {
	switch runtime.GOOS {
	case "linux":
		verb := "disable"
		if on {
			verb = "enable"
		}
		if out, err := exec.Command("systemctl", verb, "zorven").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s zorven: %v: %s", verb, err, strings.TrimSpace(string(out)))
		}
		return nil
	case "windows":
		mode := "demand"
		if on {
			mode = "auto"
		}
		if out, err := exec.Command("sc.exe", "config", "zorven", "start=", mode).CombinedOutput(); err != nil {
			return fmt.Errorf("sc config zorven start= %s: %v: %s", mode, err, strings.TrimSpace(string(out)))
		}
		return nil
	case "darwin":
		w := "-w"
		verb := "unload"
		if on {
			verb = "load"
		}
		plist := "/Library/LaunchDaemons/zorven.plist"
		if out, err := exec.Command("launchctl", verb, w, plist).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl %s %s: %v: %s", verb, plist, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return errors.New(i18n.T("svc.err.autostart.os", runtime.GOOS))
}

// newShortcutCmds, sik kullanilan servis islemleri icin kisa komutlar:
// zorven up / zorven down / zorven autostart on|off.
func newShortcutCmds() []*cobra.Command {
	var o installOpts
	up := &cobra.Command{
		Use:     "up",
		PreRunE: requireAdmin,
		Short:   i18n.T("svc.cmd.up"),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if lu, ok := detectLegacyUnit(); ok && serviceInstalled(s) {
				// Eski birim: yeni bicimle yeniden kur; baslatma asagidaki akista.
				if err := migrateLegacyUnit(lu, o, false); err != nil {
					return err
				}
			} else if serviceInstalled(s) {
				// Kurulu serviste yalniz kalici ayarlari guncelle; yeniden baslatinca gecerli olur.
				if err := saveServiceOpts(o); err != nil {
					return err
				}
			} else if err := installService(o); err != nil {
				return err
			}
			if err := setAutostart(true); err != nil {
				fmt.Println(i18n.T("svc.warn.autostart"), err)
			}
			if st, _ := s.Status(); st == service.StatusRunning {
				if err := s.Restart(); err != nil {
					return fmt.Errorf("%s: %w", i18n.T("svc.err.restart"), err)
				}
				fmt.Println(i18n.T("svc.up.restarted"))
				return nil
			}
			if err := s.Start(); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("svc.err.start.priv"), err)
			}
			fmt.Println(i18n.T("svc.up.running"))
			fmt.Println(i18n.T("svc.up.hint"))
			return nil
		},
	}
	up.Flags().StringVarP(&o.token, "token", "t", "", i18n.T("svc.flag.token"))
	up.Flags().StringVarP(&o.server, "server", "s", "", i18n.T("svc.flag.server443"))
	up.Flags().BoolVar(&o.terminal, "enable-terminal", false, i18n.T("svc.flag.terminal"))
	up.Flags().BoolVar(&o.screen, "enable-screen", false, i18n.T("svc.flag.screen"))

	var remove bool
	down := &cobra.Command{
		Use:     "down",
		PreRunE: requireAdmin,
		Short:   i18n.T("svc.cmd.down"),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			_ = s.Stop()
			if remove {
				if err := s.Uninstall(); err != nil {
					return fmt.Errorf("%s: %w", i18n.T("svc.err.uninstall"), err)
				}
				fmt.Println(i18n.T("svc.down.removed"))
				return nil
			}
			fmt.Println(i18n.T("svc.down.stopped"))
			return nil
		},
	}
	down.Flags().BoolVar(&remove, "remove", false, i18n.T("svc.flag.remove"))

	autostart := &cobra.Command{
		Use:       "autostart on|off",
		PreRunE:   requireAdmin,
		Short:     i18n.T("svc.cmd.autostart"),
		Args:      cobra.ExactValidArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			on := args[0] == "on"
			if err := setAutostart(on); err != nil {
				return err
			}
			if on {
				fmt.Println(i18n.T("svc.autostart.on"))
			} else {
				fmt.Println(i18n.T("svc.autostart.off"))
			}
			return nil
		},
	}
	return []*cobra.Command{up, down, autostart}
}

// readTokenFile, --token-file ile verilen dosyadan token'i okur ve dosyayi
// siler (tek kullanimlik). Token komut satirina hic yazilmaz: diger yerel
// islemler komut satirlarini gorebilir.
func readTokenFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("svc.err.tokenfile.read"), err)
	}
	_ = os.Remove(path)
	t := strings.TrimSpace(string(b))
	if t == "" || len(t) > 512 || strings.ContainsAny(t, " \t\r\n") {
		return "", errors.New(i18n.T("svc.err.tokenfile.invalid"))
	}
	return t, nil
}

// openServiceLog, <sistem ayar dizini>/logs/agent.log dosyasini acar (5 MB'i
// gecerse agent.log.1'e dondurur) ve Go cokme ciktisini oraya yonlendirir.
// Acilamazsa stderr doner.
func openServiceLog() io.Writer {
	dir := localctl.LogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.Stderr
	}
	p := filepath.Join(dir, "agent.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return os.Stderr
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	return io.MultiWriter(os.Stderr, f)
}

// printLegacyHint, eski bicimli systemd birimi varsa yenileme ipucunu yazar.
func printLegacyHint() {
	if _, ok := detectLegacyUnit(); ok {
		fmt.Println(i18n.T("svc.legacy.hint"))
	}
}

// migrateLegacyUnit, eski bicimli birimi (service run argumani yok) guncel
// bicimle yeniden kurar: eski ExecStart'taki --enable-terminal/--enable-screen
// niyeti kalici sistem ayarina tasinir, acilis durumu korunur. startAfter true
// ise servis eskiden calisiyorsa yeniden baslatilir.
func migrateLegacyUnit(lu legacyUnit, o installOpts, startAfter bool) error {
	o.terminal = o.terminal || lu.terminal
	o.screen = o.screen || lu.screen
	if err := saveServiceOpts(o); err != nil {
		return err
	}
	// Kaldirmadan once token'in bulundugundan emin ol: yoksa mevcut birime dokunma.
	if cfg, _ := config.Load(); cfg.Token == "" {
		return errors.New(i18n.T("svc.err.notoken"))
	}
	s, err := getService()
	if err != nil {
		return err
	}
	st, _ := s.Status()
	wasRunning := st == service.StatusRunning
	wasEnabled := autostartEnabled()
	_ = s.Stop()
	if err := s.Uninstall(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("svc.err.uninstall"), err)
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("svc.err.install"), err)
	}
	if err := setAutostart(wasEnabled); err != nil {
		fmt.Println(i18n.T("svc.warn.autostart"), err)
	}
	fmt.Println(i18n.T("svc.legacy.upgraded"))
	if startAfter && wasRunning {
		if err := s.Start(); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("svc.err.start"), err)
		}
		fmt.Println(i18n.T("svc.started"))
	}
	return nil
}
