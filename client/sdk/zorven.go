// Package sdk, Zorven tünellerini programatik olarak açmak için küçük bir Go
// istemci kütüphanesidir. Mevcut agent'ı sarar; ekstra bağımlılık gerektirmez.
//
// Örnek:
//
//	sess, err := sdk.Connect(ctx, sdk.Config{Token: os.Getenv("ZORVEN_TOKEN"), Target: "8080"})
//	if err != nil { log.Fatal(err) }
//	defer sess.Close()
//	log.Println("herkese açık URL:", sess.URL())
//	sess.Wait() // bağlantı kapanana kadar bekle
package sdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/agent"
)

// Config, bir tünel oturumunun ayarları.
type Config struct {
	// Token, istemci kimlik doğrulama jetonu (panelde "İstemciler" altında üretilir). Zorunlu.
	Token string
	// Target, tünellenecek yerel hedef. "8080", "localhost:8080" veya tam
	// "http://localhost:8080" kabul edilir. Zorunlu.
	Target string
	// ServerAddr, Zorven sunucusu (varsayılan "zorven.app:443").
	ServerAddr string
	// Insecure true ise TLS doğrulaması atlanır (yalnızca yerel geliştirme).
	Insecure bool
	// Logger, isteğe bağlı. nil ise loglar atılır.
	Logger *slog.Logger
	// NoTerminal / NoScreen, uzak kabuk / ekran paylaşımını tamamen kapatır (önerilir).
	NoTerminal bool
	NoScreen   bool
	// ConnectTimeout, ilk bağlantı için beklenecek süre (varsayılan 30s).
	ConnectTimeout time.Duration
}

// Tunnel, açılan bir tünelin herkese açık adresleri.
type Tunnel struct {
	URLs   []string // "https://api--org.zorven.app" gibi
	Target string
}

// Session, canlı bir tünel oturumu.
type Session struct {
	cancel context.CancelFunc
	done   chan error

	mu       sync.Mutex
	tunnels  []Tunnel
	clientID string
}

// Connect, sunucuya bağlanır, hedefi tünelller ve bağlantı KURULANA kadar bloklar.
// Dönen Session, herkese açık URL'leri taşır. Hata olursa (kimlik, ağ) döner.
func Connect(ctx context.Context, cfg Config) (*Session, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("zorven sdk: Token zorunlu")
	}
	target, err := normalizeTarget(cfg.Target)
	if err != nil {
		return nil, err
	}
	server := cfg.ServerAddr
	if server == "" {
		server = "zorven.app:443"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	timeout := cfg.ConnectTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	s := &Session{done: make(chan error, 1)}
	ready := make(chan agent.Status, 1)
	var once sync.Once

	ag := &agent.Agent{
		ServerAddr:      server,
		Token:           cfg.Token,
		LocalURL:        target,
		RequestedTarget: target,
		Insecure:        cfg.Insecure,
		NoTerminal:      cfg.NoTerminal,
		NoScreen:        cfg.NoScreen,
		NoAutoUpdate:    true, // SDK gömülü kullanımda kendini güncellemesin
		Log:             logger,
		OnStatus: func(st agent.Status) {
			s.mu.Lock()
			s.clientID = st.ClientID
			if len(st.Tunnels) > 0 {
				tuns := make([]Tunnel, 0, len(st.Tunnels))
				for _, t := range st.Tunnels {
					urls := make([]string, 0, len(t.Hostnames))
					for _, h := range t.Hostnames {
						urls = append(urls, "https://"+h)
					}
					tuns = append(tuns, Tunnel{URLs: urls, Target: t.Target})
				}
				s.tunnels = tuns
			}
			s.mu.Unlock()
			if st.State == agent.StateConnected {
				once.Do(func() { ready <- st })
			}
		},
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go func() { s.done <- ag.Run(runCtx) }()

	select {
	case <-time.After(timeout):
		cancel()
		return nil, fmt.Errorf("zorven sdk: baglanti %s icinde kurulamadi", timeout)
	case err := <-s.done:
		// Bağlantı kurulmadan Run döndü => hata (kimlik, ağ, kalıcı ret).
		if err == nil {
			err = errors.New("baglanti beklenmedik sekilde kapandi")
		}
		return nil, fmt.Errorf("zorven sdk: %w", err)
	case <-ready:
		return s, nil
	}
}

// URL, ilk herkese açık HTTPS adresini döner (yoksa "").
func (s *Session) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tunnels {
		if len(t.URLs) > 0 {
			return t.URLs[0]
		}
	}
	return ""
}

// URLs, tüm herkese açık adresleri döner.
func (s *Session) URLs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, t := range s.tunnels {
		out = append(out, t.URLs...)
	}
	return out
}

// ClientID, sunucunun atadığı istemci kimliği.
func (s *Session) ClientID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientID
}

// Wait, oturum kapanana kadar bloklar ve varsa nihai hatayı döner.
func (s *Session) Wait() error {
	return <-s.done
}

// Close, oturumu kapatır (tünel düşer).
func (s *Session) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// normalizeTarget, "8080" / "localhost:8080" / tam URL'yi http:// biçimine getirir.
func normalizeTarget(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", errors.New("zorven sdk: Target zorunlu")
	}
	if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
		return t, nil
	}
	// Sadece port?
	if _, err := strconv.Atoi(t); err == nil {
		return "http://localhost:" + t, nil
	}
	// host:port
	return "http://" + t, nil
}
