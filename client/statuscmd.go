package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// exitError, mesaj basmadan belirli bir cikis koduyla bitmek icin kullanilir.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// Cikis kodlari (zorven status): 0 calisiyor ve bagli; 1 hicbir sey calismiyor;
// 2 calisan bir ajan/servis var ama sunucuya bagli degil.
const (
	exitOK           = 0
	exitNothing      = 1
	exitNotConnected = 2
)

// svcState: "running" | "stopped" | "not_installed" | "unknown"
func svcState() (string, string) {
	s, err := getService()
	if err != nil {
		return "unknown", err.Error()
	}
	st, err := s.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return "not_installed", ""
		}
		return "unknown", err.Error()
	}
	switch st {
	case service.StatusRunning:
		return "running", ""
	case service.StatusStopped:
		return "stopped", ""
	}
	return "unknown", fmt.Sprint(int(st))
}

func svcStateText(st string) string {
	switch st {
	case "running":
		return i18n.T("main.val.svc_running")
	case "stopped":
		return i18n.T("main.val.svc_stopped")
	case "not_installed":
		return i18n.T("main.val.svc_missing")
	}
	return i18n.T("v3.status.svc.unknown")
}

type statusAgent struct {
	Running  bool   `json:"running"`
	Kind     string `json:"kind,omitempty"` // service | foreground
	PID      int    `json:"pid,omitempty"`
	Version  string `json:"version,omitempty"`
	Terminal string `json:"terminal,omitempty"` // "cap/eff"
	Screen   string `json:"screen,omitempty"`
	Error    string `json:"error,omitempty"`
	liveStatus
}

type statusDoc struct {
	Version  string      `json:"version"`
	Protocol string      `json:"protocol"`
	Config   cfgView     `json:"config"`
	Service  string      `json:"service"`
	Agent    statusAgent `json:"agent"`
	Tun      any         `json:"tun,omitempty"`
	Exit     int         `json:"exit_code"`
}

func fmtBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}

func capText(cap, eff bool) string {
	return i18n.T("ctl.caps.line", padLabel(onOff(cap), 6), onOff(eff))
}

// collectStatus, yerel durumu toplar (ag kullanmaz).
func collectStatus() statusDoc {
	d := statusDoc{Version: version, Protocol: protocol.Version, Config: effectiveConfig()}
	d.Service, _ = svcState()
	ep, r, err := findAgent()
	switch {
	case ep != nil && r != nil:
		d.Agent.Running = true
		d.Agent.Kind = "foreground"
		if ep.Service {
			d.Agent.Kind = "service"
		}
		d.Agent.PID, d.Agent.Version = r.PID, r.Version
		d.Agent.Terminal = fmt.Sprintf("%v/%v", r.Caps.TerminalCap, r.Caps.TerminalEff)
		d.Agent.Screen = fmt.Sprintf("%v/%v", r.Caps.ScreenCap, r.Caps.ScreenEff)
		d.Agent.liveStatus = decodeLive(r)
		if resp, terr := callAgent(*ep, localctl.Request{Op: "net_status"}); terr == nil && resp.Tun != nil {
			d.Tun = resp.Tun
		}
	case err != nil:
		d.Agent.Error = err.Error()
	}
	switch {
	case d.Agent.Running && (!d.Agent.known() || d.Agent.Connected):
		// Eski ajan baglanti alani gondermez: calisiyor say.
		d.Exit = exitOK
	case d.Agent.Running || d.Service == "running":
		d.Exit = exitNotConnected
	default:
		d.Exit = exitNothing
	}
	return d
}

