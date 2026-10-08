package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const (
	// Kutu basina gonderim siniri (kayan pencere).
	submitWindow = time.Hour
	submitLimit  = 300
)

// Relay, kabul edilen mesajin dis dunyaya (Postfix relay + DKIM) iletilme arayuzudur.
type Relay interface {
	SendRaw(from string, to []string, raw []byte) error
	Domain() string
}

// SubmissionServer, kimlik dogrulamali SMTP gonderim (587 STARTTLS / 465 TLS)
// sunucusudur. Mail istemcilerinin giden postasi buradan gecer.
type SubmissionServer struct {
	Auth       *Authenticator
	Store      store.Store
	Relay      Relay
	Hub        *Hub
	Logger     *slog.Logger
	MailDomain string
	// Local, yerel alicilari (kendi alan adlarimiz) INBOX'a teslim eder; bunlar
	// relay'e GITMEZ. Harici alicilar relay'e gider.
	Local *Local
	// AllowExternal, kiracinin harici alicilara gonderip gonderemeyecegini soyler
	// (vars. ExternalSendAllowed; acik surumde her zaman true).
	AllowExternal func(ctx context.Context, tenantID string) bool

	mu   sync.Mutex
	sent map[string][]time.Time
}

// NewSubmissionServer, gonderim sunucusu mantigini olusturur.
func NewSubmissionServer(auth *Authenticator, st store.Store, relay Relay, hub *Hub, logger *slog.Logger) *SubmissionServer {
	if hub == nil {
		hub = DefaultHub
	}
	if logger == nil {
		logger = slog.Default()
	}
	local := NewLocal(st, auth.MailDomain, auth.PlatformDomain, nil)
	local.SystemLocalParts = auth.SystemLocalParts
	local.Hub = hub
	return &SubmissionServer{
		Auth: auth, Store: st, Relay: relay, Hub: hub, Logger: logger,
		MailDomain: auth.MailDomain,
		Local:      local,
		AllowExternal: func(ctx context.Context, tenantID string) bool {
			return ExternalSendAllowed(ctx, st, tenantID)
		},
		sent: make(map[string][]time.Time),
	}
}

// NewSMTPServer, go-smtp sunucusunu yapilandirir. implicitTLS true ise cagiran
// ListenAndServeTLS kullanmalidir (465); false ise STARTTLS zorunludur (587).
func (s *SubmissionServer) NewSMTPServer(addr string, tlsCfg *tls.Config) *smtp.Server {
	srv := smtp.NewServer(&submissionBackend{s: s})
	srv.Addr = addr
	srv.Domain = firstNonEmptyStr(s.Auth.MailDomain, "localhost")
	srv.TLSConfig = tlsCfg
	srv.AllowInsecureAuth = false
	srv.ReadTimeout = 5 * time.Minute
	srv.WriteTimeout = 5 * time.Minute
	srv.MaxMessageBytes = maxMessageBytes
	srv.MaxRecipients = maxRecipients
	srv.EnableSMTPUTF8 = true
	return srv
}

// allowSend, kutu icin gonderim hiz sinirini isler.
func (s *SubmissionServer) allowSend(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	cut := now.Add(-submitWindow)
	l := s.sent[addr]
	i := 0
	for i < len(l) && l[i].Before(cut) {
		i++
	}
	l = l[i:]
	if len(l) >= submitLimit {
		s.sent[addr] = l
		return false
	}
	s.sent[addr] = append(l, now)
	return true
}

type submissionBackend struct{ s *SubmissionServer }

func (b *submissionBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	ip := ""
	if c != nil && c.Conn() != nil {
		ip = remoteIP(c.Conn().RemoteAddr())
	}
	return &submissionSession{s: b.s, conn: c, ip: ip}, nil
}

type submissionSession struct {
	s    *SubmissionServer
	conn *smtp.Conn
	ip   string

	acct *Account
	from string
	rcpt []string

	externalChecked bool
	externalOK      bool
}

var _ smtp.AuthSession = (*submissionSession)(nil)

func smtpErr(code int, ec smtp.EnhancedCode, msg string) error {
	return &smtp.SMTPError{Code: code, EnhancedCode: ec, Message: msg}
}

