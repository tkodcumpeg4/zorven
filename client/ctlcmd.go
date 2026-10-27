package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// agentCtl, agent.Agent'i localctl.Controller'a uyarlar.
type agentCtl struct{ ag *agent.Agent }

func toCtl(c agent.Caps) localctl.Caps {
	return localctl.Caps{TerminalCap: c.TerminalCap, ScreenCap: c.ScreenCap, TerminalEff: c.TerminalEff, ScreenEff: c.ScreenEff}
}
func (a agentCtl) Current() localctl.Caps { return toCtl(a.ag.Caps()) }
func (a agentCtl) Set(t, s *bool) localctl.Caps {
	return toCtl(a.ag.SetCaps(t, s))
}

// localctl.NetController: paylasim/erisim islemleri ajana devredilir.
func (a agentCtl) ShareCreate(ctx context.Context, sp localctl.ShareSpec) (localctl.ShareInfo, error) {
	return a.ag.ShareCreate(ctx, sp)
}
func (a agentCtl) ShareDelete(ctx context.Context, id string) (localctl.ShareInfo, error) {
	return a.ag.ShareDelete(ctx, id)
}
func (a agentCtl) ShareList() ([]localctl.ShareInfo, error) { return a.ag.ShareList() }
func (a agentCtl) AccessOpen(ctx context.Context, target, local string) (string, error) {
	return a.ag.AccessOpen(ctx, target, local)
}
func (a agentCtl) AccessClose(local string) error { return a.ag.AccessClose(local) }

// localctl.TunController: tam ag katmani (Mod B, OS TUN).
func (a agentCtl) NetUp(ctx context.Context) (localctl.TunInfo, error)   { return a.ag.NetUp(ctx) }
func (a agentCtl) NetDown(ctx context.Context) (localctl.TunInfo, error) { return a.ag.NetDown(ctx) }
func (a agentCtl) NetTunStatus() (localctl.TunInfo, error)               { return a.ag.NetTunStatus() }

// TriggerUpdate, calisan ajanda elle guncelleme kontrolunu arka planda baslatir
// (`zorven update` diskteki ikiliyi guncelledikten sonra calisan sureci de
// yeni surume tasimak icin). Yeni surum yoksa hicbir sey olmaz.
func (a agentCtl) TriggerUpdate() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := a.ag.CheckUpdateNow(ctx); err != nil {
			a.ag.Log.Warn("yerel kontrol: guncelleme yapilamadi", "hata", err)
		}
	}()
}

// startLocalCtl, ajan icin yerel kontrol soketini baslatir. Hata oldugunda
// ajan yine calisir (yalniz calisirken izin degistirme devre disi kalir).
func startLocalCtl(ctx context.Context, ag *agent.Agent, ep localctl.Endpoint) {
	err := localctl.Serve(ctx, ep, agentCtl{ag}, version, func(what string) {
		ag.Log.Warn("yerel kontrol: izin degisikligi", "degisim", what, "soket", ep.Path)
	})
	if err != nil {
		ag.Log.Warn("yerel kontrol kanali acilamadi; calisirken izin degistirme kapali", "hata", err)
	}
}

func onOff(b bool) string {
	if b {
		return i18n.T("set.on")
	}
	return i18n.T("set.off")
}

func printCaps(r *localctl.Response, ep *localctl.Endpoint) {
	kind := i18n.T("ctl.kind.fg")
	if ep.Service {
		kind = i18n.T("ctl.kind.svc")
	}
	fmt.Println(i18n.T("ctl.caps.header", kind, r.PID, r.Version))
	fmt.Println("  " + padLabel(i18n.T("ctl.caps.terminal"), 18) + i18n.T("ctl.caps.line", padLabel(onOff(r.Caps.TerminalCap), 6), onOff(r.Caps.TerminalEff)))
	fmt.Println("  " + padLabel(i18n.T("ctl.caps.screen"), 18) + i18n.T("ctl.caps.line", padLabel(onOff(r.Caps.ScreenCap), 6), onOff(r.Caps.ScreenEff)))
	if (r.Caps.TerminalCap && !r.Caps.TerminalEff) || (r.Caps.ScreenCap && !r.Caps.ScreenEff) {
		fmt.Println(i18n.T("ctl.caps.restricted"))
	}
}

// persistCapsToConfig, tavani yerel ayar dosyasina yazar (servis: sistem dosyasi).
func persistCapsToConfig(ep *localctl.Endpoint, t, s *bool) error {
	var cfg config.Config
	var save func(config.Config) error
	if ep.Service {
		cfg, _ = config.LoadFile(config.SystemPath())
		save = config.SaveSystem
	} else {
		p, err := config.Path()
		if err != nil {
			return err
		}
		cfg, _ = config.LoadFile(p)
		save = func(c config.Config) error { return config.SaveFile(p, c) }
	}
	if t != nil {
		cfg.EnableTerminal = *t
	}
	if s != nil {
		cfg.EnableScreen = *s
	}
	return save(cfg)
}

