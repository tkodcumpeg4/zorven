package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// `zorven share` / `share rm` / `share ls` ve `--access`: cihazda zaten bir
// ajan (servis veya baska on plan) calisiyorsa ikinci bir sunucu baglantisi
// acmak (409) yerine istek yerel kontrol kanalindan o ajana iletilir.

// Test icin degistirilebilir kancalar.
var (
	findAgent           = localctl.Find
	callAgent           = localctl.Call
	outW      io.Writer = os.Stdout
)

// mapAgentErr, ajandan donen hatalari kullaniciya anlasilir hale getirir.
func mapAgentErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case localctl.IsUnsupported(err):
		return errors.New(i18n.T("share.err.old"))
	case strings.Contains(err.Error(), localctl.ErrNoNet.Error()):
		return errors.New(i18n.T("share.err.nonet"))
	case strings.Contains(err.Error(), "yetkisiz"):
		return errors.New(i18n.T("share.err.denied", err))
	}
	return err
}

// findRunning, calisan ajani bulur. Soket var ama yanit yoksa uyari basar ve
// (nil, false) doner; cagiran gomulu ajana dusebilir.
func findRunning() (*localctl.Endpoint, bool) {
	ep, _, err := findAgent()
	if ep == nil {
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.T("share.warn.unreach", err))
		}
		return nil, false
	}
	return ep, true
}

func printShare(si localctl.ShareInfo, ep *localctl.Endpoint) {
	kv := func(key, val string) {
		fmt.Fprintln(outW, "  "+padLabel(i18n.T(key), 12)+val)
	}
	fmt.Fprintln(outW, i18n.T("share.created", si.Name))
	kv("share.l.id", si.ID)
	kv("share.l.port", fmt.Sprintf("%d/%s", si.Port, si.Proto))
	if len(si.To) > 0 {
		var g []string
		for _, t := range si.To {
			g = append(g, t.Type+":"+t.ID)
		}
		kv("share.l.to", strings.Join(g, ", "))
	}
	if si.ExpiresUnix > 0 {
		kv("share.l.expires", time.Unix(si.ExpiresUnix, 0).Format("2006-01-02 15:04"))
	}
	kind := i18n.T("share.kind.fg")
	if ep.Service {
		kind = i18n.T("share.kind.service")
	}
	if si.Persistent {
		kv("share.l.persist", i18n.T("share.persistent", kind, si.Name))
	} else {
		kv("share.l.persist", i18n.T("share.temporary", kind))
	}
	fmt.Fprintln(outW, i18n.T("share.connect", si.Name, si.Port, si.Port))
}

// shareViaAgent: calisan ajan varsa paylasimi ona yaptirir. found=false ise
// cagiran gomulu ajanla devam eder.
func shareViaAgent(sp localctl.ShareSpec) (found bool, err error) {
	ep, ok := findRunning()
	if !ok {
		return false, nil
	}
	resp, err := callAgent(*ep, localctl.Request{Op: "share_create", Share: &sp})
	if err != nil {
		return true, mapAgentErr(err)
	}
	if len(resp.Shares) == 0 {
		return true, errors.New(i18n.T("share.err.noinfo"))
	}
	printShare(resp.Shares[0], ep)
	return true, nil
}

func noAgentErr() error {
	return errors.New(i18n.T("share.err.noagent"))
}

func newShareRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id|ad>",
		Short: i18n.T("share.rm.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, ok := findRunning()
			if !ok {
				return noAgentErr()
			}
			resp, err := callAgent(*ep, localctl.Request{Op: "share_delete", ShareID: args[0]})
			if err != nil {
				return mapAgentErr(err)
			}
			name := args[0]
			if len(resp.Shares) > 0 {
				name = resp.Shares[0].Name + " (" + resp.Shares[0].ID + ")"
			}
			fmt.Fprintln(outW, i18n.T("share.rm.done", name))
			return nil
		},
	}
}

// pathLabel, yol ozetini kullaniciya gosterilecek metne cevirir.
func pathLabel(p string) string {
	switch p {
	case "direct":
		return i18n.T("net.path.l.direct")
	case "relay":
		return i18n.T("net.path.l.relay")
	case "mixed":
		return i18n.T("net.path.l.mixed")
	}
	return "-"
}

func newShareLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: i18n.T("share.ls.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, ok := findRunning()
			if !ok {
				return noAgentErr()
			}
			resp, err := callAgent(*ep, localctl.Request{Op: "share_list"})
			if err != nil {
				return mapAgentErr(err)
			}
			if len(resp.Shares) == 0 {
				fmt.Fprintln(outW, i18n.T("share.ls.none"))
				return nil
			}
			tw := newTable(outW)
			fmt.Fprintln(tw, strings.Join([]string{i18n.T("share.tbl.name"), "ID", "PORT", i18n.T("share.tbl.to"), i18n.T("net.tbl.path"), i18n.T("share.tbl.connect")}, "\t"))
			for _, s := range resp.Shares {
				var to []string
				for _, t := range s.To {
					to = append(to, t.Type+":"+t.ID)
				}
				who := strings.Join(to, ",")
				if who == "" {
					who = i18n.T("share.ls.peers", len(s.Allow))
				}
				fmt.Fprintf(tw, "%s\t%s\t%d/%s\t%s\t%s\tzorven access %s:%d --local %d\n", s.Name, s.ID, s.Port, s.Proto, who, pathLabel(s.Path), s.Name, s.Port, s.Port)
			}
			return tw.Flush()
		},
	}
}

// accessViaAgent: `zorven --access ad=port` calisan ajana iletilir.
// specs ham "hedef=yerel" ciftleridir (main.go ile ayni bicim).
func accessViaAgent(specs []string) (found bool, err error) {
	if len(specs) == 0 {
		return false, nil
	}
	type pair struct{ tgt, loc string }
	var ps []pair
	for _, spec := range specs {
		tgt, loc, ok := strings.Cut(spec, "=")
		if !ok || strings.TrimSpace(tgt) == "" || strings.TrimSpace(loc) == "" {
			return false, errors.New(i18n.T("share.err.access", spec))
		}
		loc = strings.TrimSpace(loc)
		if !strings.Contains(loc, ":") {
			loc = "127.0.0.1:" + loc
		}
		ps = append(ps, pair{strings.TrimSpace(tgt), loc})
	}
	ep, ok := findRunning()
	if !ok {
		return false, nil
	}
	for _, p := range ps {
		resp, err := callAgent(*ep, localctl.Request{Op: "access_open", Target: p.tgt, Local: p.loc})
		if err != nil {
			return true, mapAgentErr(err)
		}
		fmt.Fprintln(outW, i18n.T("share.access.open", p.tgt, resp.Local))
	}
	return true, nil
}

// conflictHint, 409 (bu token ile baska istemci bagli) icin ipucu metni.
func conflictHint() string { return i18n.T("share.conflict") }
