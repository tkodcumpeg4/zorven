package domain

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/publicsuffix"
)

// Yetkili DNS sorgusu: kullanici DNS kaydini degistirdikten sonra ozyinelemeli
// cozumleyiciler (8.8.8.8 vb.) eski degeri TTL boyunca (cogu saglayicida 1 saat)
// onbellekten verir; "DNS'i kontrol et" bu yuzden yeni kaydi gormuyordu. Zone'un
// yetkili ad sunucularina dogrudan sormak degisikligi aninda gosterir.

// authAnswer, yetkili sunucudan gelen A/AAAA adresleri ve (varsa) CNAME hedefi.
type authAnswer struct {
	ips   []net.IP
	cname string
	txts  []string
}

var errNoAuthAnswer = errors.New("yetkili sunucudan cevap alinamadi")

// lookupAuthoritativeIPs, fqdn'in IP adreslerini zone'un yetkili sunucularindan
// ogrenir. CNAME varsa hedef normal cozumleyiciyle cozulur (hedef genelde baska
// bir zone'dadir, or. cname.zorven.app). Basarisizsa hata doner; cagiran normal
// cozumleyiciye duser.
func lookupAuthoritativeIPs(ctx context.Context, fqdn string) ([]net.IPAddr, error) {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	zone, err := publicsuffix.EffectiveTLDPlusOne(fqdn)
	if err != nil {
		return nil, err
	}
	nss, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil || len(nss) == 0 {
		return nil, errNoAuthAnswer
	}
	rand.Shuffle(len(nss), func(i, j int) { nss[i], nss[j] = nss[j], nss[i] })
	for i, ns := range nss {
		if i >= 3 {
			break
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, strings.TrimSuffix(ns.Host, "."))
		if err != nil || len(addrs) == 0 {
			continue
		}
		for _, a := range addrs {
			ans, err := queryAuthoritative(ctx, a.IP, fqdn)
			if err != nil {
				continue
			}
			if ans.cname != "" && len(ans.ips) == 0 {
				return net.DefaultResolver.LookupIPAddr(ctx, ans.cname)
			}
			out := make([]net.IPAddr, 0, len(ans.ips))
			for _, ip := range ans.ips {
				out = append(out, net.IPAddr{IP: ip})
			}
			return out, nil // bos liste = yetkili sunucu "kayit yok" dedi
		}
	}
	return nil, errNoAuthAnswer
}

// queryAuthoritative, tek bir yetkili sunucuya UDP ile A sorgusu atar.
func queryAuthoritative(ctx context.Context, server net.IP, fqdn string) (authAnswer, error) {
	return queryAuthoritativeType(ctx, server, fqdn, dnsmessage.TypeA)
}

// queryAuthoritativeType, tek bir yetkili sunucuya verilen turde UDP sorgusu atar.
func queryAuthoritativeType(ctx context.Context, server net.IP, fqdn string, qtype dnsmessage.Type) (authAnswer, error) {
	name, err := dnsmessage.NewName(fqdn + ".")
	if err != nil {
		return authAnswer{}, err
	}
	id := uint16(rand.IntN(1 << 16))
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id},
		Questions: []dnsmessage.Question{{Name: name, Type: qtype, Class: dnsmessage.ClassINET}},
	}
	buf, err := msg.Pack()
	if err != nil {
		return authAnswer{}, err
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(server.String(), "53"))
	if err != nil {
		return authAnswer{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(buf); err != nil {
		return authAnswer{}, err
	}
	resp := make([]byte, 4096)
	n, err := conn.Read(resp)
	if err != nil {
		return authAnswer{}, err
	}
	return parseAuthoritative(resp[:n], id)
}

// parseAuthoritative, cevaptan A kayitlarini ve CNAME hedefini cikarir.
// Yetkili olmayan (AA bitsiz) ya da hatali cevaplar reddedilir.
func parseAuthoritative(b []byte, id uint16) (authAnswer, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		return authAnswer{}, err
	}
	if h.ID != id || !h.Response || !h.Authoritative {
		return authAnswer{}, errNoAuthAnswer
	}
	if h.RCode != dnsmessage.RCodeSuccess && h.RCode != dnsmessage.RCodeNameError {
		return authAnswer{}, errNoAuthAnswer
	}
	if err := p.SkipAllQuestions(); err != nil {
		return authAnswer{}, err
	}
	var ans authAnswer
	for {
		rh, err := p.AnswerHeader()
		if err == dnsmessage.ErrSectionDone {
			break
		}
		if err != nil {
			return authAnswer{}, err
		}
		switch rh.Type {
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err != nil {
				return authAnswer{}, err
			}
			ans.ips = append(ans.ips, net.IP(r.A[:]))
		case dnsmessage.TypeCNAME:
			r, err := p.CNAMEResource()
			if err != nil {
				return authAnswer{}, err
			}
			ans.cname = strings.TrimSuffix(r.CNAME.String(), ".")
		case dnsmessage.TypeTXT:
			r, err := p.TXTResource()
			if err != nil {
				return authAnswer{}, err
			}
			// Uzun TXT degerleri 255 baytlik parcalara bolunur; birlestir.
			ans.txts = append(ans.txts, strings.Join(r.TXT, ""))
		default:
			if err := p.SkipAnswer(); err != nil {
				return authAnswer{}, err
			}
		}
	}
	return ans, nil
}

// authoritativeQuery, fqdn'in zone'unun yetkili sunucularina (en fazla 3 ad
// sunucusu) qtype sorgusu atar ve ilk yetkili cevabi doner. Hic yetkili cevap
// alinamazsa errNoAuthAnswer: cagiran normal cozumleyiciye duser.
func authoritativeQuery(ctx context.Context, fqdn string, qtype dnsmessage.Type) (authAnswer, error) {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	zone, err := publicsuffix.EffectiveTLDPlusOne(fqdn)
	if err != nil {
		return authAnswer{}, err
	}
	nss, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil || len(nss) == 0 {
		return authAnswer{}, errNoAuthAnswer
	}
	rand.Shuffle(len(nss), func(i, j int) { nss[i], nss[j] = nss[j], nss[i] })
	for i, ns := range nss {
		if i >= 3 {
			break
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, strings.TrimSuffix(ns.Host, "."))
		if err != nil || len(addrs) == 0 {
			continue
		}
		for _, a := range addrs {
			if ans, err := queryAuthoritativeType(ctx, a.IP, fqdn, qtype); err == nil {
				return ans, nil
			}
		}
	}
	return authAnswer{}, errNoAuthAnswer
}

// lookupAuthoritativeTXT, TXT kayitlarini yetkili sunuculardan okur (onbellek
// atlanir: kullanici kaydi yeni degistirdiyse eski deger donmez). Bos liste =
// yetkili sunucu "kayit yok" dedi.
func lookupAuthoritativeTXT(ctx context.Context, name string) ([]string, error) {
	ans, err := authoritativeQuery(ctx, name, dnsmessage.TypeTXT)
	if err != nil {
		return nil, err
	}
	return ans.txts, nil
}

// lookupAuthoritativeCNAME, CNAME hedefini yetkili sunuculardan okur ("" = yok).
func lookupAuthoritativeCNAME(ctx context.Context, name string) (string, error) {
	ans, err := authoritativeQuery(ctx, name, dnsmessage.TypeCNAME)
	if err != nil {
		return "", err
	}
	return ans.cname, nil
}
