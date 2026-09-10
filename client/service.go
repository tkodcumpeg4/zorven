package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
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

	localURL := cfg.LocalURL
	if localURL == "" {
		localURL = "http://localhost:8003"
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ag := &agent.Agent{
		ServerAddr: serverAddr,
		Token:      cfg.Token,
		LocalURL:   localURL,
		Insecure:   cfg.Insecure,
		CACertPath: cfg.CACertPath,
		Log:        logger,
		IsService:  true,
		// Servis modunda varsayilan guvenlik: uzaktan shell ve ekran kapali
		NoTerminal: true,
		NoScreen:   true,
	}

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
	)

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Zorven servisini sisteme kaydeder (Windows Service veya systemd)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tokenFlag != "" || serverFlag != "" {
				sysCfg, _ := config.LoadFile(config.SystemPath())
				if tokenFlag != "" {
					sysCfg.Token = strings.TrimSpace(tokenFlag)
				}
				if serverFlag != "" {
					sysCfg.ServerAddr = strings.TrimSpace(serverFlag)
				}
				if err := config.SaveSystem(sysCfg); err != nil {
					return fmt.Errorf("sistem ayarlari kaydedilemedi: %w", err)
				}
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
				return fmt.Errorf("servis kurulamadi (yonetici/root yetkisi gereklidir): %w", err)
			}
			fmt.Println("✓ Zorven servisi basariyla kuruldu.")
			fmt.Println("  Baslatmak icin: zorven service start")
			return nil
		},
	}
	installCmd.Flags().StringVarP(&tokenFlag, "token", "t", "", "servise atanacak token (sistem ayarlarina yazilir)")
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
