package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/client/wgnet"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Zorven Net komutlari: share (cihaz portunu paylas), access (paylasima yerel
// port), net status. Hepsi kullanici alaninda calisir; yonetici/root GEREKMEZ.

type netConnFlags struct {
	server, apiToken string
	insecure         bool
	caCert           string
}

func (f *netConnFlags) add(c *cobra.Command) {
	c.Flags().StringVar(&f.server, "server", "", "Zorven sunucusu (host:port)")
	c.Flags().StringVar(&f.apiToken, "api-token", "", "API token (ZORVEN_API_TOKEN ile de verilebilir)")
	c.Flags().BoolVar(&f.insecure, "insecure", false, "TLS yerine ws:// (yalnizca lokal gelistirme)")
	c.Flags().StringVar(&f.caCert, "ca-cert", "", "self-signed sunucu sertifikasi dosyasi")
}

func (f *netConnFlags) resolveServer() (string, bool, string) {
	cfg, _ := config.Load()
	srv := strings.TrimSpace(f.server)
	if srv == "" {
		srv = strings.TrimSpace(os.Getenv("ZORVEN_SERVER"))
	}
	if srv == "" {
		srv = strings.TrimSpace(cfg.ServerAddr)
	}
	if srv == "" || srv == "localhost:8443" {
		srv = "zorven.app:443"
	}
	ca := f.caCert
	if ca == "" {
		ca = cfg.CACertPath
	}
	return srv, f.insecure || cfg.Insecure, ca
}

func (f *netConnFlags) token() (string, error) {
	t := strings.TrimSpace(f.apiToken)
	if t == "" {
		t = strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN"))
	}
	if t == "" {
		return "", fmt.Errorf("API token gerekli: --api-token veya ZORVEN_API_TOKEN (panel > API & Guvenlik, net:read/net:write kapsami)")
	}
	if strings.HasPrefix(t, "zrv_live_") || strings.HasPrefix(t, "rpsh_live_") {
		return "", fmt.Errorf("bu bir istemci (ajan) token'i; bu komut bir API token ister")
	}
	return t, nil
}

func (f *netConnFlags) httpClient(ca string) (*http.Client, error) {
	if ca == "" {
		return nil, nil
	}
	return agent.HTTPClientWithCA(ca)
}

// parseGrantee: "user:ali@x.com" | "group:role:admin" | "device:nas" | "everyone" | "ali@x.com" | "nas".
func parseGrantee(s string) protocol.NetGrantee {
	s = strings.TrimSpace(s)
	for _, p := range []string{"user", "group", "device"} {
		if v, ok := strings.CutPrefix(s, p+":"); ok {
			return protocol.NetGrantee{Type: p, ID: v}
		}
	}
	switch {
	case s == "everyone" || strings.HasPrefix(s, "role:"):
		return protocol.NetGrantee{Type: "group", ID: s}
	case strings.Contains(s, "@"):
		return protocol.NetGrantee{Type: "user", ID: s}
	}
	return protocol.NetGrantee{Type: "device", ID: s}
}

