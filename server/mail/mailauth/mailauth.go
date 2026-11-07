// Package mailauth, gelen (internetten :25'e ulasan) iletilerin kimligini
// dogrular: SPF (MAIL FROM, bos gondericide HELO), DKIM imzalari ve DMARC
// hizalamasi. Sonuc RFC 8601 Authentication-Results basligi olarak iletinin en
// ustune yazilir ve muhafazakar bir teslim karari (Gelen Kutusu / Onemsiz /
// 550 5.7.1 ret) uretilir.
//
// Ilke: posta kaybetmemek. Gecici DNS hatasi, zaman asimi, gecersiz/belirsiz
// kayit ve genel olmayan istemci adresi her zaman normal teslim demektir; yalnizca
// alan adi sahibinin acik DMARC politikasi (p=reject / p=quarantine) ya da kaydin
// hic olmadigi durumda SPF "fail" + gecerli DKIM yoklugu enforce edilir.
//
// Harici DNSBL / spam servisi KULLANILMAZ: iletinin gondericisini ucuncu taraflara
// sizdirmaz, kullanim sinirina bagimli degildir. Tum DNS sorgulari enjekte edilen
// Resolver uzerinden yapilir ve toplam sure sinirlidir.
package mailauth

import (
	"context"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// RFC 8601 sonuc dizgeleri.
const (
	Pass      = "pass"
	Fail      = "fail"
	SoftFail  = "softfail"
	Neutral   = "neutral"
	None      = "none"
	TempError = "temperror"
	PermError = "permerror"
)

const (
	// DefaultTimeout, SPF + DKIM + DMARC DNS sorgularinin TOPLAM suresidir.
	DefaultTimeout = 8 * time.Second
	// DefaultMaxDKIM, dogrulanacak en cok DKIM imzasi sayisidir (fazlasi yok sayilir).
	DefaultMaxDKIM = 5
)

// Action, dogrulama sonucunda ileti icin verilen teslim kararidir.
type Action int

const (
	// ActionInbox, normal teslim (Gelen Kutusu).
	ActionInbox Action = iota
	// ActionJunk, Onemsiz (\Junk) klasorune teslim.
	ActionJunk
	// ActionReject, SMTP DATA sonunda 550 5.7.1 ile ret.
	ActionReject
)

func (a Action) String() string {
	switch a {
	case ActionJunk:
		return "junk"
	case ActionReject:
		return "reject"
	}
	return "inbox"
}

// Input, bir ileti icin dogrulama girdisidir.
type Input struct {
	ClientIP net.IP // SMTP istemcisinin adresi
	HELO     string // EHLO/HELO adi
	MailFrom string // zarf gondericisi ("" = bos gonderici)
	Message  []byte // ham RFC 5322 iletisi (Authentication-Results eklenmeden once)
}

// SPFResult, SPF kontrolunun sonucudur.
type SPFResult struct {
	Result   string // pass fail softfail neutral none temperror permerror
	Identity string // "mailfrom" | "helo" (bos gondericide HELO denetlenir)
	Domain   string // denetlenen alan adi
	Sender   string // Authentication-Results icin guvenli smtp.mailfrom / smtp.helo degeri
	ClientIP string
	Skipped  bool   // istemci adresi genel degil: kontrol yapilmadi
	Reason   string // kisa, sabit sozlukten aciklama
}

// DKIMResult, tek bir DKIM imzasinin sonucudur.
type DKIMResult struct {
	Result string // pass fail none temperror permerror
	Domain string // imzalayan alan adi (d=)
	Reason string
}

// DMARCResult, DMARC degerlendirmesinin sonucudur.
type DMARCResult struct {
	Result      string // pass fail none temperror permerror
	FromDomain  string // RFC5322.From alan adi
	Policy      string // uygulanan politika (p ya da alt alan adi icin sp): none | quarantine | reject
	Disposition string // pct ornekleme sonrasi fiili politika
	NoRecord    bool   // DNS dogrulandi: ne alan adinda ne organizasyon alan adinda DMARC kaydi yok
	SPFAligned  bool
	DKIMAligned bool
	Note        string
}

// Result, bir iletinin tum dogrulama sonuclari ve teslim kararidir.
type Result struct {
	SPF    SPFResult
	DKIM   []DKIMResult
	DMARC  DMARCResult
	Action Action
	// Why, kararin kisa gerekcesidir (log icin; Action == ActionInbox iken genelde bos).
	Why string
}

// AnyDKIMPass, dogrulanmis en az bir DKIM imzasi var mi.
func (r *Result) AnyDKIMPass() bool {
	for _, d := range r.DKIM {
		if d.Result == Pass {
			return true
		}
	}
	return false
}

// Verifier, gelen iletileri dogrular. Sifir deger kullanilabilir
// (net.DefaultResolver, 8 sn, en cok 5 imza).
type Verifier struct {
	// Resolver, DNS arayuzudur (nil: net.DefaultResolver).
	Resolver Resolver
	// Timeout, tum DNS islemlerinin toplam suresidir (0: DefaultTimeout).
	Timeout time.Duration
	// MaxDKIM, dogrulanacak en cok imza sayisidir (0: DefaultMaxDKIM).
	MaxDKIM int
	// Rand, DMARC pct ornekleme icin [0,100) arasi sayi dondurur (nil: math/rand/v2).
	Rand func() int
}

// NewVerifier, verilen cozumleyiciyle (nil: sistem cozumleyicisi) bir dogrulayici doner.
func NewVerifier(r Resolver) *Verifier { return &Verifier{Resolver: r} }

func (v *Verifier) timeout() time.Duration {
	if v.Timeout > 0 {
		return v.Timeout
	}
	return DefaultTimeout
}

func (v *Verifier) maxDKIM() int {
	if v.MaxDKIM > 0 {
		return v.MaxDKIM
	}
	return DefaultMaxDKIM
}

func (v *Verifier) resolver() Resolver {
	if v.Resolver != nil {
		return boundedResolver{v.Resolver}
	}
	return boundedResolver{net.DefaultResolver}
}

func (v *Verifier) rand() int {
	if v.Rand != nil {
		return v.Rand()
	}
	return rand.IntN(100)
}

// Verify, iletiyi dogrular. SPF, DKIM ve DMARC kaydi aramasi ayni toplam sure
// siniri icinde paralel calisir; sure dolarsa ilgili sonuclar temperror olur.
// Verify panik atmaz ve hata dondurmez: her durumda bir Result doner.
func (v *Verifier) Verify(ctx context.Context, in Input) *Result {
	ctx, cancel := context.WithTimeout(ctx, v.timeout())
	defer cancel()
	res := v.resolver()

	from := parseFrom(in.Message)

	var (
		spfRes  SPFResult
		dkimRes []DKIMResult
		lookup  dmarcLookup
		wg      sync.WaitGroup
	)
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Kutuphanelerde beklenmeyen bir panik SMTP surecini dusurmesin;
			// sonuc "gecici hata" kalir ve ileti normal teslim edilir.
			defer func() { _ = recover() }()
			fn()
		}()
	}
	run(func() { spfRes = v.checkSPF(ctx, res, in) })
	run(func() { dkimRes = v.checkDKIM(ctx, res, in.Message) })
	if from.err == "" {
		run(func() { lookup = v.lookupDMARC(ctx, res, from.domain) })
	}
	wg.Wait()

	if spfRes.Result == "" {
		spfRes = SPFResult{Result: TempError, Reason: "internal error"}
	}
	if len(dkimRes) == 0 {
		dkimRes = []DKIMResult{{Result: TempError, Reason: "internal error"}}
	}

	out := &Result{SPF: spfRes, DKIM: dkimRes}
	out.DMARC = v.evalDMARC(from, lookup, spfRes, dkimRes)
	out.Action, out.Why = decide(out)
	return out
}
