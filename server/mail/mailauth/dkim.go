package mailauth

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
)

// checkDKIM, iletideki DKIM-Signature basliklarini dogrular (en cok MaxDKIM
// imza). Imza yoksa tek bir "none" sonucu doner. Genel anahtar sorgulari enjekte
// edilen cozumleyiciden ve ortak zaman sinirindan gecer; gecici DNS hatasi
// "temperror"dur.
func (v *Verifier) checkDKIM(ctx context.Context, res Resolver, raw []byte) []DKIMResult {
	opts := &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) { return res.LookupTXT(ctx, name) },
		// Cok sayida imza, DNS ve CPU israfi icin yaygin bir saldiri yoludur.
		MaxVerifications: v.maxDKIM(),
	}
	verifs, err := dkim.VerifyWithOptions(bytes.NewReader(raw), opts)
	if err != nil && !errors.Is(err, dkim.ErrTooManySignatures) {
		if len(verifs) == 0 && strings.HasPrefix(err.Error(), "failed to read header") {
			return []DKIMResult{{Result: PermError, Reason: "malformed header"}}
		}
		return []DKIMResult{{Result: TempError, Reason: "internal error"}}
	}
	if len(verifs) == 0 {
		return []DKIMResult{{Result: None}}
	}

	out := make([]DKIMResult, 0, len(verifs))
	for _, vf := range verifs {
		r := DKIMResult{Domain: cleanDomain(vf.Domain)}
		switch {
		case vf.Err == nil:
			r.Result = Pass
		case dkim.IsTempFail(vf.Err):
			r.Result, r.Reason = TempError, "key unavailable"
		case dkim.IsPermFail(vf.Err):
			r.Result, r.Reason = PermError, dkimReason(vf.Err)
		default:
			r.Result, r.Reason = Fail, dkimReason(vf.Err)
		}
		out = append(out, r)
	}
	return out
}

// dkimReason, kutuphane hata metnini sabit bir sozluge indirger. Kutuphane
// metni DNS sunucu adresleri ya da gonderici denetimindeki alan adlari icerebilir;
// bunlar basliga hic yazilmaz.
func dkimReason(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "body hash did not verify"):
		return "body hash did not verify"
	case strings.Contains(s, "signature did not verify"):
		return "signature did not verify"
	case strings.Contains(s, "has expired"):
		return "signature expired"
	case strings.Contains(s, "no key for signature"), strings.Contains(s, "no valid key"),
		strings.Contains(s, "key revoked"), strings.Contains(s, "key syntax"),
		strings.Contains(s, "multiple TXT"), strings.Contains(s, "key record error"),
		strings.Contains(s, "incompatible public key"):
		return "no usable key"
	case strings.Contains(s, "too weak"), strings.Contains(s, "unsupported"),
		strings.Contains(s, "inappropriate"):
		return "unsupported algorithm"
	case strings.Contains(s, "insecure body length"):
		return "body length tag not allowed"
	case strings.Contains(s, "From field not signed"):
		return "from field not signed"
	}
	return "invalid signature"
}
