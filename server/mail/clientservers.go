package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ClientServersConfig, mail istemcisi sunucularinin (IMAP + SMTP gonderim)
// baslatilmasi icin gereken ayarlardir. Bos adres = kapali.
type ClientServersConfig struct {
	Store            store.Store
	MailDomain       string
	PlatformDomain   string
	SystemLocalParts []string
	Relay            Relay
	TLS              *tls.Config
	Logger           *slog.Logger

	// Host, istemcilere bildirilen sunucu adi (bos: mail.<platformDomain> veya MailDomain).
	Host string

	IMAPAddr         string // implicit TLS, or. ":993"
	IMAPStartTLSAddr string // STARTTLS, or. ":143" (varsayilan kapali)
	SubmissionAddr   string // STARTTLS zorunlu, or. ":587"
	SubmissionsAddr  string // implicit TLS, or. ":465"
}

// DisabledAddr, "off" / "-" / "none" / "disabled" degerlerini kapali sayar.
func DisabledAddr(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "-", "none", "disabled", "false", "0":
		return true
	}
	return false
}

func portOf(addr string) int {
	if strings.TrimSpace(addr) == "" {
		return 0
	}
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// StartClientServers, ayarlanan IMAP/SMTP gonderim dinleyicilerini arka planda
// baslatir; ctx iptalinde kapatir. Istemci otomatik yapilandirmasi icin
// ClientConfig doner. TLS yoksa hicbir sunucu baslatilmaz.
func StartClientServers(ctx context.Context, cfg ClientServersConfig) (*ClientConfig, error) {
	if cfg.TLS == nil {
		return nil, errors.New("mail istemcisi sunuculari TLS sertifikasi gerektirir")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	host := strings.ToLower(strings.TrimSpace(cfg.Host))
	if host == "" {
		if cfg.PlatformDomain != "" {
			host = "mail." + cfg.PlatformDomain
		} else {
			host = cfg.MailDomain
		}
	}
	cc := &ClientConfig{
		MailDomain:     cfg.MailDomain,
		PlatformDomain: cfg.PlatformDomain,
		Host:           host,
		IMAPPort:       portOf(cfg.IMAPAddr),
		SubmissionPort: portOf(cfg.SubmissionAddr),
		SubmissionsTLS: portOf(cfg.SubmissionsAddr),
		DisplayName:    "Zorven Mail",
	}
	auth := NewAuthenticator(cfg.Store, cfg.MailDomain, cfg.PlatformDomain, cfg.SystemLocalParts, cfg.Logger)
	log := cfg.Logger

	run := func(name string, f func() error, closer func() error) {
		go func() {
			log.Info("mail istemcisi sunucusu baslatildi", "sunucu", name)
			if err := f(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Warn("mail istemcisi sunucusu durdu", "sunucu", name, "hata", err)
			}
		}()
		go func() {
			<-ctx.Done()
			_ = closer()
		}()
	}

	if cfg.IMAPAddr != "" || cfg.IMAPStartTLSAddr != "" {
		imapSrv := NewIMAPServer(auth, cfg.Store, DefaultHub, cfg.TLS, false, log)
		if cfg.IMAPAddr != "" {
			ln, err := tls.Listen("tcp", cfg.IMAPAddr, cfg.TLS)
			if err != nil {
				return nil, fmt.Errorf("IMAP (%s) dinlenemedi: %w", cfg.IMAPAddr, err)
			}
			run("imaps "+cfg.IMAPAddr, func() error { return imapSrv.Serve(ln) }, imapSrv.Close)
		}
		if cfg.IMAPStartTLSAddr != "" {
			ln, err := net.Listen("tcp", cfg.IMAPStartTLSAddr)
			if err != nil {
				return nil, fmt.Errorf("IMAP STARTTLS (%s) dinlenemedi: %w", cfg.IMAPStartTLSAddr, err)
			}
			run("imap-starttls "+cfg.IMAPStartTLSAddr, func() error { return imapSrv.Serve(ln) }, imapSrv.Close)
		}
	}

	if cfg.Relay != nil && (cfg.SubmissionAddr != "" || cfg.SubmissionsAddr != "") {
		sub := NewSubmissionServer(auth, cfg.Store, cfg.Relay, DefaultHub, log)
		if cfg.SubmissionAddr != "" {
			srv := sub.NewSMTPServer(cfg.SubmissionAddr, cfg.TLS)
			ln, err := net.Listen("tcp", cfg.SubmissionAddr)
			if err != nil {
				return nil, fmt.Errorf("SMTP gonderim (%s) dinlenemedi: %w", cfg.SubmissionAddr, err)
			}
			run("submission "+cfg.SubmissionAddr, func() error { return srv.Serve(ln) }, srv.Close)
		}
		if cfg.SubmissionsAddr != "" {
			srv := sub.NewSMTPServer(cfg.SubmissionsAddr, cfg.TLS)
			ln, err := tls.Listen("tcp", cfg.SubmissionsAddr, cfg.TLS)
			if err != nil {
				return nil, fmt.Errorf("SMTP gonderim TLS (%s) dinlenemedi: %w", cfg.SubmissionsAddr, err)
			}
			run("submissions "+cfg.SubmissionsAddr, func() error { return srv.Serve(ln) }, srv.Close)
		}
	} else if cfg.SubmissionAddr != "" || cfg.SubmissionsAddr != "" {
		cc.SubmissionPort, cc.SubmissionsTLS = 0, 0
	}
	return cc, nil
}
