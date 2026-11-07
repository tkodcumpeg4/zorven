package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

const (
	chOK   = "ok"
	chWarn = "warn"
	chFail = "fail"
	chSkip = "skip"
)

type doctorCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

// maxClockSkew: bundan buyuk sunucu-yerel saat farki imza/token dogrulamayi bozabilir.
const maxClockSkew = 2 * time.Minute

// clockSkew, sunucu Date basligiyla yerel saat arasindaki farki hesaplar.
func clockSkew(dateHdr string, now time.Time) (time.Duration, error) {
	t, err := http.ParseTime(dateHdr)
	if err != nil {
		return 0, err
	}
	return now.Sub(t), nil
}

// parseCapEff, /proc/self/status icerigindeki CapEff'ten CAP_NET_ADMIN (12) bitini okur.
func parseCapEff(status string) (bool, bool) {
	for _, l := range strings.Split(status, "\n") {
		if strings.HasPrefix(l, "CapEff:") {
			v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(l, "CapEff:")), 16, 64)
			if err != nil {
				return false, false
			}
			return v&(1<<12) != 0, true
		}
	}
	return false, false
}

func tunPrivilegeCheck() doctorCheck {
	c := doctorCheck{ID: "tun", Name: i18n.T("v3.doc.tun")}
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/dev/net/tun"); err != nil {
			c.Status, c.Detail = chWarn, i18n.T("v3.doc.tun.nodev")
			return c
		}
		if os.Geteuid() == 0 {
			c.Status, c.Detail = chOK, i18n.T("v3.doc.tun.root")
			return c
		}
		if b, err := os.ReadFile("/proc/self/status"); err == nil {
			if has, ok := parseCapEff(string(b)); ok && has {
				c.Status, c.Detail = chOK, "CAP_NET_ADMIN"
				return c
			}
		}
		c.Status, c.Detail = chWarn, i18n.T("v3.doc.tun.nopriv")
	case "windows":
		if isAdmin() {
			c.Status, c.Detail = chOK, i18n.T("v3.doc.tun.admin")
		} else {
			c.Status, c.Detail = chWarn, i18n.T("v3.doc.tun.nopriv")
		}
	default:
		if isAdmin() {
			c.Status, c.Detail = chOK, i18n.T("v3.doc.tun.root")
		} else {
			c.Status, c.Detail = chWarn, i18n.T("v3.doc.tun.nopriv")
		}
	}
	return c
}

func serverNetChecks(ctx context.Context, srv string, cfg config.Config, insecure bool) []doctorCheck {
	host, port, err := net.SplitHostPort(srv)
	if err != nil {
		host, port = srv, "443"
	}
	var out []doctorCheck

	dns := doctorCheck{ID: "dns", Name: i18n.T("v3.doc.dns", host)}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	addrs, derr := net.DefaultResolver.LookupHost(rctx, host)
	cancel()
	if derr != nil {
		dns.Status, dns.Detail = chFail, derr.Error()
		out = append(out, dns)
		out = append(out, doctorCheck{ID: "tls", Name: i18n.T("v3.doc.tls"), Status: chSkip, Detail: i18n.T("v3.doc.skipdns")})
		out = append(out, doctorCheck{ID: "clock", Name: i18n.T("v3.doc.clock"), Status: chSkip, Detail: i18n.T("v3.doc.skipdns")})
		return out
	}
	dns.Status, dns.Detail = chOK, strings.Join(addrs, ", ")
	out = append(out, dns)

	tlsc := doctorCheck{ID: "tls", Name: i18n.T("v3.doc.tls")}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	addr := net.JoinHostPort(host, port)
	scheme := "https"
	if insecure {
		scheme = "http"
		conn, cerr := dialer.DialContext(ctx, "tcp", addr)
		if cerr != nil {
			tlsc.Status, tlsc.Detail = chFail, cerr.Error()
		} else {
			conn.Close()
			tlsc.Status, tlsc.Detail = chWarn, i18n.T("v3.doc.tls.plain")
		}
	} else {
		tc := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if cfg.CACertPath != "" {
			if pem, rerr := os.ReadFile(cfg.CACertPath); rerr == nil {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(pem) {
					tc.RootCAs = pool
				}
			}
		}
		td := &tls.Dialer{NetDialer: dialer, Config: tc}
		conn, cerr := td.DialContext(ctx, "tcp", addr)
		if cerr != nil {
			tlsc.Status, tlsc.Detail = chFail, cerr.Error()
		} else {
			st := conn.(*tls.Conn).ConnectionState()
			conn.Close()
			tlsc.Status = chOK
			tlsc.Detail = tlsVersionName(st.Version)
			if len(st.PeerCertificates) > 0 {
				left := time.Until(st.PeerCertificates[0].NotAfter)
				tlsc.Detail += ", " + i18n.T("v3.doc.cert.days", int(left.Hours()/24))
				if left < 7*24*time.Hour {
					tlsc.Status = chWarn
				}
			}
		}
	}
	out = append(out, tlsc)

	clk := doctorCheck{ID: "clock", Name: i18n.T("v3.doc.clock")}
	if tlsc.Status == chFail {
		clk.Status, clk.Detail = chSkip, i18n.T("v3.doc.skiptls")
		return append(out, clk)
	}
	hc := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{
		DialContext:     dialer.DialContext,
		TLSClientConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	}}
	if cfg.CACertPath != "" && !insecure {
		if pem, rerr := os.ReadFile(cfg.CACertPath); rerr == nil {
			pool := x509.NewCertPool()
			if pool.AppendCertsFromPEM(pem) {
				hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
			}
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, scheme+"://"+addr+"/", nil)
	resp, herr := hc.Do(req)
	if herr != nil {
		clk.Status, clk.Detail = chWarn, herr.Error()
		return append(out, clk)
	}
	resp.Body.Close()
	skew, perr := clockSkew(resp.Header.Get("Date"), time.Now())
	switch {
	case perr != nil:
		clk.Status, clk.Detail = chWarn, i18n.T("v3.doc.clock.nodate")
	case skew > maxClockSkew || skew < -maxClockSkew:
		clk.Status, clk.Detail = chFail, i18n.T("v3.doc.clock.skew", skew.Round(time.Second))
	default:
		clk.Status, clk.Detail = chOK, i18n.T("v3.doc.clock.ok", skew.Round(time.Second))
	}
	return append(out, clk)
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	}
	return fmt.Sprintf("0x%x", v)
}

