package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
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
	c.Flags().StringVar(&f.server, "server", "", i18n.T("net.flag.server"))
	c.Flags().StringVar(&f.apiToken, "api-token", "", i18n.T("net.flag.apitoken"))
	c.Flags().BoolVar(&f.insecure, "insecure", false, i18n.T("net.flag.insecure"))
	c.Flags().StringVar(&f.caCert, "ca-cert", "", i18n.T("net.flag.cacert"))
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
		return "", errors.New(i18n.T("net.err.token"))
	}
	if strings.HasPrefix(t, "zrv_live_") || strings.HasPrefix(t, "rpsh_live_") {
		return "", errors.New(i18n.T("net.err.agenttoken"))
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
		Use:     "share <port> --to <kullanici|grup|cihaz> [--udp] [--name x] [--expires 24h]",
		Short:   i18n.T("net.share.short"),
		Long:    i18n.T("net.share.long"),
		Example: "  zorven share 5432 --to ali@firma.com --name db\n  zorven share 25565 --udp --to group:everyone --expires 4h",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := strconv.Atoi(strings.TrimPrefix(args[0], ":"))
			if err != nil || port < 1 || port > 65535 {
				return errors.New(i18n.T("net.share.err.port", args[0]))
			}
			if len(to) == 0 {
				return errors.New(i18n.T("net.share.err.to"))
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
					return errors.New(i18n.T("net.share.err.expires", expires))
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
				return errors.New(i18n.T("net.share.err.notoken"))
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
					fmt.Println(i18n.T("net.share.connecting", srv))
				case agent.StateFatal:
					fmt.Println(i18n.T("net.share.connerr", s.Message))
					if strings.Contains(s.Message, "409") {
						fmt.Println("  " + conflictHint())
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
	f.StringArrayVar(&to, "to", nil, i18n.T("net.share.flag.to"))
	f.BoolVar(&udp, "udp", false, i18n.T("net.share.flag.udp"))
	f.StringVar(&name, "name", "", i18n.T("net.share.flag.name"))
	f.StringVar(&expires, "expires", "", i18n.T("net.share.flag.expires"))
	f.BoolVar(&persist, "persist", false, i18n.T("net.share.flag.persist"))
	f.StringVar(&token, "token", "", i18n.T("net.share.flag.token"))
	f.StringVar(&server, "server", "", i18n.T("net.flag.server"))
	f.BoolVar(&insecure, "insecure", false, i18n.T("net.flag.insecure"))
	f.StringVar(&caCert, "ca-cert", "", i18n.T("net.flag.cacert"))
	f.StringVar(&logLevel, "log-level", "warn", "debug | info | warn | error")
	return cmd
}

// watchAccessPaths: yol (dogrudan/relay) degisince bir satir yazar.
func watchAccessPaths(ctx context.Context, acc *wgnet.Accessor) {
	last := map[string]string{}
	tk := time.NewTicker(2 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		for _, p := range acc.Paths() {
			if p.Path == last[p.IP] {
				continue
			}
			last[p.IP] = p.Path
			if p.Path == "direct" {
				fmt.Println(i18n.T("net.path.direct", p.Name, p.Addr))
			} else {
				fmt.Println(i18n.T("net.path.relay", p.Name))
			}
		}
	}
}

func newAccessCmd() *cobra.Command {
	var cf netConnFlags
	var local int
	var bind string
	cmd := &cobra.Command{
		Use:     "access <paylasim|cihaz>[:port] [--local <port>]",
		Short:   i18n.T("net.access.short"),
		Long:    i18n.T("net.access.long"),
		Example: "  ZORVEN_API_TOKEN=... zorven access db --local 15432\n  psql -h 127.0.0.1 -p 15432 app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, err := cf.token()
			if err != nil {
				return err
			}
			if ip := net.ParseIP(bind); ip == nil || !ip.IsLoopback() {
				return errors.New(i18n.T("net.access.err.bind"))
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
					return fmt.Errorf("%s: %w", i18n.T("net.access.err.session"), err)
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
					return fmt.Errorf("%s: %w", i18n.T("net.access.err.listen", addr), err)
				}
				fmt.Printf("Zorven Net: %s (udp) -> %s\n", args[0], addr)
				go wgnet.ServeUDP(ctx, pc, dial, onErr)
			} else {
				ln, err := net.Listen("tcp", addr)
				if err != nil {
					return fmt.Errorf("%s: %w", i18n.T("net.access.err.listen", addr), err)
				}
				fmt.Printf("Zorven Net: %s (tcp) -> %s\n", args[0], addr)
				go wgnet.ServeTCP(ctx, ln, dial, onErr)
			}
			fmt.Println(i18n.T("net.access.quit"))
			go watchAccessPaths(ctx, acc)
			select {
			case <-ctx.Done():
				return nil
			case err := <-runErr:
				return err
			}
		},
	}
	cf.add(cmd)
	cmd.Flags().IntVar(&local, "local", 0, i18n.T("net.access.flag.local"))
	cmd.Flags().StringVar(&bind, "bind", "127.0.0.1", i18n.T("net.access.flag.bind"))
	return cmd
}

