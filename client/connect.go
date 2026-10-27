package main

import (
	"context"
	"errors"
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
	"github.com/tkodcumpeg4/zorven/client/i18n"
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
		Short: i18n.T("conn2.short"),
		Long:  i18n.T("conn2.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			token := strings.TrimSpace(apiToken)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN"))
			}
			if token == "" {
				return errors.New(i18n.T("conn2.err.token"))
			}
			// Istemci (ajan) token'i burada ise yaramaz: o bir cihazin kimligidir,
			// kullanicinin degil. Erken ve acik hata, sunucudan 401 almaktan iyi.
			if strings.HasPrefix(token, "zrv_live_") || strings.HasPrefix(token, "rpsh_live_") {
				return errors.New(i18n.T("conn2.err.agenttoken"))
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
				return errors.New(i18n.T("conn2.err.loopback", listen))
			}
			if gateway && len(prefixes) == 0 {
				return errors.New(i18n.T("conn2.err.gateway"))
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
			fmt.Println(i18n.T("conn2.running", listen, srv))
			fmt.Println(i18n.T("net.access.quit"))
			return s.ListenAndServe(ctx)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:1080", i18n.T("conn2.flag.listen"))
	cmd.Flags().StringVar(&apiToken, "api-token", "", i18n.T("net.flag.apitoken"))
	cmd.Flags().StringVar(&cServer, "server", "", i18n.T("net.flag.server"))
	cmd.Flags().BoolVar(&insecureFlag, "insecure", false, i18n.T("net.flag.insecure"))
	cmd.Flags().BoolVar(&gateway, "gateway", false, i18n.T("conn2.flag.gateway"))
	cmd.Flags().StringVar(&allowFrom, "allow-from", "", i18n.T("conn2.flag.allow"))
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
			return nil, errors.New(i18n.T("conn2.err.allow", part))
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
