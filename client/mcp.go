package main

import (
	"context"
	"encoding/json"
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
	"github.com/tkodcumpeg4/zorven/shared/mcptools"
)

// newMCPCmd, `zorven mcp`: Zorven'i bir MCP sunucusu olarak sunar (Claude Code,
// Claude Desktop, Cursor ...). Varsayilan tasima stdio'dur; araclar REST API'ye
// kullanicinin API token'i ile gider (uzak /mcp uc noktasiyla AYNI arac kumesi).
func newMCPCmd() *cobra.Command {
	var apiToken, srvFlag, urlFlag, project, httpAddr string
	var insecureFlag bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Zorven'i MCP sunucusu olarak calistirir (stdio; Claude/Cursor icin)",
		Long: "Zorven araclarini (cihaz, tunel, alan adi, istek logu, metrik, web kapisi) Model Context\n" +
			"Protocol uzerinden sunar. Silme araclari yoktur.\n\n" +
			"API token gerekir (panel > API Tokenlari). Salt okuma icin *:read kapsamlari yeterlidir;\n" +
			"token'da olmayan kapsamlarin araclari listelenmez.\n\n" +
			"  ZORVEN_API_TOKEN=zrv_api_... zorven mcp\n\n" +
			"Sunucu adresi `zorven` yapilandirmasindan (varsayilan zorven.app) alinir; --server/--url ile degisir.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			token := strings.TrimSpace(apiToken)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN"))
			}
			if token == "" {
				return fmt.Errorf("API token gerekli: --api-token veya ZORVEN_API_TOKEN (panel > API Tokenlari)")
			}
			// Istemci (ajan) token'i bir cihazin kimligidir; REST API'de gecmez.
			if strings.HasPrefix(token, "zrv_live_") || strings.HasPrefix(token, "rpsh_live_") {
				return fmt.Errorf("bu bir istemci (ajan) token'i; zorven mcp bir API token (zrv_api_...) ister")
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
				return fmt.Errorf("%s adresine ulasilamadi: %w", base, err)
			}
			if resp.Status == http.StatusUnauthorized {
				return fmt.Errorf("API token gecersiz veya suresi dolmus (%s)", base)
			}
			if resp.Status != http.StatusOK {
				return fmt.Errorf("sunucu beklenmeyen yanit verdi: HTTP %d", resp.Status)
			}
			var me map[string]any
			if err := json.Unmarshal(resp.Body, &me); err != nil {
				return fmt.Errorf("sunucu yaniti cozulemedi: %w", err)
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
			fmt.Fprintf(os.Stderr, "zorven mcp: %s (stdio), kapsamlar: %s\n", base, strings.Join(scopes, ","))
			return newSrv().Run(ctx, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&apiToken, "api-token", "", "API token (ZORVEN_API_TOKEN ile de verilebilir)")
	cmd.Flags().StringVar(&srvFlag, "server", "", "Zorven sunucusu (host:port; varsayilan yapilandirmadan)")
	cmd.Flags().StringVar(&urlFlag, "url", "", "API taban adresi (or. https://zorven.app); --server'i ezer")
	cmd.Flags().StringVar(&project, "project", "", "varsayilan proje (slug veya kimlik)")
	cmd.Flags().BoolVar(&insecureFlag, "insecure", false, "https yerine http (yalnizca lokal gelistirme)")
	cmd.Flags().StringVar(&httpAddr, "http", "", "stdio yerine yerel streamable HTTP dinle (yalnizca loopback, or. 127.0.0.1:8765)")
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
			return "", fmt.Errorf("--url http:// veya https:// ile baslamali")
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
		return fmt.Errorf("%s loopback degil: yerel MCP HTTP yalnizca 127.0.0.1/localhost uzerinde acilabilir", addr)
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
	fmt.Fprintf(os.Stderr, "zorven mcp: http://%s (yalnizca yerel). Cikmak icin Ctrl+C.\n", addr)
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
