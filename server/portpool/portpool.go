// Package portpool, ham TCP/UDP rezerve-port tunelleri icin (IP, port) adres
// havuzunu tanimlar (K3). Havuz bir veya daha fazla "yuva"dan (slot) olusur;
// her yuva bir genel IP + onun genel host adidir ve kendi [Min, Max] port
// araligina sahiptir. Kapasite = yuva sayisi x aralik genisligi.
//
// Yapilandirma (ortam degiskenleri, main.go okur):
//
//	ZORVEN_TCP_BIND_IPS   ip1,ip2,...   bos = eski davranis (tek yuva, tum arayuzler)
//	ZORVEN_TCP_HOSTS      host1=ip1,host2=ip2,...
//	ZORVEN_TCP_PORT_MIN/MAX             yuva basina aralik (varsayilan 10000-10099)
//	ZORVEN_TCP_PORT_QUARANTINE          serbest portun yeniden verilme beklemesi (varsayilan 10m)
//
// BIND_IPS girdisi iki bicimde olabilir:
//
//	"1.2.3.4"        sunucu dogrudan o IP'ye baglanir (host network veya IP'si
//	                 konteynerde gorunen kurulum)
//	"1.2.3.4+10000"  docker bridge icin: sunucu TUM arayuzlerde (port+10000)
//	                 dinler; compose "1.2.3.4:10000-10099:20000-20099" yayinlar.
//	                 "+0" da gecerlidir (yalniz joker baglanti, kaydirma yok).
//
// Ilk girdi BIRINCIL yuvadir: mevcut (eski) tunel kayitlari (public_ip = "")
// birincil yuvaya aittir. Birincil yuvanin yerini/IP'sini degistirmeyin.
package portpool

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Varsayilanlar.
const (
	DefaultMin        = 10000
	DefaultMax        = 10099
	DefaultQuarantine = 10 * time.Minute
)

// Slot, havuzdaki tek bir genel IP.
type Slot struct {
	// Key, veritabaninda tunnels.public_ip olarak saklanan kimlik. Birincil yuva
	// icin "" (eski kayitlarla uyum), digerleri icin IP metni.
	Key string
	// IP, genel IP ("" = eski tek-yuva kurulumu, IP bilinmiyor).
	IP string
	// Host, tunel adresinde gosterilen genel host adi.
	Host string
	// Wildcard true ise sunucu IP'ye degil tum arayuzlere baglanir (bridge).
	Wildcard bool
	// Offset, Wildcard yuvada dinleme portu = genel port + Offset.
	Offset int
}

// Addr, havuzdaki bir tahsis (yuva anahtari + genel port).
type Addr struct {
	Key  string
	Port int
}

// Config, dogrulanmis havuz yapilandirmasi.
type Config struct {
	Slots      []Slot
	Min, Max   int
	Quarantine time.Duration
}

// Legacy, ortam degiskeni verilmediginde kullanilan tek-yuvali eski davranis.
func Legacy(platformDomain string) *Config {
	return &Config{
		Slots:      []Slot{{Key: "", Host: platformDomain, Wildcard: true}},
		Min:        DefaultMin,
		Max:        DefaultMax,
		Quarantine: DefaultQuarantine,
	}
}