// forwardCaps, calisan ajana izin degisikligini iletir. Ajan yoksa found=false.
func forwardCaps(t, s *bool, persist bool) (found bool, err error) {
	ep, _, ferr := localctl.Find()
	if ep == nil {
		return false, ferr
	}
	resp, err := localctl.Call(*ep, localctl.Request{Op: "set", Terminal: t, Screen: s})
	if err != nil {
		return true, err
	}
	var parts []string
	if t != nil {
		parts = append(parts, i18n.T(map[bool]string{true: "ctl.fwd.term.on", false: "ctl.fwd.term.off"}[*t]))
	}
	if s != nil {
		parts = append(parts, i18n.T(map[bool]string{true: "ctl.fwd.screen.on", false: "ctl.fwd.screen.off"}[*s]))
	}
	fmt.Println(i18n.T("ctl.fwd.result", strings.Join(parts, ", ")))
	// Servis hedefinde kalicilik varsayilan (yeniden baslayinca eski haline donmesin).
	if persist || ep.Service {
		if perr := persistCapsToConfig(ep, t, s); perr != nil {
			fmt.Fprint(os.Stderr, i18n.T("ctl.fwd.persist.err", perr))
		} else {
			fmt.Println(i18n.T("ctl.fwd.persisted"))
		}
	} else {
		fmt.Println(i18n.T("ctl.fwd.temp"))
	}
	printCaps(&resp, ep)
	slog.Debug("izin degisikligi gonderildi", "soket", ep.Path)
	return true, nil
}

func newCtlCmds() []*cobra.Command {
	mk := func(use string, enable bool) *cobra.Command {
		var persist bool
		shortKey := map[bool]string{true: "ctl.cmd.enable", false: "ctl.cmd.disable"}[enable]
		c := &cobra.Command{
			Use:       use + " <terminal|screen>",
			Short:     i18n.T(shortKey),
			Example:   "  zorven " + use + " terminal\n  zorven " + use + " screen --persist",
			Args:      cobra.ExactArgs(1),
			ValidArgs: []string{"terminal", "screen"},
			RunE: func(cmd *cobra.Command, args []string) error {
				v := enable
				var t, s *bool
				switch strings.ToLower(args[0]) {
				case "terminal":
					t = &v
				case "screen", "ekran":
					s = &v
				default:
					return errors.New(i18n.T("ctl.err.target", args[0]))
				}
				found, err := forwardCaps(t, s, persist)
				if !found {
					if err != nil {
						return fmt.Errorf("%s: %w", i18n.T("ctl.err.unreachable"), err)
					}
					return errors.New(i18n.T("ctl.err.notfound.hint", args[0]))
				}
				return err
			},
		}
		c.Flags().BoolVar(&persist, "persist", false, i18n.T("ctl.flag.persist"))
		return c
	}
	status := &cobra.Command{
		Use:   "status",
		Short: i18n.T("ctl.cmd.status"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, r, err := localctl.Find()
			if ep == nil {
				if err != nil {
					fmt.Print(i18n.T("ctl.status.unreachable", err))
				} else {
					fmt.Println(i18n.T("ctl.status.notfound"))
				}
				return nil
			}
			printCaps(r, ep)
			return nil
		},
	}
	debugCmd := &cobra.Command{Use: "debug", Short: i18n.T("ctl.cmd.debug")}
	debugCmd.AddCommand(&cobra.Command{
		Use:   "stacks",
		Short: i18n.T("ctl.cmd.stacks"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, _, ferr := localctl.Find()
			if ep == nil {
				if ferr != nil {
					return ferr
				}
				return errors.New(i18n.T("ctl.err.notfound"))
			}
			resp, err := localctl.Call(*ep, localctl.Request{Op: "debug_stacks"})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Println(i18n.T("ctl.stacks.written"), resp.File)
			fmt.Println(i18n.T("ctl.stacks.log"), filepath.Join(localctl.LogDir(), "agent.log"))
			return nil
		},
	})
	return []*cobra.Command{mk("enable", true), mk("disable", false), status, debugCmd}
}

// tryForwardFlags: `zorven --enable-terminal` gibi cagrilarda calisan ajan
// varsa degisikligi ona iletir; true donerse cagiran yeni baglanti ACMAZ.
func tryForwardFlags(enT, enS, noT, noS, persist bool) (bool, error) {
	var t, s *bool
	yes, no := true, false
	switch {
	case noT:
		t = &no
	case enT:
		t = &yes
	}
	switch {
	case noS:
		s = &no
	case enS:
		s = &yes
	}
	if t == nil && s == nil {
		return false, nil
	}
	found, err := forwardCaps(t, s, persist)
	if !found {
		return false, nil
	}
	if err == nil {
		fmt.Println(i18n.T("ctl.fwd.noconn"))
	}
	return true, err
}
