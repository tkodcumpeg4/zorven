package protocol

// Ham TCP ve WebSocket akislari icin akis basina yetenek pazarligi.
//
// NEDEN akis basina (Hello.Features yerine): yetenegi kullanabilmek icin
// sunucu tarafindaki TUKETICININ da (ziyaretci koprusu) onu desteklemesi
// gerekir; ayni oturumda hem destekleyen (rawproxy) hem desteklemeyen
// (kume aktarimi) tuketiciler vardir. Sunucu StreamOpen.Caps / WSOpen.Caps ile
// onerir, ajan StreamAck.Caps / WSAccept.Caps ile kabul ettiklerini yankilar.
// Eski ajan alani bilmez, yankilamaz -> sunucu eski davranisa (bayt tavani)
// duser. Eski sunucu onermez -> yeni ajan eski davranisi surdurur.
//
// Kredi (StreamCapCredit), HTTP govdesindeki pencereyle ayni mantiktir ve ayni
// mesaji kullanir: alici tukettikce WindowUpdate{ReqID, Increment} gonderir.
// Iki yonde de: sunucu -> ajan (ajan->ziyaretci verisi tuketildi) ve ajan ->
// sunucu (ziyaretci->hedef verisi yerel hedefe yazildi). Her iki yonde
// baslangic penceresi InitialWindowBytes'tir. Her cerceve len(payload) +
// StreamFrameCost kredi harcar: cok kucuk cercevelerle alici kuyrugu (cerceve
// sayisi) sisirilemesin.
//
// Yari kapama (StreamCapHalfClose, yalniz tcp): bir taraf yazmayi bitirince
// (okumada EOF) bos govdeli FrameStreamData (EOF bayragi set) gonderir; alici
// kalan veriyi yazdiktan SONRA kendi tarafinda CloseWrite yapar, akis acik
// kalir. Iki yon de bittiginde sunucu StreamClose ile akisi kapatir. EOF
// isareti kredi harcamaz.
const (
	StreamCapCredit    = "credit"
	StreamCapHalfClose = "half_close"
)

// ReqCapBodyCredit, HTTP istek govdesi (ziyaretci -> sunucu -> ajan -> yerel
// servis) icin istek basina kredi.
//
// NEDEN: bu yonde akis kontrolu yoktu; sunucu govdeyi ziyaretcinin hiziyla
// akitiyor, ajan ise yerel servise yazilmayi bekleyen veriyi 4 MB'lik kuyrukta
// tutup tasinca istegi iptal ediyordu (yavas tuketen yerel serviste 30 MB
// yukleme 10/10 basarisiz).
//
// NEDEN Hello.Features degil de istek basina: el sikisma (server/tunnel
// handler.go) bu degisikligin kapsami disinda ve govde akisinin tek ureticisi
// Session.streamRequestBody oldugu icin karar zaten istek basina verilir.
// Yanit yonundeki pencereden (FeatureFlowControl) ayridir: o pazarlik eski
// ajanlarda da vardir ama istek yonunu kapsamaz.
//
// Tel bicimi (yeni mesaj YOK, WindowUpdate yeniden kullanilir):
//   - Sunucu, govdeli istegin HTTPRequest.Caps'ine "body_credit" koyar.
//   - Yeni ajan, istegi isleyen goroutine'de yerel istege baslamadan ONCE
//     WindowUpdate{ReqID, Increment: 0} yollar (kabul). Ayni baglantida
//     yazimlar sirali oldugu icin kabul, o istegin yanit basligindan
//     (http_response / http_error) once ulasir.
//   - Sunucu her govde cercevesi icin len(payload) + StreamFrameCost kredi
//     harcar (EOF cercevesi harcamaz); baslangic penceresi InitialWindowBytes.
//   - Ajan veriyi yerel servise yazdikca (ya da veriyi bilerek attiginda:
//     yerel servis govdeyi okumayi birakti, istek bitti) ayni birimle kredi
//     iade eder.
//   - Eski ajan kabul yollamaz. Sunucu, kabul gelmeden yanit basligi gelirse
//     ya da pencere bitip kisa bir sure (sunucu tarafindaki bekleme) icinde
//     kabul gelmezse eski davranisa (kredisiz) duser.
//   - Eski sunucu Caps koymaz; yeni ajan kabul ya da kredi YOLLAMAZ.
const ReqCapBodyCredit = "body_credit"

// StreamFrameCost, kredi hesabinda her ham akis / WS cercevesine eklenen sabit
// maliyet. 2 MB pencerede en fazla 512 cerceve ucusta olabilir; ajan yazma
// kuyrugunun (1024 oge) ve sunucu kuyrugunun cerceve sayisi sinirli kalir.
const StreamFrameCost = 4096

// HasCap, caps listesinde c var mi.
func HasCap(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}
