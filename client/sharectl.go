package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
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
		return errors.New("calisan istemci bu islemi desteklemiyor (eski surum); 'zorven update' ile guncelleyip yeniden baslatin")
	case strings.Contains(err.Error(), localctl.ErrNoNet.Error()):
		return errors.New("calisan istemcide Zorven Net etkin degil (sunucu wg_net sunmuyor veya istemci --no-net ile basladi)")
	case strings.Contains(err.Error(), "yetkisiz"):
		return fmt.Errorf("calisan istemci bu kullaniciya yerel erisim vermedi (%v); istemciyle ayni kullanici/sudo ile deneyin", err)
	}
	return err
}

// findRunning, calisan ajani bulur. Soket var ama yanit yoksa uyari basar ve
// (nil, false) doner; cagiran gomulu ajana dusebilir.
func findRunning() (*localctl.Endpoint, bool) {
	ep, _, err := findAgent()
	if ep == nil {
		if err != nil {
			fmt.Fprintf(os.Stderr, "Uyari: calisan istemciye erisilemedi (%v); yerel kontrol kanali atlandi.\n", err)
		}
		return nil, false
	}
	return ep, true
}

func printShare(si localctl.ShareInfo, ep *localctl.Endpoint) {
	fmt.Fprintf(outW, "Paylasim olusturuldu: %s\n", si.Name)
	fmt.Fprintf(outW, "  id:      %s\n", si.ID)
	fmt.Fprintf(outW, "  port:    %d/%s\n", si.Port, si.Proto)
	if len(si.To) > 0 {
		var g []string
		for _, t := range si.To {
			g = append(g, t.Type+":"+t.ID)
		}
		fmt.Fprintf(outW, "  alicilar: %s\n", strings.Join(g, ", "))
	}
	if si.ExpiresUnix > 0 {
		fmt.Fprintf(outW, "  bitis:   %s\n", time.Unix(si.ExpiresUnix, 0).Format("2006-01-02 15:04"))
	}
	kind := "on plan istemci"
	if ep.Service {
		kind = "arka plan servisi"
	}
	if si.Persistent {
		fmt.Fprintf(outW, "  kalicilik: kalici (%s yeniden baslasa da surer; silmek icin: zorven share rm %s)\n", kind, si.Name)
	} else {
		fmt.Fprintf(outW, "  kalicilik: %s calistigi surece (kalici yapmak icin --persist)\n", kind)
	}
	fmt.Fprintf(outW, "Baglanmak icin (alici): zorven access %s:%d --local %d\n", si.Name, si.Port, si.Port)
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
		return true, errors.New("calisan istemci paylasim bilgisi dondurmedi")
	}
	printShare(resp.Shares[0], ep)
	return true, nil
}

func noAgentErr() error {
	return errors.New("calisan istemci bulunamadi; paylasimlar yalniz calisan bir istemci (zorven / zorven service start) uzerinden yonetilir veya panelden (Paylasimlar) silinir")
}

func newShareRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id|ad>",
		Short: "Calisan istemcinin sundugu bir paylasimi siler",
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
			fmt.Fprintf(outW, "Paylasim silindi: %s\n", name)
			return nil
		},
	}
}

func newShareLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "Calisan istemcinin sundugu paylasimlari listeler",
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
				fmt.Fprintln(outW, "Paylasim yok.")
				return nil
			}
			tw := tabwriter.NewWriter(outW, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "AD\tID\tPORT\tALICILAR\tBAGLANTI")
			for _, s := range resp.Shares {
				var to []string
				for _, t := range s.To {
					to = append(to, t.Type+":"+t.ID)
				}
				who := strings.Join(to, ",")
				if who == "" {
					who = fmt.Sprintf("%d es", len(s.Allow))
				}
				fmt.Fprintf(tw, "%s\t%s\t%d/%s\t%s\tzorven access %s:%d --local %d\n", s.Name, s.ID, s.Port, s.Proto, who, s.Name, s.Port, s.Port)
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
			return false, fmt.Errorf("gecersiz --access %q (bicim: paylasim[:port]=yerel_port)", spec)
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
		fmt.Fprintf(outW, "Calisan istemcide erisim acildi: %s -> %s (istemci calistigi surece)\n", p.tgt, resp.Local)
	}
	return true, nil
}

// conflictHint, 409 (bu token ile baska istemci bagli) icin ipucu metni.
const conflictHint = "Bu token ile baska bir istemci bagli ama yerel kontrol kanalindan ulasilamadi " +
	"(istemci eski surum veya baska kullanicida olabilir). Istemciyi 'zorven update' ile guncelleyip yeniden baslatin, " +
	"ayni kullanici/sudo ile deneyin ya da paylasimi panelden (Paylasimlar) olusturun."
