package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
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

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
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
	prog := &serviceProgram{}
	return service.New(prog, svcConfig)
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Zorven arka plan sistem servisini (Windows Service / systemd) yonetir",
	}

	var (
		tokenFlag  string
		serverFlag string
		instTerm   bool
		instScreen bool
	)

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Zorven servisini sisteme kaydeder (Windows Service veya systemd)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := installService(installOpts{token: tokenFlag, server: serverFlag, terminal: instTerm, screen: instScreen}); err != nil {
				return err
			}
			fmt.Println("Zorven servisi kuruldu (sistem acilisinda otomatik baslar).")
			fmt.Println("  Baslatmak icin: zorven service start   (ya da tek adimda: zorven up)")
			return nil
		},
	}
	installCmd.Flags().StringVarP(&tokenFlag, "token", "t", "", "servise atanacak token (sistem ayarlarina yazilir)")
	installCmd.Flags().BoolVar(&instTerm, "enable-terminal", false, "servis icin uzaktan terminali kalici olarak ac (varsayilan kapali)")
	installCmd.Flags().BoolVar(&instScreen, "enable-screen", false, "servis icin ekran paylasimini kalici olarak ac (varsayilan kapali)")
	installCmd.Flags().StringVarP(&serverFlag, "server", "s", "", "sunucu adresi (or. zorven.app:8443)")

	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Zorven servisini sistemden kaldirir",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			_ = s.Stop()
			if err := s.Uninstall(); err != nil {
				return fmt.Errorf("servis kaldirilamadi: %w", err)
			}
			fmt.Println("✓ Zorven servisi sistemden kaldirildi.")
			return nil
		},
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Zorven servisini baslatir",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if err := s.Start(); err != nil {
				return fmt.Errorf("servis baslatilamadi: %w", err)
			}
			fmt.Println("✓ Zorven servisi baslatildi.")
			return nil
		},
	}

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Zorven servisini durdurur",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if err := s.Stop(); err != nil {
				return fmt.Errorf("servis durdurulamadi: %w", err)
			}
			fmt.Println("✓ Zorven servisi durduruldu.")
			return nil
		},
	}

	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Zorven servisini yeniden baslatir",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if err := s.Restart(); err != nil {
				return fmt.Errorf("servis yeniden baslatilamadi: %w", err)
			}
			fmt.Println("✓ Zorven servisi yeniden baslatildi.")
			return nil
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Zorven servisinin aktif durumunu gosterir",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			st, err := s.Status()
			if err != nil {
				fmt.Printf("Servis Durumu: Bilinmiyor / Kurulu degil (%v)\n", err)
				return nil
			}
			switch st {
			case service.StatusRunning:
				fmt.Println("Servis Durumu: Calisiyor (Running)")
			case service.StatusStopped:
				fmt.Println("Servis Durumu: Durduruldu (Stopped)")
			default:
				fmt.Printf("Servis Durumu: %d\n", st)
			}
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
		return fmt.Errorf("istemci token'i bulunamadi. Lutfen --token bayragini verin veya once 'zorven authtoken <TOKEN>' calistirin")
	}
	s, err := getService()
	if err != nil {
		return err
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("servis kurulamadi (yonetici/root yetkisi gereklidir: sudo / Yonetici olarak calistir): %w", err)
	}
	return nil
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
			return fmt.Errorf("sistem ayarlari kaydedilemedi: %w", err)
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
	return fmt.Errorf("bu isletim sisteminde acilista baslatma ayari desteklenmiyor: %s", runtime.GOOS)
}

// newShortcutCmds, sik kullanilan servis islemleri icin kisa komutlar:
// zorven up / zorven down / zorven autostart on|off.
func newShortcutCmds() []*cobra.Command {
	var o installOpts
	up := &cobra.Command{
		Use:   "up",
		Short: "Arka plan servisi olarak calistir: gerekirse kur, acilista baslat ve simdi baslat",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			if serviceInstalled(s) {
				// Kurulu serviste yalniz kalici ayarlari guncelle; yeniden baslatinca gecerli olur.
				if err := saveServiceOpts(o); err != nil {
					return err
				}
			} else if err := installService(o); err != nil {
				return err
			}
			if err := setAutostart(true); err != nil {
				fmt.Println("Uyari: acilista baslatma ayarlanamadi:", err)
			}
			if st, _ := s.Status(); st == service.StatusRunning {
				if err := s.Restart(); err != nil {
					return fmt.Errorf("servis yeniden baslatilamadi: %w", err)
				}
				fmt.Println("Zorven servisi yeniden baslatildi; sistem acilisinda otomatik baslar.")
				return nil
			}
			if err := s.Start(); err != nil {
				return fmt.Errorf("servis baslatilamadi (yonetici/root yetkisi gerekebilir): %w", err)
			}
			fmt.Println("Zorven arka plan servisi calisiyor; sistem acilisinda otomatik baslar.")
			fmt.Println("  Durum: zorven status   Durdur: zorven down")
			return nil
		},
	}
	up.Flags().StringVarP(&o.token, "token", "t", "", "servise atanacak token (sistem ayarlarina yazilir)")
	up.Flags().StringVarP(&o.server, "server", "s", "", "sunucu adresi (or. zorven.app:443)")
	up.Flags().BoolVar(&o.terminal, "enable-terminal", false, "servis icin uzaktan terminali kalici olarak ac")
	up.Flags().BoolVar(&o.screen, "enable-screen", false, "servis icin ekran paylasimini kalici olarak ac")

	var remove bool
	down := &cobra.Command{
		Use:   "down",
		Short: "Arka plan servisini durdur (--remove ile sistemden de kaldir)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := getService()
			if err != nil {
				return err
			}
			_ = s.Stop()
			if remove {
				if err := s.Uninstall(); err != nil {
					return fmt.Errorf("servis kaldirilamadi: %w", err)
				}
				fmt.Println("Zorven servisi durduruldu ve sistemden kaldirildi.")
				return nil
			}
			fmt.Println("Zorven servisi durduruldu. (Acilista yine baslar; kapatmak icin: zorven autostart off)")
			return nil
		},
	}
	down.Flags().BoolVar(&remove, "remove", false, "servisi sistemden tamamen kaldir")

	autostart := &cobra.Command{
		Use:       "autostart on|off",
		Short:     "Servisin sistem acilisinda otomatik baslamasini ac/kapat",
		Args:      cobra.ExactValidArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			on := args[0] == "on"
			if err := setAutostart(on); err != nil {
				return err
			}
			if on {
				fmt.Println("Acilista otomatik baslatma ACIK.")
			} else {
				fmt.Println("Acilista otomatik baslatma KAPALI. (Servis kurulu kalir; elle: zorven up)")
			}
			return nil
		},
	}
	return []*cobra.Command{up, down, autostart}
}
