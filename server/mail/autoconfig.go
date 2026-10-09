package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// ClientConfig, mail istemcisi otomatik yapilandirmasi (Thunderbird autoconfig,
// Outlook Autodiscover, Apple .mobileconfig) icin sunucu bilgileridir.
type ClientConfig struct {
	MailDomain     string // mail.zorven.app (kiraci adresleri)
	PlatformDomain string // zorven.app (sistem adresleri)
	Host           string // IMAP/SMTP sunucu adi, or. mail.zorven.app
	IMAPPort       int    // 993 (0 = kapali)
	SubmissionPort int    // 587 STARTTLS (0 = kapali)
	SubmissionsTLS int    // 465 implicit TLS (0 = kapali)
	DisplayName    string // "Zorven Mail"
}

// Domains, bu yapilandirmanin kapsadigi e-posta alan adlari.
func (c ClientConfig) Domains() []string {
	var out []string
	for _, d := range []string{c.MailDomain, c.PlatformDomain} {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// HandlesDomain, e-posta alan adinin bu yapilandirmaya ait olup olmadigini soyler.
func (c ClientConfig) HandlesDomain(d string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	for _, x := range c.Domains() {
		if x == d {
			return true
		}
	}
	return false
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (c ClientConfig) name() string { return firstNonEmptyStr(c.DisplayName, "Zorven Mail") }

// AutoconfigXML, Thunderbird config-v1.1.xml icerigini uretir.
func (c ClientConfig) AutoconfigXML() []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<clientConfig version="1.1">` + "\n")
	b.WriteString(`  <emailProvider id="` + xmlEsc(c.Host) + `">` + "\n")
	for _, d := range c.Domains() {
		b.WriteString("    <domain>" + xmlEsc(d) + "</domain>\n")
	}
	b.WriteString("    <displayName>" + xmlEsc(c.name()) + "</displayName>\n")
	b.WriteString("    <displayShortName>" + xmlEsc(c.name()) + "</displayShortName>\n")
	if c.IMAPPort > 0 {
		b.WriteString(`    <incomingServer type="imap">` + "\n")
		b.WriteString("      <hostname>" + xmlEsc(c.Host) + "</hostname>\n")
		b.WriteString("      <port>" + strconv.Itoa(c.IMAPPort) + "</port>\n")
		b.WriteString("      <socketType>SSL</socketType>\n")
		b.WriteString("      <authentication>password-cleartext</authentication>\n")
		b.WriteString("      <username>%EMAILADDRESS%</username>\n")
		b.WriteString("    </incomingServer>\n")
	}
	if c.SubmissionPort > 0 {
		b.WriteString(`    <outgoingServer type="smtp">` + "\n")
		b.WriteString("      <hostname>" + xmlEsc(c.Host) + "</hostname>\n")
		b.WriteString("      <port>" + strconv.Itoa(c.SubmissionPort) + "</port>\n")
		b.WriteString("      <socketType>STARTTLS</socketType>\n")
		b.WriteString("      <authentication>password-cleartext</authentication>\n")
		b.WriteString("      <username>%EMAILADDRESS%</username>\n")
		b.WriteString("    </outgoingServer>\n")
	}
	if c.SubmissionsTLS > 0 {
		b.WriteString(`    <outgoingServer type="smtp">` + "\n")
		b.WriteString("      <hostname>" + xmlEsc(c.Host) + "</hostname>\n")
		b.WriteString("      <port>" + strconv.Itoa(c.SubmissionsTLS) + "</port>\n")
		b.WriteString("      <socketType>SSL</socketType>\n")
		b.WriteString("      <authentication>password-cleartext</authentication>\n")
		b.WriteString("      <username>%EMAILADDRESS%</username>\n")
		b.WriteString("    </outgoingServer>\n")
	}
	b.WriteString("  </emailProvider>\n</clientConfig>\n")
	return []byte(b.String())
}

// AutodiscoverXML, Outlook Autodiscover (outlook/responseschema/2006a) yanitini uretir.
func (c ClientConfig) AutodiscoverXML(email string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006">` + "\n")
	b.WriteString(`  <Response xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a">` + "\n")
	b.WriteString("    <Account>\n      <AccountType>email</AccountType>\n      <Action>settings</Action>\n")
	if c.IMAPPort > 0 {
		b.WriteString("      <Protocol>\n        <Type>IMAP</Type>\n")
		b.WriteString("        <Server>" + xmlEsc(c.Host) + "</Server>\n")
		b.WriteString("        <Port>" + strconv.Itoa(c.IMAPPort) + "</Port>\n")
		b.WriteString("        <DomainRequired>off</DomainRequired>\n")
		b.WriteString("        <LoginName>" + xmlEsc(email) + "</LoginName>\n")
		b.WriteString("        <SPA>off</SPA>\n        <SSL>on</SSL>\n        <AuthRequired>on</AuthRequired>\n      </Protocol>\n")
	}
	smtpPort, enc := c.SubmissionPort, "TLS"
	if smtpPort == 0 {
		smtpPort, enc = c.SubmissionsTLS, "SSL"
	}
	if smtpPort > 0 {
		b.WriteString("      <Protocol>\n        <Type>SMTP</Type>\n")
		b.WriteString("        <Server>" + xmlEsc(c.Host) + "</Server>\n")
		b.WriteString("        <Port>" + strconv.Itoa(smtpPort) + "</Port>\n")
		b.WriteString("        <DomainRequired>off</DomainRequired>\n")
		b.WriteString("        <LoginName>" + xmlEsc(email) + "</LoginName>\n")
		b.WriteString("        <SPA>off</SPA>\n        <SSL>on</SSL>\n")
		b.WriteString("        <Encryption>" + enc + "</Encryption>\n")
		b.WriteString("        <AuthRequired>on</AuthRequired>\n        <UsePOPAuth>off</UsePOPAuth>\n        <SMTPLast>off</SMTPLast>\n      </Protocol>\n")
	}
	b.WriteString("    </Account>\n  </Response>\n</Autodiscover>\n")
	return []byte(b.String())
}

func autodiscoverError(msg string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/responseschema/2006">` + "\n" +
		`  <Response><Error Time="00:00:00.0000000" Id="0"><ErrorCode>600</ErrorCode><Message>` + xmlEsc(msg) +
		`</Message><DebugData /></Error></Response>` + "\n</Autodiscover>\n")
}

func randUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// MobileConfig, Apple Mail icin imzasiz .mobileconfig profili uretir. Parola
// profile YAZILMAZ; kullanici kurulumda uygulama parolasini girer.
func (c ClientConfig) MobileConfig(email, personName string) []byte {
	smtpPort := c.SubmissionPort
	if smtpPort == 0 {
		smtpPort = c.SubmissionsTLS
	}
	imapPort := c.IMAPPort
	if imapPort == 0 {
		imapPort = 993
	}
	if smtpPort == 0 {
		smtpPort = 587
	}
	id := "app.zorven.mail." + strings.NewReplacer("@", ".", "_", "-").Replace(strings.ToLower(email))
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0"><dict>` + "\n")
	b.WriteString("<key>PayloadContent</key><array><dict>\n")
	kv := func(k, v string) { b.WriteString("<key>" + k + "</key><string>" + xmlEsc(v) + "</string>\n") }
	kb := func(k string, v bool) {
		val := "<false/>"
		if v {
			val = "<true/>"
		}
		b.WriteString("<key>" + k + "</key>" + val + "\n")
	}
	ki := func(k string, v int) {
		b.WriteString("<key>" + k + "</key><integer>" + strconv.Itoa(v) + "</integer>\n")
	}
	kv("EmailAccountDescription", c.name()+" ("+email+")")
	kv("EmailAccountName", firstNonEmptyStr(personName, email))
	kv("EmailAccountType", "EmailTypeIMAP")
	kv("EmailAddress", email)
	kv("IncomingMailServerAuthentication", "EmailAuthPassword")
	kv("IncomingMailServerHostName", c.Host)
	ki("IncomingMailServerPortNumber", imapPort)
	kb("IncomingMailServerUseSSL", true)
	kv("IncomingMailServerUsername", email)
	kv("OutgoingMailServerAuthentication", "EmailAuthPassword")
	kv("OutgoingMailServerHostName", c.Host)
	ki("OutgoingMailServerPortNumber", smtpPort)
	kb("OutgoingMailServerUseSSL", true)
	kv("OutgoingMailServerUsername", email)
	kb("OutgoingPasswordSameAsIncomingPassword", true)
	kv("PayloadDescription", "Zorven mail hesabi")
	kv("PayloadDisplayName", c.name()+" ("+email+")")
	kv("PayloadIdentifier", id+".account")
	kv("PayloadType", "com.apple.mail.managed")
	kv("PayloadUUID", randUUID())
	ki("PayloadVersion", 1)
	b.WriteString("</dict></array>\n")
	kv("PayloadDescription", "Zorven mail hesabi")
	kv("PayloadDisplayName", c.name())
	kv("PayloadIdentifier", id)
	kv("PayloadOrganization", c.name())
	kb("PayloadRemovalDisallowed", false)
	kv("PayloadType", "Configuration")
	kv("PayloadUUID", randUUID())
	ki("PayloadVersion", 1)
	b.WriteString("</dict></plist>\n")
	return []byte(b.String())
}

