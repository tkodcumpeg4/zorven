package mailauth

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"

	"github.com/emersion/go-msgauth/dmarc"
)

// lookupState, bir DMARC kaydi aramasinin sonucudur. Sifir deger "gecici hata"dir:
// belirsizlik her zaman normal teslim demektir.
type lookupState int

const (
	stTemp  lookupState = iota // gecici DNS hatasi / zaman asimi / hic calismadi
	stFound                    // tek ve gecerli kayit
	stNone                     // DNS dogrulandi: kayit yok
	stPerm                     // kayit gecersiz ya da birden fazla
)

type dmarcLookup struct {
	state lookupState
	rec   *dmarc.Record
	atOrg bool // kayit alan adinda degil, organizasyon alan adinda bulundu
}

// dmarcV1Re, bir TXT kaydinin DMARC kaydi olup olmadigini soyler (RFC 7489
// madde 6.6.3: "v=DMARC1" ilk etiket ve buyuk/kucuk harf duyarli).
var dmarcV1Re = regexp.MustCompile(`^\s*v\s*=\s*DMARC1\s*(;|$)`)

// dmarcTagNames, bilinen DMARC etiketleridir; etiket adlari buyuk/kucuk harf
// duyarsiz okunur. dmarcValueCI olanlarin DEGERLERI de duyarsizdir (RFC 7489
// madde 6.4: politika ve hizalama sozcukleri); pct/ri sayi, rua/ruf URI'dir.
var (
	dmarcTagNames = map[string]bool{"p": true, "sp": true, "adkim": true, "aspf": true, "fo": true,
		"rf": true, "pct": true, "ri": true, "rua": true, "ruf": true}
	dmarcValueCI = map[string]bool{"p": true, "sp": true, "adkim": true, "aspf": true, "fo": true, "rf": true}
)

// normalizeDMARC, kayittaki bilinen etiket adlarini ve (sozcuk degerli
// etiketlerin) degerlerini kucuk harfe indirir; "P=REJECT" gibi gecerli ama
// buyuk harfli kayitlar kutuphane tarafindan reddedilmesin (bu, politika
// uygulanmamasina yol acardi). "v=DMARC1" ve bilinmeyen etiketler aynen kalir.
func normalizeDMARC(txt string) string {
	parts := strings.Split(txt, ";")
	for i, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		tag := strings.ToLower(strings.TrimSpace(k))
		if !dmarcTagNames[tag] {
			continue
		}
		v = strings.TrimSpace(v)
		if dmarcValueCI[tag] {
			v = strings.ToLower(v)
		}
		parts[i] = tag + "=" + v
	}
	return strings.Join(parts, ";")
}

// dmarcAt, _dmarc.<domain> TXT kaydini arar ve ayristirir. Ayristirma
// github.com/emersion/go-msgauth/dmarc ile yapilir; DNS hatasi siniflamasi bizdedir
// (kutuphanenin Lookup'i organizasyon alan adina dusmez ve hata ayrimini gizler).
func dmarcAt(ctx context.Context, res Resolver, domain string) (lookupState, *dmarc.Record) {
	txts, err := res.LookupTXT(ctx, "_dmarc."+domain)
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && de.IsNotFound {
			return stNone, nil
		}
		return stTemp, nil
	}
	var cands []string
	for _, t := range txts {
		if dmarcV1Re.MatchString(t) {
			cands = append(cands, t)
		}
	}
	switch len(cands) {
	case 0:
		return stNone, nil
	case 1:
		rec, perr := dmarc.Parse(normalizeDMARC(cands[0]))
		if perr != nil {
			return stPerm, nil
		}
		return stFound, rec
	}
	// Birden fazla DMARC kaydi: politika bulunamadi sayilir (RFC 7489); biz bunu
	// "belirsiz" kabul edip enforce etmeyiz.
	return stPerm, nil
}

// lookupDMARC, once From alan adinda, bulunamazsa organizasyon alan adinda
// DMARC politikasini arar (RFC 7489 madde 6.6.3).
func (v *Verifier) lookupDMARC(ctx context.Context, res Resolver, domain string) dmarcLookup {
	st, rec := dmarcAt(ctx, res, domain)
	if st != stNone {
		return dmarcLookup{state: st, rec: rec}
	}
	org := orgDomain(domain)
	if org == domain {
		return dmarcLookup{state: stNone}
	}
	st, rec = dmarcAt(ctx, res, org)
	return dmarcLookup{state: st, rec: rec, atOrg: true}
}

// evalDMARC, SPF ve DKIM sonuclarindan DMARC sonucunu hesaplar (RFC 7489 madde 3).
// DMARC gecmesi icin SPF'nin ya da bir DKIM imzasinin gecmesi VE ilgili alan
// adinin RFC5322.From ile hizali olmasi gerekir. Hizali kimlik dogrulamasi
// gecici hatayla belirsiz kaldiysa sonuc temperror'dur (enforce edilmez).
func (v *Verifier) evalDMARC(from fromInfo, lk dmarcLookup, spf SPFResult, dk []DKIMResult) DMARCResult {
	d := DMARCResult{FromDomain: from.domain}
	if from.err != "" {
		d.Result, d.Note = PermError, from.err
		return d
	}
	switch lk.state {
	case stNone:
		d.Result, d.NoRecord = None, true
		return d
	case stPerm:
		d.Result, d.Note = PermError, "invalid or ambiguous record"
		return d
	case stTemp:
		d.Result, d.Note = TempError, "dns lookup failed"
		return d
	}

	rec := lk.rec
	strictSPF := rec.SPFAlignment == dmarc.AlignmentStrict
	strictDKIM := rec.DKIMAlignment == dmarc.AlignmentStrict

	policy := string(rec.Policy)
	if lk.atOrg && rec.SubdomainPolicy != "" {
		policy = string(rec.SubdomainPolicy)
	}
	d.Policy = policy

	spfAligned := spf.Result == Pass && aligned(spf.Domain, from.domain, strictSPF)
	spfUnknown := spf.Result == TempError && aligned(spf.Domain, from.domain, strictSPF)
	var dkimAligned, dkimUnknown bool
	for _, k := range dk {
		if !aligned(k.Domain, from.domain, strictDKIM) {
			continue
		}
		switch k.Result {
		case Pass:
			dkimAligned = true
		case TempError:
			dkimUnknown = true
		}
	}
	d.SPFAligned, d.DKIMAligned = spfAligned, dkimAligned

	switch {
	case spfAligned || dkimAligned:
		d.Result, d.Disposition = Pass, "none"
	case spfUnknown || dkimUnknown:
		d.Result, d.Note = TempError, "spf or dkim temporarily unavailable"
	case spf.Skipped:
		// Istemci adresi genel degil (Docker proxy, yerel test): SPF'den kanit
		// yok, DKIM de hizalanmadi. Karar verilemez; enforce edilmez.
		d.Result, d.Note = None, "spf skipped"
	default:
		d.Result, d.Disposition = Fail, v.sample(policy, rec.Percent)
	}
	return d
}

// sample, DMARC pct etiketini uygular: iletilerin yuzde pct'sine politika
// uygulanir, geri kalanina bir alt politika (reject -> quarantine -> none)
// uygulanir (RFC 7489 madde 6.3). Kayitta pct yoksa ya da 100 ise politika aynen gecer.
func (v *Verifier) sample(policy string, pct *int) string {
	if pct == nil || *pct >= 100 || policy == "none" {
		return policy
	}
	if v.rand() < *pct {
		return policy
	}
	switch policy {
	case "reject":
		return "quarantine"
	case "quarantine":
		return "none"
	}
	return policy
}