func (ss *submissionSession) AuthMechanisms() []string { return []string{sasl.Plain, sasl.Login} }

func (ss *submissionSession) Auth(mech string) (sasl.Server, error) {
	doAuth := func(username, password string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		acct, err := ss.s.Auth.Authenticate(ctx, "smtp", username, password, ss.ip)
		if err != nil {
			if errors.Is(err, ErrAuthRateLimited) {
				return smtpErr(454, smtp.EnhancedCode{4, 7, 0}, "Too many failed attempts, try again later")
			}
			return smtp.ErrAuthFailed
		}
		ss.acct = acct
		return nil
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			if identity != "" && !strings.EqualFold(identity, username) {
				return smtp.ErrAuthFailed
			}
			return doAuth(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{auth: doAuth}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

// loginServer, sasl LOGIN mekanizmasinin sunucu tarafidir (go-sasl'ta yok).
type loginServer struct {
	state    int
	username string
	auth     func(user, pass string) error
}

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.state {
	case 0:
		if len(response) > 0 { // ilk yanit (AUTH LOGIN <kullanici>)
			l.username = string(response)
			l.state = 2
			return []byte("Password:"), false, nil
		}
		l.state = 1
		return []byte("Username:"), false, nil
	case 1:
		l.username = string(response)
		l.state = 2
		return []byte("Password:"), false, nil
	default:
		return nil, true, l.auth(l.username, string(response))
	}
}

func (ss *submissionSession) requireAuth() error {
	if ss.acct != nil {
		return nil
	}
	if _, ok := ss.conn.TLSConnectionState(); !ok {
		return smtpErr(530, smtp.EnhancedCode{5, 7, 0}, "Must issue a STARTTLS command first")
	}
	return smtpErr(530, smtp.EnhancedCode{5, 7, 0}, "Authentication required")
}

func (ss *submissionSession) Mail(from string, _ *smtp.MailOptions) error {
	if err := ss.requireAuth(); err != nil {
		return err
	}
	addr, err := ParseAddress(from)
	if err != nil || addr != ss.acct.Address {
		return smtpErr(553, smtp.EnhancedCode{5, 7, 1}, "Sender address must match the authenticated mailbox "+ss.acct.Address)
	}
	ss.from = addr
	return nil
}

func (ss *submissionSession) externalAllowed(ctx context.Context) bool {
	if !ss.externalChecked {
		ss.externalChecked = true
		ss.externalOK = ss.s.AllowExternal != nil && ss.s.AllowExternal(ctx, ss.acct.TenantID)
	}
	return ss.externalOK
}

func (ss *submissionSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if ss.acct == nil || ss.from == "" {
		return smtpErr(503, smtp.EnhancedCode{5, 5, 1}, "Need MAIL before RCPT")
	}
	addr, err := ParseAddress(to)
	if err != nil {
		return smtpErr(553, smtp.EnhancedCode{5, 1, 3}, "Invalid recipient address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Yerel alan adi (mail.<domain> / platform domaini): var olmayan kutu RCPT
	// aninda reddedilir; plan kisiti yerel teslime uygulanmaz.
	if ss.s.Local.IsLocalDomain(addr) {
		_, _, rerr := ss.s.Local.Resolve(ctx, addr)
		switch {
		case errors.Is(rerr, ErrNoSuchMailbox):
			return smtpErr(550, smtp.EnhancedCode{5, 1, 1}, "No such mailbox: "+addr)
		case rerr != nil:
			return smtpErr(451, smtp.EnhancedCode{4, 3, 0}, "Temporary lookup failure, try again later")
		}
	} else if !ss.externalAllowed(ctx) {
		// Panel ile ayni kural (bkz. plan.go).
		return smtpErr(550, smtp.EnhancedCode{5, 7, 1},
			"External email is only available on the Enterprise plan; you can send to @"+ss.s.MailDomain+" addresses")
	}
	ss.rcpt = append(ss.rcpt, addr)
	return nil
}

func (ss *submissionSession) Data(r io.Reader) error {
	if ss.acct == nil || ss.from == "" || len(ss.rcpt) == 0 {
		return smtpErr(503, smtp.EnhancedCode{5, 5, 1}, "Need MAIL and RCPT before DATA")
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxMessageBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxMessageBytes {
		return smtpErr(552, smtp.EnhancedCode{5, 3, 4}, "Message too large (25 MB limit)")
	}

	// From basligi kimligi dogrulanmis kutuyla ayni olmali (sahte gonderen engeli).
	froms := HeaderAddresses(raw, "From")
	if len(froms) != 1 || froms[0] != ss.acct.Address {
		return smtpErr(550, smtp.EnhancedCode{5, 7, 1}, "From header must match the authenticated mailbox "+ss.acct.Address)
	}
	if !ss.s.allowSend(ss.acct.Address) {
		return smtpErr(450, smtp.EnhancedCode{4, 7, 1}, "Sending rate limit reached, try again later")
	}

	domain := ss.s.MailDomain
	if ss.s.Relay != nil && ss.s.Relay.Domain() != "" {
		domain = ss.s.Relay.Domain()
	}
	full, messageID := EnsureEnvelopeHeaders(raw, domain)
	// Bcc alicilari zarfta kalir, iletilen kopyadan cikarilir.
	wire := StripHeader(full, "Bcc")

	// Alicilari bol: yerel olanlar dogrudan INBOX'a, harici olanlar relay'e.
	var localRcpt, relayRcpt []string
	for _, a := range ss.rcpt {
		if ss.s.Local.IsLocalDomain(a) {
			localRcpt = append(localRcpt, a)
		} else {
			relayRcpt = append(relayRcpt, a)
		}
	}
	if len(relayRcpt) > 0 && ss.s.Relay == nil {
		return smtpErr(451, smtp.EnhancedCode{4, 3, 0}, "Mail relay is not configured")
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dcancel()
	if len(localRcpt) > 0 {
		if _, err := ss.s.Local.Deliver(dctx, ss.from, localRcpt, wire); err != nil {
			ss.s.Logger.Warn("yerel teslim hatasi", "kutu", ss.acct.Address, "hata", err)
			if errors.Is(err, ErrNoSuchMailbox) {
				return smtpErr(550, smtp.EnhancedCode{5, 1, 1}, "No such mailbox")
			}
			return smtpErr(451, smtp.EnhancedCode{4, 3, 0}, "Temporary delivery failure, try again later")
		}
	}
	if len(relayRcpt) > 0 {
		if err := ss.s.Relay.SendRaw(ss.from, relayRcpt, wire); err != nil {
			ss.s.Logger.Warn("gonderim relay hatasi", "kutu", ss.acct.Address, "hata", err)
			return smtpErr(451, smtp.EnhancedCode{4, 4, 0}, "Temporary delivery failure, try again later")
		}
	}

	// Gonderilenler klasorune kopya (istemci ayrica APPEND ederse Message-ID ile tekillesir).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, found, ferr := ss.s.Store.FindMailByMessageID(ctx, ss.acct.TenantID, ss.acct.Address, store.MailFolderSent, messageID); ferr == nil && !found {
		if _, serr := storeRawMessage(ctx, ss.s.Store, ss.acct, store.MailFolderSent, full, appendFlagSet{seen: true}); serr != nil {
			ss.s.Logger.Warn("gonderilen kopya kaydedilemedi", "kutu", ss.acct.Address, "hata", serr)
		} else {
			ss.s.Hub.Notify(ss.acct.TenantID, ss.acct.Address)
		}
	}
	ss.s.Logger.Info("mail istemcisinden mail gonderildi", "kutu", ss.acct.Address, "alici_sayisi", len(ss.rcpt), "ip", ss.ip)
	return nil
}

func (ss *submissionSession) Reset() {
	ss.from = ""
	ss.rcpt = nil
}

func (ss *submissionSession) Logout() error { return nil }

// ListenSubmission, 587 (STARTTLS) dinleyicisini baslatir.
func ListenSubmission(srv *smtp.Server) error { return srv.ListenAndServe() }

// ListenSubmissions, 465 (implicit TLS) dinleyicisini baslatir.
func ListenSubmissions(srv *smtp.Server) error { return srv.ListenAndServeTLS() }
