package mcptools

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Hassas hedef siniflandirmasi. SAF fonksiyon: DNS cozumlemesi YAPMAZ, yalnizca
// hedefin yazili ana makinesine (literal IP veya ad) ve portuna bakar.
//
// Hassas sayilanlar:
//   - yonetim/veritabani portlari (herhangi bir ana makinede, loopback dahil):
//     22, 3389, 5900-5902, 2375, 2376, 6443, 10250, 5432, 3306, 6379, 27017, 9200
//   - loopback OLMAYAN ozel/link-local/ULA adresler (10/8, 172.16/12, 192.168/16,
//     169.254/16, fe80::/10, fc00::/7) ve host.docker.internal / *.internal adlari.
//
// Hassas SAYILMAYANLAR: yonetim portu olmayan loopback (localhost:3000, 127.0.0.1:8080,
// [::1]:5173, 0.0.0.0:8000) - urunun cekirdek kullanimi; her tunel onay istemesin.

// adminPorts, internete acilirsa tehlikeli olan iyi bilinen yonetim/veri portlari.
var adminPorts = map[int]string{
	22:    "SSH",
	3389:  "RDP",
	5900:  "VNC",
	5901:  "VNC",
	5902:  "VNC",
	2375:  "Docker API",
	2376:  "Docker API (TLS)",
	6443:  "Kubernetes API",
	10250: "kubelet API",
	5432:  "PostgreSQL",
	3306:  "MySQL/MariaDB",
	6379:  "Redis",
	27017: "MongoDB",
	9200:  "Elasticsearch",
}

// SensitiveTarget, bir hedefin hassas olup olmadigini ve nedenlerini (Ingilizce,
// kullaniciya gosterilecek cumleler) dondurur. Bos/ayristirilamayan hedef hassas degildir
// (API zaten reddeder).
func SensitiveTarget(target string) (bool, []string) {
	host, port := splitTarget(target)
	if host == "" {
		return false, nil
	}
	var reasons []string
	if name, ok := adminPorts[port]; ok {
		reasons = append(reasons, fmt.Sprintf("port %d (%s) would be exposed to the internet", port, name))
	}
	switch hostClass(host) {
	case "private":
		reasons = append(reasons, fmt.Sprintf("%s is a private/link-local network address, not the device itself; the tunnel would expose another machine or an internal/metadata service", host))
	case "internal":
		reasons = append(reasons, fmt.Sprintf("%s is an internal container/cluster host name, not the device's own app", host))
	}
	return len(reasons) > 0, reasons
}

// splitTarget, "http://h:p/yol", "h:p", "[::1]:22", "h" bicimlerinden (host, port) cikarir.
// Port yoksa semadan (http 80, https 443) tureyilir; yoksa 0.
func splitTarget(target string) (string, int) {
	s := strings.TrimSpace(target)
	if s == "" {
		return "", 0
	}
	defPort := 0
	if i := strings.Index(s, "://"); i >= 0 {
		switch strings.ToLower(s[:i]) {
		case "http", "ws":
			defPort = 80
		case "https", "wss":
			defPort = 443
		}
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 { // kullanici bilgisi
		s = s[i+1:]
	}
	host, ps, err := net.SplitHostPort(s)
	if err != nil {
		host = strings.Trim(s, "[]")
		return strings.ToLower(host), defPort
	}
	p, err := strconv.Atoi(ps)
	if err != nil || p < 0 || p > 65535 {
		p = defPort
	}
	return strings.ToLower(host), p
}

// hostClass: "loopback", "private", "internal" veya "" (diger).
func hostClass(host string) string {
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if i := strings.Index(h, "%"); i >= 0 { // IPv6 zone
		h = h[:i]
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return "loopback"
	}
	if h == "host.docker.internal" || h == "internal" || strings.HasSuffix(h, ".internal") {
		return "internal"
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback() || a.IsUnspecified():
		return "loopback"
	case a.IsPrivate() || a.IsLinkLocalUnicast():
		return "private"
	}
	return ""
}

// sensitiveErr, onaysiz hassas create/update cagrisina doner (hicbir istek gonderilmez).
func sensitiveErr(tool, target string, reasons []string) error {
	return fmt.Errorf("confirm_required: %s refused (nothing was sent): target %q is sensitive: %s. "+
		"Explain this risk to the user and only if they explicitly agree call %s again with confirm=true",
		tool, target, strings.Join(reasons, "; "), tool)
}
