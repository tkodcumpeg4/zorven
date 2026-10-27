package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// Zorven Net Mod B (W3): `zorven net up | down | status`. Komutlar TUN'u KENDILERI
// acmaz; CALISAN ajana/servise (yerel IPC) yaptirir. TUN ayricalik ister (Linux:
// CAP_NET_ADMIN/root, Windows: SYSTEM/yonetici, macOS: root); servis bu yetkiyle
// calisir. Mod B'de ayni anda yalniz bu cihazin organizasyonu icin TUN acik olur.

func tunInstallHint() string { return i18n.T("net.tun.install") }

// tunCall, calisan ajana net_* IPC komutu yollar ve kullaniciya yonelik hata uretir.
func tunCall(op string) (*localctl.TunInfo, error) {
	ep, _, ferr := findAgent()
	if ep == nil {
		if ferr != nil {
			return nil, errors.New(i18n.T("net.tun.err.unreachable", ferr, accessHint()))
		}
		if _, serr := os.Stat(localctl.ServiceEndpoint().Path); serr != nil && errors.Is(serr, os.ErrPermission) {
			return nil, errors.New(i18n.T("net.tun.err.perm", accessHint()))
		}
		return nil, errors.New(i18n.T("net.tun.err.none", tunInstallHint()))
	}
	resp, err := callAgent(*ep, localctl.Request{Op: op})
	if err != nil {
		return nil, mapTunErr(err, ep)
	}
	if resp.Tun == nil {
		return nil, errors.New(i18n.T("net.tun.err.badresp"))
	}
	return resp.Tun, nil
}

func accessHint() string {
	if runtime.GOOS == "windows" {
		return i18n.T("net.tun.hint.win")
	}
	return i18n.T("net.tun.hint.unix")
}

func mapTunErr(err error, ep *localctl.Endpoint) error {
	if localctl.IsUnsupported(err) {
		return errors.New(i18n.T("net.tun.err.old"))
	}
	if strings.Contains(err.Error(), "yetkisiz") {
		return fmt.Errorf("%v\n%s", err, accessHint())
	}
	if strings.Contains(err.Error(), localctl.ErrNoNet.Error()) {
		return errors.New(i18n.T("net.tun.err.nonet"))
	}
	if ep != nil && !ep.Service && (strings.Contains(err.Error(), "yetki") || strings.Contains(err.Error(), "CAP_NET_ADMIN")) {
		return fmt.Errorf("%v\n%s", err, tunInstallHint())
	}
	return err
}

func printTun(ti *localctl.TunInfo) {
	kv := func(label, val string) {
		fmt.Fprintln(outW, "  "+padLabel(label, 10)+val)
	}
	if ti.Active {
		fmt.Fprintln(outW, i18n.T("net.tun.on"))
		kv(i18n.T("net.tun.l.device"), ti.Name)
		kv(i18n.T("net.tun.l.iface"), i18n.T("net.tun.iface", ti.Interface, ti.IP, ti.Pool, ti.MTU))
		if ti.DNSOK {
			kv("DNS:", i18n.T("net.tun.dns.ok", ti.Resolver, ti.DNSMethod, ti.DNSServed))
		} else {
			kv("DNS:", i18n.T("net.tun.dns.fail", ti.DNSNote))
			kv("", i18n.T("net.tun.dns.fail2"))
		}
		kv(i18n.T("net.tun.l.filter"), i18n.T("net.tun.dropped", ti.DroppedIn, ti.DroppedOut))
	} else {
		fmt.Fprint(outW, i18n.T("net.tun.off"))
		switch {
		case !ti.ServerTun && ti.IP == "":
			fmt.Fprintln(outW, i18n.T("net.tun.off.wait"))
		case !ti.ServerTun:
			fmt.Fprintln(outW, i18n.T("net.tun.off.nosrv"))
		default:
			fmt.Fprintln(outW, i18n.T("net.tun.off.hint"))
		}
		if ti.IP != "" {
			fmt.Fprintln(outW, i18n.T("net.tun.modea", ti.IP))
		}
	}
	if len(ti.Peers) > 0 {
		fmt.Fprintln(outW)
		tw := newTable(outW)
		fmt.Fprintln(tw, i18n.T("net.tbl.peer2")+"\tIP\t"+i18n.T("net.tbl.status")+"\t"+i18n.T("net.tbl.path")+"\tRTT")
		for _, p := range ti.Peers {
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
}

func newNetUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: i18n.T("net.up.short"),
		Long:  i18n.T("net.up.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ti, err := tunCall("net_up")
			if err != nil {
				return err
			}
			printTun(ti)
			return nil
		},
	}
}

func newNetDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: i18n.T("net.down.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ti, err := tunCall("net_down")
			if err != nil {
				return err
			}
			printTun(ti)
			return nil
		},
	}
}

// printLocalTun, `zorven net status` icin yerel Mod B durumunu yazar. Calisan ajan
// yoksa false doner (sessiz).
func printLocalTun() bool {
	ep, _, err := findAgent()
	if ep == nil || err != nil {
		return false
	}
	resp, err := callAgent(*ep, localctl.Request{Op: "net_status"})
	if err != nil || resp.Tun == nil {
		return false
	}
	printTun(resp.Tun)
	fmt.Fprintln(os.Stdout)
	return true
}