// printLocalPaths: bu cihazda calisan ajan varsa esleriyle yolu (dogrudan/relay) yazar.
// Ajan yok/eski ise sessizce atlanir.
func printLocalPaths() {
	ep, _, err := findAgent()
	if ep == nil || err != nil {
		return
	}
	resp, err := callAgent(*ep, localctl.Request{Op: "net_paths"})
	if err != nil || len(resp.Paths) == 0 {
		return
	}
	fmt.Println()
	tw := newTable(os.Stdout)
	fmt.Fprintln(tw, i18n.T("net.tbl.peer")+"\tIP\t"+i18n.T("net.tbl.status")+"\t"+i18n.T("net.tbl.path")+"\tRTT")
	for _, p := range resp.Paths {
		st := i18n.T("net.offline")
		if p.Online {
			st = i18n.T("net.online")
		}
		rtt := "-"
		if p.Path == "direct" && p.RTTMs > 0 {
			rtt = fmt.Sprintf("%d ms", p.RTTMs)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.IP, st, pathLabel(p.Path), rtt)
	}
	tw.Flush()
}

func newNetCmd() *cobra.Command {
	root := &cobra.Command{Use: "net", Short: i18n.T("net.root.short")}
	var cf netConnFlags
	status := &cobra.Command{
		Use:   "status",
		Short: i18n.T("net.status.short"),
		Long:  i18n.T("net.status.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			localShown := printLocalTun()
			tok, err := cf.token()
			if err != nil {
				if localShown {
					fmt.Println(i18n.T("net.status.hint"))
					return nil
				}
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
					return errors.New(i18n.T("net.status.server", resp.StatusCode, strings.TrimSpace(string(b))))
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
					Path        string `json:"path"`
				} `json:"shares"`
			}
			if err := get("/api/v1/net/nodes", &nodes); err != nil {
				return err
			}
			if err := get("/api/v1/net/shares", &shares); err != nil {
				return err
			}
			tw := newTable(os.Stdout)
			fmt.Fprintln(tw, i18n.T("net.tbl.node")+"\tIP\t"+i18n.T("net.tbl.kind")+"\t"+i18n.T("net.tbl.status"))
			for _, n := range nodes.Nodes {
				st := i18n.T("net.offline")
				if n.Online {
					st = i18n.T("net.online")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n.Name, n.IP, n.Kind, st)
			}
			tw.Flush()
			fmt.Println()
			tw = newTable(os.Stdout)
			fmt.Fprintln(tw, strings.Join([]string{i18n.T("net.tbl.share"), i18n.T("net.tbl.device"), "PORT", i18n.T("net.tbl.relation"), i18n.T("net.tbl.owner"), i18n.T("net.tbl.path")}, "\t"))
			for _, s := range shares.Shares {
				st := i18n.T("net.offline")
				if s.OwnerOnline {
					st = i18n.T("net.online")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s/%d\t%s\t%s\t%s\n", s.Name, s.OwnerName, s.Proto, s.Port, s.Relation, st, pathLabel(s.Path))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if !localShown {
				printLocalPaths()
			}
			return nil
		},
	}
	cf.add(status)
	root.AddCommand(status, newNetUpCmd(), newNetDownCmd())
	return root
}
