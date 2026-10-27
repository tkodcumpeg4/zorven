package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/shared/mcptools"
)

// newMCPCmd, `zorven mcp`: Zorven'i bir MCP sunucusu olarak sunar (Claude Code,
// Claude Desktop, Cursor ...). Varsayilan tasima stdio'dur; araclar REST API'ye
// kullanicinin API token'i ile gider (uzak /mcp uc noktasiyla AYNI arac kumesi).
func newMCPCmd() *cobra.Command {
	var apiToken, srvFlag, urlFlag, project, httpAddr string
	var insecureFlag bool

	cmd := &cobra.Command{
		Use:          "mcp",
		Short:        i18n.T("mcp.short"),
		Long:         i18n.T("mcp.long"),
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			token := strings.TrimSpace(apiToken)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN"))
			}
			if token == "" {
				return errors.New(i18n.T("mcp.err.token"))
			}
			// Istemci (ajan) token'i bir cihazin kimligidir; REST API'de gecmez.
			if strings.HasPrefix(token, "zrv_live_") || strings.HasPrefix(token, "rpsh_live_") {
				return errors.New(i18n.T("mcp.err.agenttoken"))
			}
			cfg, _ := config.Load()
			base, err := mcpBaseURL(urlFlag, srvFlag, os.Getenv("ZORVEN_SERVER"), cfg.ServerAddr, insecureFlag || cfg.Insecure)
			if err != nil {
				return err
			}

			be := &mcptools.HTTPBackend{BaseURL: base, Token: token, UserAgent: "zorven-mcp/" + version,
				Client: &http.Client{Timeout: 30 * time.Second}}
			if project != "" {
				be.Client.Transport = projectTransport{project: project}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Token'i dogrula ve kapsamlari ogren (tools/list kapsama gore kisilir).
			vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			resp, err := be.Do(vctx, mcptools.Request{Method: http.MethodGet, Path: "/api/v1/me", Project: project})
			cancel()
			if err != nil {
				return fmt.Errorf("%s: %w", i18n.T("mcp.err.unreach", base), err)
			}
			if resp.Status == http.StatusUnauthorized {
				return errors.New(i18n.T("mcp.err.unauth", base))
			}
			if resp.Status != http.StatusOK {
				return errors.New(i18n.T("mcp.err.status", resp.Status))
			}
			var me map[string]any
			if err := json.Unmarshal(resp.Body, &me); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("mcp.err.decode"), err)
			}
			scopes := mcptools.ScopesFromMe(me)
			if scopes == nil {
				scopes = []string{}
			}

			newSrv := func() *mcp.Server {
				return mcptools.NewServer(be, mcptools.Options{Version: version, Scopes: scopes})
			}

			if httpAddr != "" {
				return serveMCPHTTP(ctx, httpAddr, newSrv())
			}
			fmt.Fprintln(os.Stderr, i18n.T("mcp.stdio", base, strings.Join(scopes, ",")))
			return newSrv().Run(ctx, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&apiToken, "api-token", "", i18n.T("net.flag.apitoken"))
	cmd.Flags().StringVar(&srvFlag, "server", "", i18n.T("mcp.flag.server"))
	cmd.Flags().StringVar(&urlFlag, "url", "", i18n.T("mcp.flag.url"))
	cmd.Flags().StringVar(&project, "project", "", i18n.T("mcp.flag.project"))
	cmd.Flags().BoolVar(&insecureFlag, "insecure", false, i18n.T("mcp.flag.insecure"))
	cmd.Flags().StringVar(&httpAddr, "http", "", i18n.T("mcp.flag.http"))
	return cmd
}

// projectTransport, her isteğe varsayilan proje basligini ekler.
type projectTransport struct{ project string }

func (t projectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if r.Header.Get("X-Zorven-Project") == "" {
		r.Header.Set("X-Zorven-Project", t.project)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// mcpBaseURL, API taban adresini belirler. Oncelik: --url, --server, ZORVEN_SERVER,
// yapilandirma, zorven.app.
func mcpBaseURL(urlFlag, srvFlag, envSrv, cfgSrv string, insecure bool) (string, error) {
	if u := strings.TrimSpace(urlFlag); u != "" {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return "", errors.New(i18n.T("mcp.err.url"))
		}
		return strings.TrimRight(u, "/"), nil
	}
	srv := ""
	for _, c := range []string{srvFlag, envSrv, cfgSrv} {
		if c = strings.TrimSpace(c); c != "" {
			srv = c
			break
		}
	}
	if srv == "" || srv == "localhost:8443" {
		srv = "zorven.app:443"
	}
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	host, port, err := net.SplitHostPort(srv)
	if err != nil {
		return scheme + "://" + srv, nil
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + host, nil
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// serveMCPHTTP, yerel streamable HTTP sunar. Dinleyicide kimlik dogrulama yoktur
// (token bu surecte durur), bu yuzden YALNIZCA loopback'e baglanir ve tarayicidan
// gelen capraz-site istekleri reddedilir.
func serveMCPHTTP(ctx context.Context, addr string, srv *mcp.Server) error {
	if !isLoopbackListen(addr) {
		return errors.New(i18n.T("mcp.err.loopback", addr))
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	protected := http.NewCrossOriginProtection().Handler(h)
	hs := &http.Server{Addr: addr, Handler: protected, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	fmt.Fprintln(os.Stderr, i18n.T("mcp.http", addr))
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
