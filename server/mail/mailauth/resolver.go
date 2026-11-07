package mailauth

import (
	"context"
	"errors"
	"net"
)

// Resolver, dogrulamada kullanilan DNS arayuzudur. *net.Resolver bunu dogrudan
// karsilar (blitiri.com.ar/go/spf DNSResolver ile ayni yontem kumesi); testler
// agsiz bir sahte uygulama enjekte eder.
//
// Hatalar *net.DNSError olmalidir: IsNotFound "kayit yok", IsTimeout/IsTemporary
// gecici hata demektir. SPF kutuphanesi hatayi dogrudan tip iddiasiyla siniflar,
// bu yuzden Verifier cozumleyiciyi her zaman boundedResolver ile sarar.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

var _ Resolver = (*net.Resolver)(nil)

// boundedResolver, bir Resolver'i toplam sure siniriyla uyumlu hale getirir:
//   - Baglam bitmisse (zaman asimi ya da iptal) hata gecici DNS hatasina cevrilir;
//     boylece SPF/DKIM/DMARC bunu kalici sonuc (fail/permerror) degil temperror
//     sayar ve ileti normal teslim edilir.
//   - Sarilmis hatalar acilir (SPF kutuphanesi errors.As degil tip iddiasi kullanir).
//   - DNSError olmayan hatalar gecici sayilir.
type boundedResolver struct{ inner Resolver }

func (b boundedResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	out, err := b.inner.LookupTXT(ctx, name)
	return out, normalizeDNSErr(ctx, name, err)
}

func (b boundedResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	out, err := b.inner.LookupMX(ctx, name)
	return out, normalizeDNSErr(ctx, name, err)
}

func (b boundedResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	out, err := b.inner.LookupIPAddr(ctx, host)
	return out, normalizeDNSErr(ctx, host, err)
}

func (b boundedResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	out, err := b.inner.LookupAddr(ctx, addr)
	return out, normalizeDNSErr(ctx, addr, err)
}

func normalizeDNSErr(ctx context.Context, name string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return &net.DNSError{Err: "dns zaman asimi", Name: name, IsTimeout: true, IsTemporary: true}
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return de
	}
	return &net.DNSError{Err: "dns hatasi", Name: name, IsTemporary: true}
}