func newShareCmd() *cobra.Command {
	var to []string
	var udp, persist bool
	var name, expires, token, server, caCert, logLevel string
	var insecure bool
	cmd := &cobra.Command{
		Use:   "share <port> --to <kullanici|grup|cihaz> [--udp] [--name x] [--expires 24h]",
		Short: "Bu cihazin bir portunu secili kisi/grup/cihazla uctan uca sifreli paylasir (yonetici hakki gerekmez)",
		Long: "Yerel bir portu WireGuard ile YALNIZ belirttiginiz alicilara acar. Sunucu icerigi goremez.\n" +
			"Paylasim bu komut calistigi surece gecerlidir (Ctrl+C ile biter); --persist ile kalici olur\n" +
			"(ajan calistikca sunulur). Alici: zorven access <ad> --local <port>.\n\n" +
			"Alici bicimleri: ali@firma.com (kullanici), user:<id>, group:everyone, group:role:admin, device:<ad>",
		Example: "  zorven share 5432 --to ali@firma.com --name db\n  zorven share 25565 --udp --to group:everyone --expires 4h",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(strings.TrimPrefix(args[0], ":"))
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("gecersiz port %q", args[0])
			}
			if len(to) == 0 {
				return fmt.Errorf("--to gerekli: paylasimin kime acilacagini belirtin")
			}
			spec := agent.NetShareSpec{Name: name, Port: port, Proto: "tcp", Persist: persist}
			if udp {
				spec.Proto = "udp"
			}
			for _, t := range to {
				spec.To = append(spec.To, parseGrantee(t))
			}
			if expires != "" {
				d, err := time.ParseDuration(expires)
				if err != nil || d < time.Minute {
					return fmt.Errorf("gecersiz --expires %q (en az 1m, ornek 24h)", expires)
				}
				spec.ExpiresSec = int(d.Seconds())
			}
			// Cihazda ajan zaten calisiyorsa (servis/on plan) ikinci baglanti acma:
			// paylasimi o ajana yaptir.
			isp := localctl.ShareSpec{Name: spec.Name, Port: spec.Port, Proto: spec.Proto, ExpiresSec: spec.ExpiresSec, Persist: persist}
			for _, g := range spec.To {
				isp.To = append(isp.To, localctl.Grantee{Type: g.Type, ID: g.ID})
			}
			if found, ferr := shareViaAgent(isp); found {
				return ferr
			}

			cfg, _ := config.Load()
			tok := strings.TrimSpace(token)
			if tok == "" {
				tok = strings.TrimSpace(os.Getenv("ZORVEN_TOKEN"))
			}
			if tok == "" {
				tok = strings.TrimSpace(cfg.Token)
			}
			if tok == "" {
				return fmt.Errorf("istemci token'i bulunamadi: zorven authtoken <TOKEN>")
			}
			srv := strings.TrimSpace(server)
			if srv == "" {
				srv = strings.TrimSpace(os.Getenv("ZORVEN_SERVER"))
			}
			if srv == "" {
				srv = strings.TrimSpace(cfg.ServerAddr)
			}
			if srv == "" || srv == "localhost:8443" {
				srv = "zorven.app:443"
			}
			ca := caCert
			if ca == "" {
				ca = cfg.CACertPath
			}
			var lvl slog.Level
			if lvl.UnmarshalText([]byte(logLevel)) != nil {
				lvl = slog.LevelWarn
			}
			lg := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
			ag := &agent.Agent{
				ServerAddr: srv, Token: tok, Insecure: insecure || cfg.Insecure, CACertPath: ca, Log: lg,
				NoTerminal: true, NoScreen: true, NoAutoUpdate: true, Version: version,
				NetShares: []agent.NetShareSpec{spec},
			}
			ag.OnNetEvent = func(m string) { fmt.Println("  " + m) }
			ag.OnStatus = func(s agent.Status) {
				switch s.State {
				case agent.StateConnecting:
					fmt.Println("Zorven sunucusuna baglaniliyor (" + srv + ")...")
				case agent.StateFatal:
					fmt.Println("Baglanti hatasi: " + s.Message)
					if strings.Contains(s.Message, "409") {
						fmt.Println("  " + conflictHint)
					}
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return ag.Run(ctx)
		},
	}
	cmd.AddCommand(newShareRmCmd(), newShareLsCmd())
	f := cmd.Flags()
	f.StringArrayVar(&to, "to", nil, "alici (tekrarlanabilir): kullanici, grup veya cihaz")
	f.BoolVar(&udp, "udp", false, "UDP portunu paylas (varsayilan TCP)")
	f.StringVar(&name, "name", "", "paylasim adi (varsayilan <cihaz>-<port>)")
	f.StringVar(&expires, "expires", "", "paylasimin omru (ornek 24h)")
	f.BoolVar(&persist, "persist", false, "komut/ajan bitince paylasimi silme (calisan servis uzerinden olusturulan paylasimlar zaten kalicidir)")
	f.StringVar(&token, "token", "", "istemci token'i (zrv_live_...)")
	f.StringVar(&server, "server", "", "Zorven sunucusu (host:port)")
	f.BoolVar(&insecure, "insecure", false, "TLS yerine ws:// (yalnizca lokal gelistirme)")
	f.StringVar(&caCert, "ca-cert", "", "self-signed sunucu sertifikasi dosyasi")
	f.StringVar(&logLevel, "log-level", "warn", "debug | info | warn | error")
	return cmd
}

