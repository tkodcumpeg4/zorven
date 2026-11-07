package mail

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
	"time"
)

// receivedStamp, gelen :25 alicisinin iletinin en ustune yazdigi izleme
// basliginin (RFC 5321 madde 4.4, "Received:") bilesenleridir:
//
//	Received: from <HELO> ([<IP>])
//	        by <posta sunucusu> with ESMTP[S] id <kimlik>
//	        for <alici>; <tarih>
//
// HELO adi, IP ve alici istemcinin denetimindedir; header() hepsini temizler,
// boylece EHLO satirina gomulu CR/LF ya da ayirici karakterlerle baslik
// enjeksiyonu yapilamaz.
type receivedStamp struct {
	HELO string // istemcinin EHLO/HELO adi (ham; header() temizler)
	IP   net.IP // TCP baglantisinin uzak adresi (nil: yazilmaz)
	By   string // bu sunucunun posta ana makine adi
	TLS  bool   // STARTTLS kurulduysa ESMTPS, aksi halde ESMTP
	ID   string // islem kimligi (bos: yazilmaz)
	For  string // YALNIZ tek alici varsa adresi; coklu aliciya yazilmaz (sizinti)
	At   time.Time
}

// maxReceivedToken, temizlenen tek bir belirtecin (HELO, ana makine, alici)
// azami uzunlugu; baslik satiri 998 baytin cok altinda kalir.
const maxReceivedToken = 255

// header, katlanmis (CRLF + sekme) Received satirlarini doner; sonu CRLF'dir.
func (r receivedStamp) header() []byte {
	var b strings.Builder
	b.WriteString("Received: from ")
	b.WriteString(safeReceivedToken(r.HELO, "unknown", func(c rune) bool {
		return isAlnum(c) || c == '.' || c == '-' || c == '_' || c == ':' || c == '[' || c == ']'
	}))
	if lit := ipLiteral(r.IP); lit != "" {
		b.WriteString(" (" + lit + ")")
	}
	b.WriteString("\r\n\tby ")
	b.WriteString(safeReceivedToken(r.By, "localhost", func(c rune) bool {
		return isAlnum(c) || c == '.' || c == '-' || c == '_'
	}))
	if r.TLS {
		b.WriteString(" with ESMTPS")
	} else {
		b.WriteString(" with ESMTP")
	}
	if id := safeReceivedToken(r.ID, "", isAlnumOnly); id != "" {
		b.WriteString(" id " + id)
	}
	if rc := safeReceivedToken(r.For, "", func(c rune) bool {
		return isAlnum(c) || strings.ContainsRune("._-+@%=~", c)
	}); rc != "" {
		b.WriteString("\r\n\tfor <" + rc + ">")
	}
	b.WriteString("; " + r.At.UTC().Format(time.RFC1123Z) + "\r\n")
	return []byte(b.String())
}

func isAlnum(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isAlnumOnly(c rune) bool { return isAlnum(c) }

// safeReceivedToken, istemci kaynakli bir degeri baslikta guvenle yazilacak
// hale getirir: izinli olmayan her karakter (CR, LF, bosluk, parantez, ";",
// "<>", ASCII disi ...) "?" olur, uzunluk sinirlanir. Sonuc bossa def doner.
func safeReceivedToken(s, def string, ok func(rune) bool) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, c := range s {
		if b.Len() >= maxReceivedToken {
			break
		}
		if ok(c) {
			b.WriteRune(c)
		} else {
			b.WriteByte('?')
		}
	}
	if b.Len() == 0 {
		return def
	}
	return b.String()
}

// ipLiteral, RFC 5321 madde 4.1.3 adres degismezini uretir: "[203.0.113.7]" ya
// da "[IPv6:2001:db8::1]". Adres yoksa "" doner.
func ipLiteral(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return "[" + v4.String() + "]"
	}
	if ip.To16() == nil {
		return ""
	}
	return "[IPv6:" + ip.String() + "]"
}

// newReceivedID, bir SMTP islemi icin kisa, rastgele bir kimlik uretir (Received id).
func newReceivedID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand hatasi pratikte olmaz; zamandan turetilmis kimlik yine tekil sayilir.
		return strings.ToUpper(strconv.FormatInt(time.Now().UnixNano(), 16))
	}
	return strings.ToUpper(hex.EncodeToString(b[:]))
}