// AutoConfigHandler, mail istemcisi otomatik yapilandirma uclarini sunar.
type AutoConfigHandler struct {
	Cfg   ClientConfig
	hosts map[string]bool
}

// NewAutoConfigHandler, cevap verilecek hostlari yapilandirmadan turetir:
// platform/mail alan adlari ile autoconfig.* ve autodiscover.* altlari (+ sunucu adi).
func NewAutoConfigHandler(cfg ClientConfig) *AutoConfigHandler {
	h := &AutoConfigHandler{Cfg: cfg, hosts: make(map[string]bool)}
	for _, base := range append(cfg.Domains(), cfg.Host) {
		base = strings.ToLower(strings.TrimSpace(base))
		if base == "" {
			continue
		}
		h.hosts[base] = true
		h.hosts["autoconfig."+base] = true
		h.hosts["autodiscover."+base] = true
	}
	if cfg.PlatformDomain != "" {
		h.hosts["www."+strings.ToLower(cfg.PlatformDomain)] = true
		// MTA-STS politikasi (RFC 8461): yalniz platform alani; wildcard
		// sertifika mta-sts.<platform>'u kapsar, mta-sts.mail.<platform>'u kapsamaz.
		h.hosts["mta-sts."+strings.ToLower(cfg.PlatformDomain)] = true
	}
	return h
}

// Hosts, bu handler'in cevap verdigi tum host adlarini doner (platform hosu
// olarak ayrilmalari icin).
func (h *AutoConfigHandler) Hosts() []string {
	out := make([]string, 0, len(h.hosts))
	for k := range h.hosts {
		out = append(out, k)
	}
	return out
}

func hostOnly(hostport string) string {
	h := strings.ToLower(strings.TrimSpace(hostport))
	if hh, _, err := net.SplitHostPort(h); err == nil {
		return hh
	}
	return h
}

func (h *AutoConfigHandler) kind(r *http.Request) string {
	if !h.hosts[hostOnly(r.Host)] {
		return ""
	}
	p := strings.ToLower(r.URL.Path)
	if p == "/.well-known/mta-sts.txt" && strings.HasPrefix(hostOnly(r.Host), "mta-sts.") &&
		h.Cfg.Host != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		return "mtasts"
	}
	switch p {
	case "/mail/config-v1.1.xml", "/.well-known/autoconfig/mail/config-v1.1.xml":
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return "autoconfig"
		}
	case "/autodiscover/autodiscover.xml":
		if r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodHead {
			return "autodiscover"
		}
	}
	return ""
}

// Match, istegin bu handler'a ait olup olmadigini soyler.
func (h *AutoConfigHandler) Match(r *http.Request) bool { return h.kind(r) != "" }

type autodiscoverRequest struct {
	EMailAddress string `xml:"Request>EMailAddress"`
}

// ServeHTTP, Match(r) true iken cagrilir.
func (h *AutoConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	switch h.kind(r) {
	case "autoconfig":
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write(h.Cfg.AutoconfigXML())
	case "autodiscover":
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("emailaddress")))
		if r.Method == http.MethodPost {
			var req autodiscoverRequest
			body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if err := xml.Unmarshal(body, &req); err == nil && req.EMailAddress != "" {
				email = strings.ToLower(strings.TrimSpace(req.EMailAddress))
			}
		}
		at := strings.LastIndex(email, "@")
		if at <= 0 || !h.Cfg.HandlesDomain(email[at+1:]) {
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(autodiscoverError("Invalid or unsupported e-mail address"))
			return
		}
		_, _ = w.Write(h.Cfg.AutodiscoverXML(email))
	case "mtasts":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(mtaSTSPolicy(os.Getenv("ZORVEN_MTA_STS_MODE"), h.Cfg.Host)))
	default:
		http.NotFound(w, r)
	}
}

// mtaSTSPolicy, RFC 8461 politika dosyasini uretir. Mod ZORVEN_MTA_STS_MODE
// ile secilir (testing varsayilan; enforce yalniz TLS-RPT raporlari temizken).
func mtaSTSPolicy(mode, mx string) string {
	maxAge := 86400
	switch mode {
	case "enforce":
		maxAge = 604800
	case "none":
	default:
		mode = "testing"
	}
	return fmt.Sprintf("version: STSv1\r\nmode: %s\r\nmx: %s\r\nmax_age: %d\r\n", mode, mx, maxAge)
}