func newAccessCmd() *cobra.Command {
	var cf netConnFlags
	var local int
	var bind string
	cmd := &cobra.Command{
		Use:   "access <paylasim|cihaz>[:port] [--local <port>]",
		Short: "Size acilan bir paylasima yerel 127.0.0.1 portu uzerinden baglanir (WireGuard, yonetici hakki gerekmez)",
		Long: "API token ile gecici bir ag dugumu olarak katilir ve yerel bir portu paylasima yonlendirir.\n" +
			"UDP paylasimlari icin yerel UDP soketi acilir.",
		Example: "  ZORVEN_API_TOKEN=... zorven access db --local 15432\n  psql -h 127.0.0.1 -p 15432 app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, err := cf.token()
			if err != nil {
				return err
			}
			if ip := net.ParseIP(bind); ip == nil || !ip.IsLoopback() {
				return fmt.Errorf("--bind yalnizca loopback olabilir (yerel dinleyicide kimlik dogrulama yok)")
			}
			srv, insecure, ca := cf.resolveServer()
			hc, err := cf.httpClient(ca)
			if err != nil {
				return err
			}
			lg := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
			acc := wgnet.NewAccessor(wgnet.SessionConfig{
				ServerAddr: srv, Token: tok, Insecure: insecure, HTTPClient: hc, Log: lg, Name: "access",
			})
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			runErr := make(chan error, 1)
			go func() { runErr <- acc.Run(ctx) }()

			wctx, wcancel := context.WithTimeout(ctx, 30*time.Second)
			defer wcancel()
			ready := make(chan error, 1)
			go func() { ready <- acc.WaitReady(wctx) }()
			select {
			case err := <-runErr:
				return err
			case err := <-ready:
				if err != nil {
					return fmt.Errorf("ag oturumu kurulamadi: %w", err)
				}
			}
			// Netmap tam gelsin (paylasimlar ilk mesajda gelir, ama kosu icin kisa bekleme).
			var dial wgnet.DialFunc
			var proto string
			deadline := time.Now().Add(10 * time.Second)
			for {
				dial, proto, err = acc.Dialer(args[0])
				if err == nil || time.Now().After(deadline) {
					break
				}
				time.Sleep(300 * time.Millisecond)
			}
			if err != nil {
				return err
			}
			port := local
			if port == 0 {
				for _, s := range acc.State().SharesIn {
					if s.Name == args[0] || s.OwnerName == args[0] {
						port = s.Port
					}
				}
			}
			addr := net.JoinHostPort(bind, strconv.Itoa(port))
			onErr := func(e error) { fmt.Printf("  x %v\n", e) }
			if proto == "udp" {
				pc, err := net.ListenPacket("udp", addr)
				if err != nil {
					return fmt.Errorf("dinlenemedi (%s): %w", addr, err)
				}
				fmt.Printf("Zorven Net: %s (udp) -> %s\n", args[0], addr)
				go wgnet.ServeUDP(ctx, pc, dial, onErr)
			} else {
				ln, err := net.Listen("tcp", addr)
				if err != nil {
					return fmt.Errorf("dinlenemedi (%s): %w", addr, err)
				}
				fmt.Printf("Zorven Net: %s (tcp) -> %s\n", args[0], addr)
				go wgnet.ServeTCP(ctx, ln, dial, onErr)
			}
			fmt.Println("Cikmak icin Ctrl+C.")
			select {
			case <-ctx.Done():
				return nil
			case err := <-runErr:
				return err
			}
		},
	}
	cf.add(cmd)
	cmd.Flags().IntVar(&local, "local", 0, "yerel port (varsayilan: paylasim portu)")
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", "yerel dinleme adresi (yalnizca loopback)")
	return cmd
}

func newNetCmd() *cobra.Command {
	root := &cobra.Command{Use: "net", Short: "Zorven Net (WireGuard port paylasimi) komutlari"}
	var cf netConnFlags
	status := &cobra.Command{
		Use:   "status",
		Short: "Dugumleri (IP, cevrimici) ve paylasimlari listeler",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, err := cf.token()
			if err != nil {
				return err
			}
			srv, insecure, ca := cf.resolveServer()
			hc, err := cf.httpClient(ca)
			if err != nil {
				return err
			}
			if hc == nil {
				hc = &http.Client{Timeout: 20 * time.Second}
			}
			scheme := "https"
			if insecure {
				scheme = "http"
			}
			get := func(path string, v any) error {
				req, _ := http.NewRequestWithContext(cmd.Context(), http.MethodGet, scheme+"://"+srv+path, nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				resp, err := hc.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
				if resp.StatusCode != 200 {
					return fmt.Errorf("sunucu %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
				}
				return json.Unmarshal(b, v)
			}
			var nodes struct {
				Nodes []struct {
					Name, IP, Kind string
					Online         bool
				} `json:"nodes"`
			}
			var shares struct {
				Shares []struct {
					Name        string `json:"name"`
					Proto       string `json:"proto"`
					Port        int    `json:"port"`
					OwnerName   string `json:"owner_name"`
					OwnerOnline bool   `json:"owner_online"`
					Relation    string `json:"relation"`
				} `json:"shares"`
			}
			if err := get("/api/v1/net/nodes", &nodes); err != nil {
				return err
			}
			if err := get("/api/v1/net/shares", &shares); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "DUGUM\tIP\tTUR\tDURUM")
			for _, n := range nodes.Nodes {
				st := "cevrimdisi"
				if n.Online {
					st = "cevrimici"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n.Name, n.IP, n.Kind, st)
			}
			tw.Flush()
			fmt.Println()
			tw = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "PAYLASIM\tCIHAZ\tPORT\tILISKI\tSAHIP")
			for _, s := range shares.Shares {
				st := "cevrimdisi"
				if s.OwnerOnline {
					st = "cevrimici"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s/%d\t%s\t%s\n", s.Name, s.OwnerName, s.Proto, s.Port, s.Relation, st)
			}
			return tw.Flush()
		},
	}
	cf.add(status)
	root.AddCommand(status)
	return root
}
