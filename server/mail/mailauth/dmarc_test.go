package mailauth

import (
	"context"
	"testing"

	"github.com/emersion/go-msgauth/dmarc"
)

func TestNormalizeDMARC(t *testing.T) {
	cases := map[string]string{
		"v=DMARC1; p=reject":                             "v=DMARC1;p=reject",
		"v=DMARC1; P=REJECT; ADKIM=S":                    "v=DMARC1;p=reject;adkim=s",
		"v=DMARC1; sp=Quarantine; pct=50":                "v=DMARC1;sp=quarantine;pct=50",
		"v=DMARC1; p=none; rua=mailto:Rapor@Example.com": "v=DMARC1;p=none;rua=mailto:Rapor@Example.com",
		"v=DMARC1; p=none; t=Y; np=Reject":               "v=DMARC1;p=none; t=Y; np=Reject",
		"v=DMARC1; p=none;":                              "v=DMARC1;p=none;",
	}
	for in, want := range cases {
		if got := normalizeDMARC(in); got != want {
			t.Errorf("normalizeDMARC(%q) = %q, beklenen %q", in, got, want)
		}
	}
	// Normallestirilmis kayit kutuphane tarafindan ayristirilir.
	rec, err := dmarc.Parse(normalizeDMARC("v=DMARC1; P=REJECT; ADKIM=S; ASPF=R; PCT=20; SP=NONE"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Policy != dmarc.PolicyReject || rec.SubdomainPolicy != dmarc.PolicyNone ||
		rec.DKIMAlignment != dmarc.AlignmentStrict || rec.SPFAlignment != dmarc.AlignmentRelaxed ||
		rec.Percent == nil || *rec.Percent != 20 {
		t.Fatalf("kayit: %+v", rec)
	}
}

func TestDMARCAtStates(t *testing.T) {
	ctx := context.Background()
	fr := newFakeResolver()
	fr.txt["_dmarc.ok.example"] = []string{"v=DMARC1; p=quarantine"}
	fr.txt["_dmarc.bosluklu.example"] = []string{"  v = DMARC1 ; p=none"}
	fr.txt["_dmarc.kucuk.example"] = []string{"v=dmarc1; p=reject"} // v degeri buyuk/kucuk harf duyarli
	fr.txt["_dmarc.iki.example"] = []string{"v=DMARC1; p=none", "v=DMARC1; p=reject"}
	fr.txt["_dmarc.bozuk.example"] = []string{"v=DMARC1; pct=10"} // p yok
	fr.errs["_dmarc.gecici.example"] = errTempDNS
	fr.errs["_dmarc.zaman.example"] = errTimeout
	res := boundedResolver{fr}

	cases := []struct {
		domain string
		want   lookupState
	}{
		{"ok.example", stFound},
		{"bosluklu.example", stFound},
		{"kucuk.example", stNone}, // "v=dmarc1" DMARC kaydi sayilmaz
		{"iki.example", stPerm},
		{"bozuk.example", stPerm},
		{"gecici.example", stTemp},
		{"zaman.example", stTemp},
		{"yok.example", stNone},
	}
	for _, tc := range cases {
		if got, _ := dmarcAt(ctx, res, tc.domain); got != tc.want {
			t.Errorf("dmarcAt(%s) = %v, beklenen %v", tc.domain, got, tc.want)
		}
	}
}

func TestAligned(t *testing.T) {
	cases := []struct {
		a, b   string
		strict bool
		want   bool
	}{
		{"example.com", "example.com", true, true},
		{"example.com", "example.com", false, true},
		{"mail.example.com", "example.com", false, true},
		{"mail.example.com", "example.com", true, false},
		{"a.example.com", "b.example.com", false, true},
		{"example.com", "example.org", false, false},
		{"evilexample.com", "example.com", false, false},
		// Genel sonek: ayni eTLD+1 degil.
		{"a.co.uk", "b.co.uk", false, false},
		{"x.github.io", "y.github.io", false, false},
		{"", "example.com", false, false},
		{"example.com", "", true, false},
	}
	for _, tc := range cases {
		if got := aligned(tc.a, tc.b, tc.strict); got != tc.want {
			t.Errorf("aligned(%q,%q,strict=%v) = %v, beklenen %v", tc.a, tc.b, tc.strict, got, tc.want)
		}
	}
}
