package agent

// FAZ 3 / F14 — Cihaz kimligi toplama.
//
// Bu bilgiler sunucuda kalicilastirilir; amac cihaz OFFLINE iken de
// "bu neydi, hangi makineydi" sorusunun cevaplanabilmesi.

import (
	"net"
	"os"
	"sort"
)

// deviceHostname, isletim sisteminin makine adi. Okunamazsa bos doner —
// bos ad gondermek, uydurma bir ad gondermekten iyidir.
func deviceHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// localIPs, cihazin yerel adreslerini doner.
//
// Loopback ve link-local adresler ATILIR: her makinede aynidirlar ve cihazi
// ayirt etmeye yaramazlar. Sonuc siralidir ki panelde her yenilemede farkli
// sirada gorunmesin.
func localIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}
