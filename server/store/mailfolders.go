package store

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Kullanici klasoru sinirlari ve hatalari (migration 0064).
const (
	MaxMailFoldersPerMailbox = 200
	MaxMailFolderNameLen     = 200
	// MailUserFolderPrefix, mail_messages.folder icinde kullanici klasorlerini
	// sistem klasorlerinden ('inbox', 'sent'...) ayirir: 'u:' + IMAP adi.
	MailUserFolderPrefix = "u:"
)

var (
	ErrMailFolderExists      = errors.New("mail klasoru zaten var")
	ErrMailFolderLimit       = errors.New("mail klasor sayisi siniri asildi")
	ErrMailFolderInvalid     = errors.New("gecersiz mail klasor adi")
	ErrMailFolderHasChildren = errors.New("mail klasorunun alt klasorleri var")
)

// MailFolder, kullanicinin olusturdugu bir IMAP klasorudur.
type MailFolder struct {
	Name       string // '/' ayiracli IMAP adi
	SpecialUse string // "" | \Archive | \Junk | ...
	Subscribed bool
	CreatedAt  time.Time
}

// MailUserFolderKey, klasor adini mail_messages.folder degerine cevirir.
func MailUserFolderKey(name string) string { return MailUserFolderPrefix + name }

// MailUserFolderName, folder degeri kullanici klasoruyse adini doner.
func MailUserFolderName(folder string) (string, bool) {
	if strings.HasPrefix(folder, MailUserFolderPrefix) && len(folder) > len(MailUserFolderPrefix) {
		return folder[len(MailUserFolderPrefix):], true
	}
	return "", false
}

// IsMailSystemFolderName, adin (buyuk/kucuk harf duyarsiz) ilk bileseni sanal
// sistem klasorlerinden biri mi (INBOX, Sent, Trash, Drafts).
func IsMailSystemFolderName(name string) bool {
	top := name
	if i := strings.IndexByte(top, '/'); i >= 0 {
		top = top[:i]
	}
	switch strings.ToLower(top) {
	case "inbox", "sent", "trash", "drafts":
		return true
	}
	return false
}

// ValidateMailFolderName, kullanici klasoru adini dogrular.
func ValidateMailFolderName(name string) error {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxMailFolderNameLen {
		return ErrMailFolderInvalid
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == '*' || r == '%' || r == 0xfffd {
			return ErrMailFolderInvalid
		}
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.TrimSpace(seg) != seg {
			return ErrMailFolderInvalid
		}
	}
	if IsMailSystemFolderName(name) {
		return ErrMailFolderInvalid
	}
	return nil
}

// MailFolderParents, "a/b/c" icin ["a", "a/b"] doner (ust klasorler, kokten).
func MailFolderParents(name string) []string {
	var out []string
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			out = append(out, name[:i])
		}
	}
	return out
}
