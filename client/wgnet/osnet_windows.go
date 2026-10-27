//go:build windows

package wgnet

import (
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Windows Mod B: Wintun (wintun.dll; WireGuard LLC imzali surucu), winipcfg ile
// adres/rota, split-DNS icin NRPT (Add-DnsClientNrptRule). Adaptor olusturma
// YONETICI/SYSTEM ister: Zorven Windows Servisi (SYSTEM) olarak calisiyorsa
// istem cikmaz.
//
// wintun.dll zorven.exe ile BIRLIKTE gelmez/gomulmez: kurulumla zorven.exe'nin
// yanina konur (bkz. docs/zorven-net-share.md; dll'in "Prebuilt Binaries License"
// kosullari: yeniden dagitim izinli, degistirilemez). dll yoksa Mod B baslamaz
// ve acik bir hata verilir; Mod A (port paylasimi) etkilenmez.
//
// NOT: bu dosya Windows makinesinde calistirilarak DOGRULANMADI (capraz derleme
// ile derlenir; test edilmedi).

type windowsOS struct{}

// DefaultOS, derleme hedefinin OSNet uygulamasi.
func DefaultOS() OSNet { return windowsOS{} }

func (windowsOS) Name() string { return "windows" }

// Sabit adaptor GUID'i: Windows ag profilini her baslatmada yeniden olusturmaz.
var zorvenAdapterGUID = &windows.GUID{Data1: 0x7a0f3c52, Data2: 0x5b1e, Data3: 0x4d7e, Data4: [8]byte{0x9a, 0x42, 0x5e, 0x3b, 0x1c, 0x8d, 0x6f, 0x21}}

const nrptComment = "zorven"

func (windowsOS) CreateTUN(name string, mtu int) (tun.Device, error) {
	d, err := tun.CreateTUNWithRequestedGUID(name, zorvenAdapterGUID, mtu)
	if err != nil {
		low := strings.ToLower(err.Error())
		switch {
		case strings.Contains(low, "wintun") || strings.Contains(low, "dll") || strings.Contains(low, "module could not be found"):
			return nil, fmt.Errorf("wintun.dll bulunamadi veya yuklenemedi (%v). Normalde 'zorven service install' / 'zorven net up' "+
				"bunu sunucudan otomatik indirir (imzali manifest dogrulamasiyla); elle kurmak icin wintun.dll'i (wintun.net, imzali, "+
				"bu makinenin mimarisine uygun amd64/arm64 surumu) zorven.exe ile ayni klasore koyun", err)
		case strings.Contains(low, "access is denied") || strings.Contains(low, "erisim engellendi"):
			return nil, fmt.Errorf("Wintun adaptoru olusturulamadi: yonetici yetkisi gerekli. Zorven servisini kurun (yonetici PowerShell: zorven service install) ve 'zorven net up' komutunu yonetici olarak calistirin")
		}
		return nil, fmt.Errorf("Wintun adaptoru olusturulamadi: %w", err)
	}
	return d, nil
}

func (windowsOS) Configure(dev tun.Device, ip netip.Addr, pool netip.Prefix, mtu int) (func(), error) {
	nt, ok := dev.(*tun.NativeTun)
	if !ok {
		// Filter sarmalayicisi ise ic aygiti bul.
		if f, isF := dev.(*Filter); isF {
			nt, ok = f.inner.(*tun.NativeTun)
		}
	}
	if !ok {
		return nil, fmt.Errorf("beklenmeyen TUN aygiti turu")
	}
	luid := winipcfg.LUID(nt.LUID())
	if err := luid.SetIPAddressesForFamily(windows.AF_INET, []netip.Prefix{netip.PrefixFrom(ip, 32)}); err != nil {
		return nil, fmt.Errorf("adres atanamadi: %w", err)
	}
	// On-link havuz rotasi (sonraki adim 0.0.0.0).
	routes := []*winipcfg.RouteData{{Destination: pool.Masked(), NextHop: netip.IPv4Unspecified(), Metric: 0}}
	if err := luid.SetRoutesForFamily(windows.AF_INET, routes); err != nil {
		return nil, fmt.Errorf("rota eklenemedi (%s): %w", pool, err)
	}
	if ipif, err := luid.IPInterface(windows.AF_INET); err == nil {
		ipif.NLMTU = uint32(mtu)
		ipif.UseAutomaticMetric = false
		ipif.Metric = 0
		ipif.DadTransmits = 0
		ipif.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
		_ = ipif.Set()
	}
	return func() { _ = luid.FlushRoutes(windows.AF_INET); _ = luid.FlushIPAddresses(windows.AF_INET) }, nil
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (windowsOS) SetupDNS(dev tun.Device, resolver netip.Addr, zone string) DNSSetup {
	removeStale()
	script := fmt.Sprintf("Add-DnsClientNrptRule -Namespace %s -NameServers %s -Comment %s; Clear-DnsClientCache",
		psQuote("."+zone), psQuote(resolver.String()), psQuote(nrptComment))
	if out, err := runCmdCtx(15e9, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script); err != nil {
		return DNSSetup{Note: "NRPT kurali eklenemedi: " + firstLine(out, err)}
	}
	return DNSSetup{OK: true, Method: "nrpt", Undo: removeStale}
}

// removeStale, "zorven" yorumlu NRPT kurallarini siler (down ve yetim temizleme).
func removeStale() {
	script := fmt.Sprintf("Get-DnsClientNrptRule | Where-Object { $_.Comment -eq %s } | Remove-DnsClientNrptRule -Force; Clear-DnsClientCache",
		psQuote(nrptComment))
	_, _ = runCmdCtx(15e9, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
}

func (windowsOS) CleanupStale() { removeStale() }
