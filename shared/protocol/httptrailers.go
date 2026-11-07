package protocol

// HTTP trailer ve gRPC destegi (Akis G1).
//
// gRPC; HTTP/2 uzerinde, yanit sonunda trailer basliklari (grpc-status,
// grpc-message) tasiyan bir protokoldur. Tunel protokolu bunu su sekilde karsilar:
//
//   - Ajan, yerel servise gRPC icin HTTP/2 (h2c ya da TLS ALPN h2) ile baglanir.
//   - Yanit govdesi bittiginde yerel servisin trailer'lari varsa ajan, SON govde
//     cercevesinden (EOF) HEMEN ONCE bir `http_trailers` kontrol mesaji yollar.
//     Kontrol mesaji ve ikili cerceveler tek WebSocket uzerinden sirayla akar,
//     bu yuzden sunucu EOF'u gordugunde trailer'lar zaten elindedir.
//   - Sunucu bu trailer'lari ziyaretciye (HTTP/2 trailer ya da HTTP/1.1 chunked
//     trailer) yazar.
//
// Geriye uyumluluk: ajan FeatureHTTPTrailers'i Hello'da ilan eder, sunucu
// hello_ack'te yankilar. Yetenek pazarlanmadiysa hicbir yeni alan/mesaj
// gonderilmez; gRPC istekleri ziyaretciye gRPC UNAVAILABLE (trailers-only) ile
// net bir hata olarak doner.
//
// Sinir: ISTEK tarafi trailer'i tasinmaz (gRPC istemcileri istek trailer'i
// gondermez; nadir HTTP kullanimlari kapsam disidir).

// FeatureHTTPTrailers, yanit trailer'lari + gRPC icin HTTP/2 yetenegi.
const FeatureHTTPTrailers = "http_trailers"

// TypeHTTPTrailers, ajandan sunucuya: yanit govdesinden sonra gelen trailer'lar.
const TypeHTTPTrailers MessageType = "http_trailers"

// HTTPTrailers, bir istegin yanit trailer'larini tasir. Son govde cercevesinden
// (EOF) once gonderilir. Bos Trailers gonderilmez.
type HTTPTrailers struct {
	Type     MessageType         `json:"type"`
	ReqID    uint64              `json:"req_id"`
	Trailers map[string][]string `json:"trailers"`
}
