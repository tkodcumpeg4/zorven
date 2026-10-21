package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/config"
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
		return "ACIK"
	}
	return "kapali"
}

func printCaps(r *localctl.Response, ep *localctl.Endpoint) {
	kind := "on plan istemci"
	if ep.Service {
		kind = "arka plan servisi"
	}
	fmt.Printf("Calisan istemci: %s (pid %d, surum %s)\n", kind, r.PID, r.Version)
	fmt.Printf("  Terminal:        yerel izin %-6s etkin %s\n", onOff(r.Caps.TerminalCap), onOff(r.Caps.TerminalEff))
	fmt.Printf("  Ekran paylasimi: yerel izin %-6s etkin %s\n", onOff(r.Caps.ScreenCap), onOff(r.Caps.ScreenEff))
	if (r.Caps.TerminalCap && !r.Caps.TerminalEff) || (r.Caps.ScreenCap && !r.Caps.ScreenEff) {
		fmt.Println("  (yerel izin acik ama panel/sunucu ayari kisitliyor: panelden izni acin)")
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
	word := map[bool]string{true: "acildi", false: "kapatildi"}
	var parts []string
	if t != nil {
		parts = append(parts, "uzaktan terminal "+word[*t])
	}
	if s != nil {
		parts = append(parts, "ekran paylasimi "+word[*s])
	}
	fmt.Printf("Calisan istemcide %s.\n", strings.Join(parts, ", "))
	// Servis hedefinde kalicilik varsayilan (yeniden baslayinca eski haline donmesin).
	if persist || ep.Service {
		if perr := persistCapsToConfig(ep, t, s); perr != nil {
			fmt.Fprintf(os.Stderr, "Uyari: ayar kalici yazilamadi: %v\n", perr)
		} else {
			fmt.Println("Ayar yerel yapilandirmaya kaydedildi; yeniden baslatmada korunur.")
		}
	} else {
		fmt.Println("Bu degisiklik yalniz calisma suresince gecerli; kalici yapmak icin --persist ekleyin.")
	}
	printCaps(&resp, ep)
	slog.Debug("izin degisikligi gonderildi", "soket", ep.Path)
	return true, nil
}

func newCtlCmds() []*cobra.Command {
	mk := func(use string, enable bool) *cobra.Command {
		var persist bool
		verb := map[bool]string{true: "acar", false: "kapatir"}[enable]
		c := &cobra.Command{
			Use:       use + " <terminal|screen>",
			Short:     "Calisan istemcide uzaktan terminal veya ekran paylasimini " + verb + " (yeniden baslatmadan)",
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
					return fmt.Errorf("gecersiz hedef %q: terminal veya screen", args[0])
				}
				found, err := forwardCaps(t, s, persist)
				if !found {
					if err != nil {
						return fmt.Errorf("calisan istemciye erisilemedi: %w", err)
					}
					return fmt.Errorf("calisan istemci bulunamadi; once 'zorven' veya 'zorven service start' ile baslatin (ya da --enable-%s ile baslatin)", args[0])
				}
				return err
			},
		}
		c.Flags().BoolVar(&persist, "persist", false, "degisikligi yerel ayara da yaz (yeniden baslatmada korunur; servis icin zaten varsayilan)")
		return c
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Calisan istemcinin terminal/ekran izin durumunu gosterir",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, r, err := localctl.Find()
			if ep == nil {
				if err != nil {
					fmt.Printf("Calisan istemciye erisilemedi: %v\n", err)
				} else {
					fmt.Println("Calisan istemci bulunamadi (yerel kontrol kanali yok).")
				}
				return nil
			}
			printCaps(r, ep)
			return nil
		},
	}
	return []*cobra.Command{mk("enable", true), mk("disable", false), status}
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
		fmt.Println("Yeni baglanti acilmadi: istemci zaten calisiyor.")
	}
	return true, err
}