// Parse, ortam degiskeni degerlerinden yapilandirma uretir. min/max 0 ise
// varsayilan; quarantine <0 ise varsayilan, 0 ise karantina kapali.
func Parse(bindIPs, hosts string, min, max int, quarantine time.Duration, platformDomain string) (*Config, error) {
	if min == 0 {
		min = DefaultMin
	}
	if max == 0 {
		max = DefaultMax
	}
	if min < 1024 || max > 65535 || min > max {
		return nil, fmt.Errorf("port araligi gecersiz: %d-%d (1024-65535 ve min<=max olmali)", min, max)
	}
	if quarantine < 0 {
		quarantine = DefaultQuarantine
	}

	// HOSTS: host=ip listesi.
	hostOfIP := map[string]string{}
	var hostIPOrder []string
	for _, part := range splitList(hosts) {
		h, ipStr, ok := strings.Cut(part, "=")
		h, ipStr = strings.ToLower(strings.TrimSpace(h)), strings.TrimSpace(ipStr)
		if !ok || h == "" || ipStr == "" {
			return nil, fmt.Errorf("ZORVEN_TCP_HOSTS girdisi 'host=ip' olmali: %q", part)
		}
		a, err := netip.ParseAddr(ipStr)
		if err != nil {
			return nil, fmt.Errorf("ZORVEN_TCP_HOSTS: gecersiz IP %q", ipStr)
		}
		ipStr = a.String()
		if _, dup := hostOfIP[ipStr]; dup {
			return nil, fmt.Errorf("ZORVEN_TCP_HOSTS: %s birden fazla host'a verilmis", ipStr)
		}
		hostOfIP[ipStr] = h
		hostIPOrder = append(hostIPOrder, ipStr)
	}

	// BIND_IPS: ip[+offset] listesi. Bos ise HOSTS'taki IP'ler (joker degil).
	type entry struct {
		ip       string
		wildcard bool
		offset   int
	}
	var entries []entry
	for _, part := range splitList(bindIPs) {
		ipPart, offPart, hasOff := strings.Cut(part, "+")
		a, err := netip.ParseAddr(strings.TrimSpace(ipPart))
		if err != nil {
			return nil, fmt.Errorf("ZORVEN_TCP_BIND_IPS: gecersiz IP %q", ipPart)
		}
		e := entry{ip: a.String()}
		if hasOff {
			off, err := strconv.Atoi(strings.TrimSpace(offPart))
			if err != nil || off < 0 || max+off > 65535 {
				return nil, fmt.Errorf("ZORVEN_TCP_BIND_IPS: gecersiz kaydirma %q (0 ve %d arasinda olmali)", offPart, 65535-max)
			}
			e.wildcard, e.offset = true, off
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		for _, ip := range hostIPOrder {
			entries = append(entries, entry{ip: ip})
		}
	}
	if len(entries) == 0 {
		c := Legacy(platformDomain)
		c.Min, c.Max, c.Quarantine = min, max, quarantine
		return c, nil
	}

	cfg := &Config{Min: min, Max: max, Quarantine: quarantine}
	seen := map[string]bool{}
	for i, e := range entries {
		if seen[e.ip] {
			return nil, fmt.Errorf("ZORVEN_TCP_BIND_IPS: %s tekrar ediyor", e.ip)
		}
		seen[e.ip] = true
		host := hostOfIP[e.ip]
		if host == "" {
			if i == 0 && platformDomain != "" {
				host = platformDomain // birincil yuva, host verilmediyse apex
			} else {
				host = e.ip // son care: IP'nin kendisi gecerli bir adrestir
			}
		}
		s := Slot{IP: e.ip, Host: host, Wildcard: e.wildcard, Offset: e.offset}
		if i > 0 {
			s.Key = e.ip
		}
		cfg.Slots = append(cfg.Slots, s)
	}
	for ip := range hostOfIP {
		if !seen[ip] {
			return nil, fmt.Errorf("ZORVEN_TCP_HOSTS: %s ZORVEN_TCP_BIND_IPS icinde yok", ip)
		}
	}
	if err := cfg.checkOverlap(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// checkOverlap, ayni dinleme adresinde cakisabilecek yuvalari reddeder: joker
// yuvalar birbirleriyle ve baska her yuvayla port araligi (kaydirma dahil)
// ortusmemeli; farkli ozel IP'ler ortusebilir.
func (c *Config) checkOverlap() error {
	for i := range c.Slots {
		for j := i + 1; j < len(c.Slots); j++ {
			a, b := c.Slots[i], c.Slots[j]
			if !a.Wildcard && !b.Wildcard {
				continue
			}
			aMin, aMax := c.Min+a.Offset, c.Max+a.Offset
			bMin, bMax := c.Min+b.Offset, c.Max+b.Offset
			if aMin <= bMax && bMin <= aMax {
				return fmt.Errorf("%s ve %s dinleme port araliklari cakisiyor; joker (+kaydirma) yuvalara farkli kaydirma verin", a.IP, b.IP)
			}
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Slot, anahtara gore yuvayi bulur. Bilinmeyen anahtar (IP env'den cikarilmis)
// icin ok=false doner.
func (c *Config) Slot(key string) (Slot, bool) {
	for _, s := range c.Slots {
		if s.Key == key {
			return s, true
		}
	}
	return Slot{}, false
}

// Host, anahtarin genel host adi; bilinmiyorsa birincil yuvaninki.
func (c *Config) Host(key string) string {
	if s, ok := c.Slot(key); ok {
		return s.Host
	}
	return c.Slots[0].Host
}

// Listen, genel (anahtar, port) icin sunucunun dinleyecegi adres/portu doner.
// ok=false: yuva artik yapilandirmada yok (dinleme acilmaz).
func (c *Config) Listen(key string, port int) (bindHost string, listenPort int, ok bool) {
	s, found := c.Slot(key)
	if !found {
		return "", 0, false
	}
	if s.Wildcard || s.IP == "" {
		return "", port + s.Offset, true
	}
	return s.IP, port, true
}

// Capacity, toplam tahsis edilebilir (IP, port) sayisi.
func (c *Config) Capacity() int { return len(c.Slots) * (c.Max - c.Min + 1) }

// Pick, kullanilmayan ve karantinada olmayan ilk (yuva, port)'u secer: once
// birincil yuva dolar, sonra siradaki. skip, ek olarak atlanacak adresler
// (ornegin unique ihlali alan). ok=false: havuz dolu.
func (c *Config) Pick(used, quarantined, skip map[Addr]bool) (Addr, bool) {
	for _, s := range c.Slots {
		for p := c.Min; p <= c.Max; p++ {
			a := Addr{Key: s.Key, Port: p}
			if used[a] || quarantined[a] || skip[a] {
				continue
			}
			return a, true
		}
	}
	return Addr{}, false
}

// Format, "host:port" biciminde genel adres.
func (c *Config) Format(key string, port int) string {
	return net.JoinHostPort(c.Host(key), strconv.Itoa(port))
}

// Stats, havuz doluluk ozeti (operator uyarisi / admin API).
type Stats struct {
	Capacity    int         `json:"capacity"`
	Used        int         `json:"used"`
	Quarantined int         `json:"quarantined"`
	Free        int         `json:"free"`
	Slots       []SlotStats `json:"slots"`
}

// SlotStats, tek yuvanin doluluk ozeti.
type SlotStats struct {
	Host        string `json:"host"`
	IP          string `json:"ip,omitempty"`
	Capacity    int    `json:"capacity"`
	Used        int    `json:"used"`
	Quarantined int    `json:"quarantined"`
}

// Stats, used ve quarantined kumelerinden doluluk hesaplar.
func (c *Config) Stats(used, quarantined map[Addr]bool) Stats {
	width := c.Max - c.Min + 1
	st := Stats{}
	for _, s := range c.Slots {
		ss := SlotStats{Host: s.Host, IP: s.IP, Capacity: width}
		for p := c.Min; p <= c.Max; p++ {
			a := Addr{Key: s.Key, Port: p}
			if used[a] {
				ss.Used++
			} else if quarantined[a] {
				ss.Quarantined++
			}
		}
		st.Capacity += ss.Capacity
		st.Used += ss.Used
		st.Quarantined += ss.Quarantined
		st.Slots = append(st.Slots, ss)
	}
	st.Free = st.Capacity - st.Used - st.Quarantined
	return st
}
