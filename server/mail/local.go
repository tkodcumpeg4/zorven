package mail

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ErrNoSuchMailbox, yerel bir alan adina (mail.<domain> veya platform domaini)
// ait olup var olmayan bir adrese teslim denendiginde doner.
var ErrNoSuchMailbox = errors.New("boyle bir posta kutusu yok")

// Local, bu sunucunun KENDI posta kutularini (<slug>@<mailDomain> ve
// <sistem>@<platformDomain>) tanir ve teslim eder. Gelen :25 alicisi, SMTP
// gonderim (587/465) ve panel gonderimi AYNI kurallari kullanir: yerel
// alicilar Postfix relay'ine GITMEZ (relay kendi alan adlari icin "loops back
// to myself" ile geri cevirir), dogrudan INBOX'a yazilir.
type Local struct {
	Store            store.Store
	MailDomain       string
	PlatformDomain   string
	SystemLocalParts map[string]bool
	// Hub, yeni maili acik IMAP oturumlarina (IDLE) bildirir (nil: DefaultHub).
	Hub *Hub
}

// NewLocal, yerel teslim cozumleyicisi olusturur.
func NewLocal(st store.Store, mailDomain, platformDomain string, systemLocalParts []string) *Local {
	sys := make(map[string]bool, len(systemLocalParts))
	for _, lp := range systemLocalParts {
		if lp = strings.ToLower(strings.TrimSpace(lp)); lp != "" {
			sys[lp] = true
		}
	}
	return &Local{
		Store:            st,
		MailDomain:       strings.ToLower(strings.TrimSpace(mailDomain)),
		PlatformDomain:   strings.ToLower(strings.TrimSpace(platformDomain)),
		SystemLocalParts: sys,
	}
}

// IsLocalDomain, adresin alan adi bu sunucuya mi ait (kutu var olmasa bile).
func (l *Local) IsLocalDomain(addr string) bool {
	d := AddressDomain(strings.ToLower(strings.TrimSpace(addr)))
	if d == "" {
		return false
	}
	return (l.MailDomain != "" && d == l.MailDomain) || (l.PlatformDomain != "" && d == l.PlatformDomain)
}

// Resolve, yerel bir adresin kiraci kimligini bulur. Alan adi yerel degilse
// (ok=false, err=nil); yerel ama kutu yoksa ErrNoSuchMailbox doner.
func (l *Local) Resolve(ctx context.Context, addr string) (tenantID string, local bool, err error) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 {
		return "", false, nil
	}
	lp, domain := addr[:at], addr[at+1:]
	if l.PlatformDomain != "" && domain == l.PlatformDomain {
		if l.SystemLocalParts[lp] {
			return store.DefaultTenantID, true, nil
		}
		return "", true, ErrNoSuchMailbox
	}
	if l.MailDomain != "" && domain == l.MailDomain {
		t, gerr := l.Store.GetTenantBySlug(ctx, lp)
		if gerr != nil {
			if errors.Is(gerr, store.ErrNotFound) {
				return "", true, ErrNoSuchMailbox
			}
			return "", true, fmt.Errorf("kutu cozumlenemedi: %w", gerr)
		}
		return t.ID, true, nil
	}
	return "", false, nil
}

// Deliver, ham mesaji yerel alicilarin INBOX'una yazar: ayristirir (konu,
// govde, ekler), her alici icin ayri kayit olusturur ve IMAP IDLE oturumlarina
// haber verir. Tekrarlanan alicilar teke indirilir. Donen sayi teslim edilen
// kutu sayisidir. Bir kayit basarisiz olursa hata doner (cagiran 451 verir).
func (l *Local) Deliver(ctx context.Context, envFrom string, rcpts []string, raw []byte) (int, error) {
	subject, fromAddr, text, html, messageID, inReplyTo, attachments := parseMessage(raw)
	if fromAddr == "" {
		fromAddr = strings.TrimSpace(envFrom)
	}
	hub := l.Hub
	if hub == nil {
		hub = DefaultHub
	}
	seen := make(map[string]bool, len(rcpts))
	n := 0
	for _, to := range rcpts {
		to = strings.ToLower(strings.TrimSpace(to))
		if to == "" || seen[to] {
			continue
		}
		seen[to] = true
		tenantID, isLocal, err := l.Resolve(ctx, to)
		if err != nil {
			return n, fmt.Errorf("%s: %w", to, err)
		}
		if !isLocal {
			return n, fmt.Errorf("%s: yerel alan adi degil", to)
		}
		saved, err := l.Store.InsertMailMessage(ctx, store.MailMessage{
			TenantID:  tenantID,
			Direction: "inbound",
			From:      fromAddr,
			To:        to,
			Subject:   subject,
			TextBody:  text,
			HTMLBody:  html,
			MessageID: messageID,
			InReplyTo: inReplyTo,
			Seen:      false,
			RawBytes:  raw,
			Folder:    store.MailFolderInbox,
			Mailbox:   to,
		})
		if err != nil {
			log.Printf("[mail] yerel teslim kaydedilemedi (tenant=%s): %v", tenantID, err)
			return n, err
		}
		for _, att := range attachments {
			att.MessageID = saved.ID
			att.TenantID = tenantID
			if aerr := l.Store.InsertMailAttachment(ctx, att); aerr != nil {
				log.Printf("[mail] ek kaydedilemedi (msg=%s): %v", saved.ID, aerr)
			}
		}
		hub.Notify(tenantID, to)
		n++
	}
	return n, nil
}