func printStatus(d statusDoc) {
	title := i18n.T("main.status.title")
	fmt.Println(title)
	fmt.Println(strings.Repeat("-", len([]rune(title))))
	row := func(label, value string) { fmt.Println(padLabel(label, 16) + value) }

	row(i18n.T("main.lbl.version"), i18n.T("main.val.version", d.Version, d.Protocol))
	row(i18n.T("main.lbl.file"), d.Config.UserFile)
	for _, e := range d.Config.Entries {
		switch e.Key {
		case "server":
			row(i18n.T("main.lbl.server"), e.Value+"  ("+e.Source+")")
		case "token":
			if e.Value != "" {
				row(i18n.T("main.lbl.token"), i18n.T("main.val.token_saved", e.Value)+"  ("+e.Source+")")
			} else {
				row(i18n.T("main.lbl.token"), i18n.T("main.val.token_none"))
			}
		case "insecure":
			if e.Value == "true" {
				row(i18n.T("main.lbl.insecure"), paint("33", i18n.T("v3.status.insecure.on")))
			}
		}
	}
	row(i18n.T("main.lbl.service"), svcStateText(d.Service))

	a := d.Agent
	fmt.Println()
	if !a.Running {
		if a.Error != "" {
			row(i18n.T("v3.status.agent"), i18n.T("ctl.status.unreachable", a.Error))
		} else {
			row(i18n.T("v3.status.agent"), i18n.T("v3.status.agent.none"))
		}
		return
	}
	kind := i18n.T("ctl.kind.fg")
	if a.Kind == "service" {
		kind = i18n.T("ctl.kind.svc")
	}
	row(i18n.T("v3.status.agent"), i18n.T("v3.status.agent.run", kind, a.PID, a.Version))
	if a.known() {
		conn := paint("31", i18n.T("v3.status.disconnected"))
		if a.Connected {
			conn = paint("32", i18n.T("v3.status.connected"))
		}
		if a.State != "" {
			conn += "  [" + a.State + "]"
		}
		row(i18n.T("v3.status.conn"), conn)
		if a.Server != "" {
			row(i18n.T("main.lbl.server"), a.Server)
		}
		if id := a.currentNode(); id != "" {
			row(i18n.T("nodes.lbl.node"), id)
		}
		if up, ok := a.uptime(); ok {
			row(i18n.T("v3.status.uptime"), up.String())
		}
		row(i18n.T("v3.status.reconnects"), fmt.Sprint(a.Reconnects))
		if a.LastError != "" {
			row(i18n.T("v3.status.lasterr"), a.LastError)
		}
	} else {
		row(i18n.T("v3.status.conn"), i18n.T("v3.status.oldagent"))
	}
	printNodes(a.liveStatus, row)
	if r := a.Terminal; r != "" {
		var tc, te, sc, se bool
		fmt.Sscanf(a.Terminal, "%t/%t", &tc, &te)
		fmt.Sscanf(a.Screen, "%t/%t", &sc, &se)
		row(i18n.T("ctl.caps.terminal"), capText(tc, te))
		row(i18n.T("ctl.caps.screen"), capText(sc, se))
		if (tc && !te) || (sc && !se) {
			fmt.Println(i18n.T("ctl.caps.restricted"))
		}
	}
	if len(a.Tunnels) > 0 {
		fmt.Println()
		fmt.Println(i18n.T("v3.status.tunnels"))
		tw := newTable(os.Stdout)
		fmt.Fprintln(tw, "  "+i18n.T("v3.tbl.name")+"\tURL\t"+i18n.T("v3.tbl.target")+"\t"+i18n.T("v3.tbl.requests")+"\t"+i18n.T("v3.tbl.errors")+"\tp95\t"+i18n.T("v3.tbl.traffic"))
		for _, t := range a.Tunnels {
			p95 := "-"
			if t.P95Ms > 0 {
				p95 = fmt.Sprintf("%.0f ms", t.P95Ms)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%d\t%s\t%s\n", dash(t.Name), dash(t.URL), dash(t.Target), t.Requests, t.Errors, p95, fmtBytes(t.BytesIn)+" / "+fmtBytes(t.BytesOut))
		}
		tw.Flush()
	}
	if n := len(a.Disconnects); n > 0 {
		fmt.Println()
		fmt.Println(i18n.T("v3.status.disc"))
		start := 0
		if n > 5 {
			start = n - 5
		}
		for _, x := range a.Disconnects[start:] {
			at := x.At
			if t, err := time.Parse(time.RFC3339, x.At); err == nil {
				at = t.Local().Format("2006-01-02 15:04:05")
			}
			fmt.Printf("  %s  %s\n", at, x.Reason)
		}
	}
	if ti, ok := d.Tun.(*localctl.TunInfo); ok && ti != nil {
		fmt.Println()
		printTun(ti)
	}
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "status",
		Short:        i18n.T("main.cmd.status"),
		Long:         i18n.T("v3.status.long"),
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			d := collectStatus()
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(d); err != nil {
					return err
				}
			} else {
				printStatus(d)
				printLegacyHint()
			}
			if d.Exit != exitOK {
				return &exitError{d.Exit}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, i18n.T("v3.flag.json"))
	return cmd
}
