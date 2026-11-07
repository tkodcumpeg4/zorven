package ingress

import (
	"net/http"
	"strings"
	"time"
)

// gRPC / HTTP trailer destegi (Akis G1).
//
// KARARLAR:
//   - gRPC tespiti: ziyaretci HTTP/2 ile gelir ve Content-Type "application/grpc"
//     ya da "application/grpc+proto" gibi bir alt turdur. "application/grpc-web*"
//     HTTP/1.1 uyumludur (trailer govdenin icinde cercevelenir), normal HTTP
//     yolundan gecer ve ozel islem gormez.
//   - Istek govdesi gRPC'de AKAR (client-streaming/bidi): StreamRequestBody
//     yanit beklenirken es zamanli calisir ve 32 MB tavani uygulanmaz.
//   - Gövde yakalama (capture) gRPC'de KAPALIDIR: ikili, uzun omurlu akislar
//     bellege tee edilmez. WAF yalniz istek satiri/basliklarini inceler (govde
//     incelemesi yok); verify_webhook govdeyi tamamen okumayi gerektirdiginden
//     gRPC isteklerinde desteklenmez ve reddedilir.
//   - Sıkistirma: ingress yanit sikistirmaz; grpc-encoding mesaj icinde
//     uctan uca (istemci<->sunucu) kalir, Zorven dokunmaz.
//   - Yanit trailer'lari her zaman (gRPC olmasa da) ziyaretciye yazilir.

// grpcHeadTimeout, gRPC isteklerinde yanit basligi icin taninan sure. Normal
// 30 sn, client-streaming gibi sunucunun basligi akis bitince yolladigi
// cagrilarda cok kisadir; akis omru zaten istek baglamina baglidir.
const grpcHeadTimeout = 15 * time.Minute

// isGRPCContentType, gRPC (grpc-web haric) icerik turu mu.
func isGRPCContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if !strings.HasPrefix(ct, "application/grpc") {
		return false
	}
	rest := ct[len("application/grpc"):]
	return rest == "" || rest[0] == '+'
}

// isGRPCRequest: HTTP/2 uzerinden gelen gRPC cagrisi mi.
func isGRPCRequest(r *http.Request) bool {
	return r.ProtoMajor == 2 && isGRPCContentType(r.Header.Get("Content-Type"))
}

// teTrailers: istek "TE: trailers" istiyor mu.
func teTrailers(r *http.Request) bool {
	for _, v := range r.Header.Values("Te") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "trailers") {
				return true
			}
		}
	}
	return false
}

// grpc durum kodlari (yalniz Zorven'in kendi urettigi hatalar icin).
const (
	grpcStatusUnavailable = "14"
)

// writeGRPCError, gRPC istemcisinin anlayacagi "trailers-only" yaniti yazar:
// HTTP 200 + grpc-status/grpc-message (govdesiz). Duz bir 502 gRPC
// istemcilerinde belirsiz bir hataya donusurdu.
func writeGRPCError(w http.ResponseWriter, code, msg string) {
	h := w.Header()
	h.Set("Content-Type", "application/grpc")
	h.Set("Grpc-Status", code)
	h.Set("Grpc-Message", grpcEscapeMessage(msg))
	w.WriteHeader(http.StatusOK)
}

// grpcEscapeMessage, grpc-message icin yuzde-kodlama (yalniz yazdirilabilir
// ASCII disi ve '%' kodlanir; spec: PROTOCOL-HTTP2.md).
func grpcEscapeMessage(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c <= 0x7e && c != '%' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xf])
	}
	return b.String()
}

// writeTrailers, yanit trailer'larini ResponseWriter'a yazar. Govde yazildiktan
// SONRA, http.TrailerPrefix ile: onceden "Trailer" basligi bildirmek gerekmez,
// HTTP/2'de HEADERS(END_STREAM), HTTP/1.1'de chunked sonuna yazilir.
// Hop-by-hop / baglama ait basliklar yazilmaz.
func writeTrailers(w http.ResponseWriter, tr http.Header) {
	for k, vals := range tr {
		lk := strings.ToLower(k)
		if hopByHop[lk] || lk == "content-length" || lk == "content-type" || lk == "content-encoding" {
			continue
		}
		for _, v := range vals {
			w.Header().Add(http.TrailerPrefix+k, v)
		}
	}
}
