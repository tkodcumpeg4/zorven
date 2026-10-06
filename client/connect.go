package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/netconnect"
)

// newConnectCmd, Zorven Network istemcisi (FAZ 3 / F17, Asama 1): yerel bir
// SOCKS5 proxy acar; uygulamalar bunun uzerinden kurumun OZEL kaynaklarina
// (or. db.internal:5432) adiyla ulasir. Kaynaklar internete hic acilmaz.
//
// Kullanim:
//
//	ZORVEN_API_TOKEN=zrv_... zorven connect
//	psql "host=db.internal port=5432" ile degil, SOCKS destekleyen istemciyle:
//	ssh -o ProxyCommand='nc -X 5 -x 127.0.0.1:1080 %h %p' ssh.internal
func newConnectCmd() *cobra.Command {
	var listen, apiToken, cServer, allowFrom string
	var insecureFlag, gateway bool

	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Ozel aga baglanir: yerel SOCKS5 proxy acar (Zorven Network)",
		Long: "Kurumun ozel kaynaklarina (or. db.internal:5432) yerel bir SOCKS5 proxy\n" +
			"uzerinden erisim saglar. Kaynaklar internete acik degildir.\n\n" +
			"API token gerekir (panel > API & Guvenlik, en az tunnels:read kapsami):\n" +
			"  ZORVEN_API_TOKEN=... zorven connect\n\n" +
			"Ardindan uygulamanizda SOCKS5 proxy olarak 127.0.0.1:1080 kullanin.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			token := strings.TrimSpace(apiToken)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN"))
			}
			if token == "" {
				return fmt.Errorf("API token gerekli: --api-token veya ZORVEN_API_TOKEN")
			}
			// Istemci (ajan) token'i burada ise yaramaz: o bir cihazin kimligidir,
			// kullanicinin degil. Erken ve acik hata, sunucudan 401 almaktan iyi.
			if strings.HasPrefix(token, "zrv_live_") || strings.HasPrefix(token, "rpsh_live_") {
				return fmt.Errorf("bu bir istemci (ajan) token'i; zorven connect bir API token ister")
			}

			cfg, _ := config.Load()
			srv := strings.TrimSpace(cServer)
			if srv == "" {
				srv = strings.TrimSpace(os.Getenv("ZORVEN_SERVER"))
			}
			if srv == "" {
				srv = strings.TrimSpace(cfg.ServerAddr)
			}
			if srv == "" || srv == "localhost:8443" {
				srv = "zorven.app:443"
			}

			// Loopback disina acmak LAN'daki makinelere ozel ag erisimi verir ve
			// yerel dinleyicide kimlik dogrulama yoktur. Bu yuzden YALNIZCA acik
			// ag gecidi modunda ve kaynak araligi izin listesiyle mumkundur (F19).
			prefixes, err := parseAllowFrom(allowFrom)
			if err != nil {
				return err
			}
			if !isLoopbackListen(listen) && !gateway {
				return fmt.Errorf("%s loopback degil: LAN'a acmak icin --gateway ve --allow-from kullanin", listen)
			}
			if gateway && len(prefixes) == 0 {
				return fmt.Errorf("--gateway icin --allow-from zorunlu (or. --allow-from 192.168.2.0/24)")
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			s := &netconnect.Server{
				ServerAddr: srv,
				Token:      token,
				Insecure:   insecureFlag || cfg.Insecure,
				Listen:     listen,
				AllowFrom:  prefixes,
				Log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
				OnConnect: func(target string, err error) {
					if err != nil {
						fmt.Printf("  x %s — %v\n", target, err)
						return
					}
					fmt.Printf("  > %s\n", target)
				},
			}
			fmt.Printf("Zorven Network: SOCKS5 proxy %s adresinde (sunucu: %s)\n", listen, srv)
			fmt.Println("Cikmak icin Ctrl+C.")
			return s.ListenAndServe(ctx)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:1080", "yerel SOCKS5 dinleme adresi")
	cmd.Flags().StringVar(&apiToken, "api-token", "", "API token (ZORVEN_API_TOKEN ile de verilebilir)")
	cmd.Flags().StringVar(&cServer, "server", "", "Zorven sunucusu (host:port)")
	cmd.Flags().BoolVar(&insecureFlag, "insecure", false, "TLS yerine ws:// (yalnizca lokal gelistirme)")
	cmd.Flags().BoolVar(&gateway, "gateway", false, "ag gecidi modu (site-to-site): LAN'daki makinelere proxy ac")
	cmd.Flags().StringVar(&allowFrom, "allow-from", "", "ag gecidi modunda izinli istemci araliklari, virgulle (or. 192.168.2.0/24)")
	return cmd
}

// parseAllowFrom, virgulle ayrilmis araliklari cozer.
func parseAllowFrom(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("gecersiz --allow-from araligi %q (or. 192.168.2.0/24)", part)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// isLoopbackListen, dinleme adresinin yalnizca bu makineye acik olup olmadigi.
func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