func runDoctor(ctx context.Context) doctorReport {
	var r doctorReport
	add := func(c doctorCheck) { r.Checks = append(r.Checks, c) }

	// 1. ayar dosyalari ayrisiyor mu
	cfgc := doctorCheck{ID: "config", Name: i18n.T("v3.doc.config")}
	up, _ := config.Path()
	var problems []string
	if up != "" {
		if _, err := config.LoadFile(up); err != nil {
			problems = append(problems, up+": "+err.Error())
		}
	}
	// Yetkisiz kullanicinin sistem dosyasini okuyamamasi bir ayar hatasi degildir.
	if _, err := config.LoadFile(config.SystemPath()); err != nil && !errors.Is(err, os.ErrPermission) {
		problems = append(problems, config.SystemPath()+": "+err.Error())
	}
	if len(problems) > 0 {
		cfgc.Status, cfgc.Detail = chFail, strings.Join(problems, "; ")
	} else {
		cfgc.Status, cfgc.Detail = chOK, up
	}
	add(cfgc)

	cfg, _ := config.Load()

	// 2. token
	tk := doctorCheck{ID: "token", Name: i18n.T("v3.doc.token")}
	tok := strings.TrimSpace(os.Getenv("ZORVEN_TOKEN"))
	src := "env"
	if tok == "" {
		tok, src = strings.TrimSpace(cfg.Token), "config"
	}
	switch {
	case tok == "":
		tk.Status, tk.Detail = chFail, i18n.T("v3.doc.token.none")
	case !isAgentToken(tok):
		tk.Status, tk.Detail = chFail, i18n.T("main.err.bad_token_format")
	default:
		tk.Status, tk.Detail = chOK, maskSecret(tok)+" ("+src+")"
	}
	add(tk)

	// 3-5. ag
	srv := resolveServerAddr("", cfg)
	for _, c := range serverNetChecks(ctx, srv, cfg, cfg.Insecure) {
		add(c)
	}

	// 6. yerel ajan
	ag := doctorCheck{ID: "agent", Name: i18n.T("v3.doc.agent")}
	if ep, resp, err := findAgent(); ep != nil && resp != nil {
		kind := "foreground"
		if ep.Service {
			kind = "service"
		}
		ag.Status, ag.Detail = chOK, fmt.Sprintf("%s, pid %d, %s", kind, resp.PID, resp.Version)
		l := decodeLive(resp)
		if len(l.Nodes) > 0 {
			ag.Detail = i18n.T("nodes.doctor", ag.Detail, len(l.Nodes))
			if id := l.currentNode(); id != "" {
				ag.Detail += ", " + i18n.T("nodes.lbl.node") + " " + id
			}
		} else if l.NodePinned {
			ag.Detail += ", " + i18n.T("nodes.pinned")
		}
		if l.known() && !l.Connected {
			ag.Status = chWarn
			ag.Detail += " - " + i18n.T("v3.status.disconnected")
			if l.LastError != "" {
				ag.Detail += ": " + l.LastError
			}
		}
	} else if err != nil {
		ag.Status, ag.Detail = chWarn, err.Error()
	} else {
		ag.Status, ag.Detail = chSkip, i18n.T("v3.status.agent.none")
	}
	add(ag)

	// 7. servis
	sv := doctorCheck{ID: "service", Name: i18n.T("v3.doc.service")}
	st, why := svcState()
	switch st {
	case "running":
		sv.Status = chOK
	case "stopped":
		sv.Status = chWarn
	case "not_installed":
		sv.Status = chSkip
	default:
		sv.Status, sv.Detail = chWarn, why
	}
	if sv.Detail == "" {
		sv.Detail = svcStateText(st)
	}
	add(sv)

	// 8. TUN ayricaligi
	add(tunPrivilegeCheck())

	r.OK = true
	for _, c := range r.Checks {
		if c.Status == chFail {
			r.OK = false
		}
	}
	return r
}

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "doctor",
		Short:        i18n.T("v3.doctor.short"),
		Long:         i18n.T("v3.doctor.long"),
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			rep := runDoctor(ctx)
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(rep)
			} else {
				for _, c := range rep.Checks {
					mark := map[string]string{chOK: paint("32", "[ OK ]"), chWarn: paint("33", "[WARN]"), chFail: paint("31", "[FAIL]"), chSkip: "[ -- ]"}[c.Status]
					line := mark + " " + c.Name
					if c.Detail != "" {
						line += ": " + c.Detail
					}
					fmt.Println(line)
				}
				fmt.Println()
				if rep.OK {
					fmt.Println(i18n.T("v3.doctor.pass"))
				} else {
					fmt.Println(i18n.T("v3.doctor.failed"))
				}
			}
			if !rep.OK {
				return &exitError{1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, i18n.T("v3.flag.json"))
	return cmd
}
