package mail

import (
	"fmt"
	"net"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/mail/mailauth"
)

// InboundAuthMode, gelen :25 alicisinin kimlik dogrulama (SPF/DKIM/DMARC) kipidir.
type InboundAuthMode string

const (
	// AuthEnforce (varsayilan): sonuc basliga yazilir ve politika uygulanir
	// (DMARC reject -> 550 5.7.1, quarantine / SPF fail -> Onemsiz).
	AuthEnforce InboundAuthMode = "enforce"
	// AuthMonitor: sonuc basliga yazilir ve loglanir ama hicbir ileti
	// reddedilmez ya da Onemsiz'e yonlendirilmez (kademeli devreye alma).
	AuthMonitor InboundAuthMode = "monitor"
	// AuthOff: dogrulama yok (eski davranis).
	AuthOff InboundAuthMode = "off"
)

// ParseInboundAuthMode, ZORVEN_MAIL_AUTH degerini kipe cevirir. Bos deger
// varsayilandir (enforce). Taninmayan degerde (enforce, false) doner; cagiran uyarabilir.
func ParseInboundAuthMode(s string) (InboundAuthMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "enforce", "on", "1", "true":
		return AuthEnforce, true
	case "monitor", "report":
		return AuthMonitor, true
	case "off", "0", "false", "disabled":
		return AuthOff, true
	}
	return AuthEnforce, false
}

// InboundAuth, gelen iletilerin kimlik dogrulamasi ayarlaridir.
type InboundAuth struct {
	Verifier *mailauth.Verifier
	// AuthservID, Authentication-Results basligindaki authserv-id'dir
	// (sunucunun posta ana makine adi, ornegin mail.zorven.app).
	AuthservID string
	// StripIDs, AuthservID'ye ek olarak sahte sayilip silinecek authserv-id'ler
	// (ornegin platform alan adi).
	StripIDs []string
	Mode     InboundAuthMode
}

// NewInboundAuth, yerel teslim ayarlarindan dogrulama yapilandirmasi kurar.
// resolver nil ise sistem DNS cozumleyicisi kullanilir. AuthOff icin nil doner.
func NewInboundAuth(l *Local, mode InboundAuthMode, resolver mailauth.Resolver) *InboundAuth {
	if mode == AuthOff {
		return nil
	}
	a := &InboundAuth{
		Verifier:   mailauth.NewVerifier(resolver),
		AuthservID: l.MailDomain,
		Mode:       mode,
	}
	if l.PlatformDomain != "" && l.PlatformDomain != l.MailDomain {
		a.StripIDs = []string{l.PlatformDomain}
	}
	return a
}

// authExemptLocalParts, hicbir kosulda reddedilmeyen / Onemsize ayrilmayan
// yerel kisimlardir: RFC 5321 madde 4.5.1 postmaster'in, RFC 2142 abuse'in her
// kaynaktan posta almasini ister (kotu yapilandirilmis bir alan adindan gelen
// kotuye kullanim bildirimi kaybolmamali). Sonuc yine basliga yazilir.
var authExemptLocalParts = map[string]bool{"postmaster": true, "abuse": true}

// action, sonuc icin UYGULANACAK karari verir (kip ve muafiyetler sonrasi).
func (a *InboundAuth) action(res *mailauth.Result, rcpts []string) mailauth.Action {
	if res.Action == mailauth.ActionInbox || a.Mode != AuthEnforce {
		return mailauth.ActionInbox
	}
	for _, rc := range rcpts {
		if at := strings.LastIndex(rc, "@"); at > 0 && authExemptLocalParts[strings.ToLower(rc[:at])] {
			return mailauth.ActionInbox
		}
	}
	return res.Action
}

// rejectText, 550 5.7.1 yaniti metnidir. Alan adi mailauth tarafinda dogrulanmis
// bir ASCII ana makine adidir; satir sonu ve bosluk icermez.
func rejectText(res *mailauth.Result) string {
	d := res.DMARC.FromDomain
	if d == "" {
		d = "the sender domain"
	}
	return fmt.Sprintf("Unauthenticated email from %s is not accepted due to the domain's DMARC policy (p=reject): SPF and DKIM alignment failed", d)
}

// authSummary, tek satirlik log ozetidir (yalnizca sabit sozlukten degerler ve IP).
func authSummary(res *mailauth.Result, ip net.IP, applied mailauth.Action) string {
	dk := make([]string, 0, len(res.DKIM))
	for _, d := range res.DKIM {
		dk = append(dk, d.Result)
	}
	ipStr := "-"
	if ip != nil {
		ipStr = ip.String()
	}
	s := fmt.Sprintf("ip=%s spf=%s dkim=%s dmarc=%s karar=%s", ipStr, res.SPF.Result, strings.Join(dk, ","), res.DMARC.Result, applied)
	if res.Action != applied {
		s += fmt.Sprintf(" (politika=%s)", res.Action)
	}
	if res.Why != "" {
		s += " neden=" + res.Why
	}
	return s
}

// connIP, SMTP baglantisinin uzak IP adresini doner (cozulemezse nil).
func connIP(nc net.Conn) net.IP {
	if nc == nil {
		return nil
	}
	switch a := nc.RemoteAddr().(type) {
	case *net.TCPAddr:
		return a.IP
	case nil:
		return nil
	default:
		host, _, err := net.SplitHostPort(a.String())
		if err != nil {
			return net.ParseIP(a.String())
		}
		return net.ParseIP(host)
	}
}
