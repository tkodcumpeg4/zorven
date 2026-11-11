package ingress

import (
	"net/http"

	"github.com/tkodcumpeg4/zorven/server/obs"
)

// Tunel veri yolunda istek kimligi (A5 #7).
//
// Tasarim karari: iki baslik, iki ayri anlam.
//
//   - X-Zorven-Request-Id: HER ZAMAN Zorven'in kimligi (request_logs.id ile ayni,
//     "req_<hex>"). Ziyaretciye yanitta ve hedefe istekte gider; ziyaretcinin
//     ya da hedefin gonderdigi deger EZILIR. Destek/korelasyon icin guvenilir
//     tek anahtar budur. request_logs.id disaridan gelen degerden turetilmez:
//     birincil anahtar cakismasi (ON CONFLICT DO NOTHING) log kaybettirirdi.
//   - X-Request-Id: yaygin gelenek; tunel sahibinin uygulamasi kendi kimligini
//     bununla uretiyor/izliyor olabilir. Hedefe: ziyaretcinin gecerli
//     (^[A-Za-z0-9._-]{1,64}$) degeri korunur, yoksa ya da gecersizse Zorven
//     kimligi yazilir. Ziyaretciye: hedef yanitinda X-Request-Id VARSA ona
//     dokunulmaz (uygulamanin kimligi ezilmez); yoksa hedefe gonderilen deger
//     yankilanir. Boylece uygulama loglari ile ziyaretcinin gordugu deger ayni
//     kalir, Zorven kimligi ise ayri baslikta her zaman bulunur.
const headerZorvenRequestID = "X-Zorven-Request-Id"

// forwardRequestID, hedefe X-Request-Id olarak gidecek deger.
func forwardRequestID(r *http.Request, zid string) string {
	if v := r.Header.Get(obs.HeaderRequestID); obs.ValidRequestID(v) {
		return v
	}
	return zid
}

// setRequestIDHeaders, hedefe giden istek basliklarina kimlikleri yazar.
func setRequestIDHeaders(h map[string][]string, zid, fwd string) {
	http.Header(h).Del(obs.HeaderRequestID)
	http.Header(h).Del(headerZorvenRequestID)
	h[obs.HeaderRequestID] = []string{fwd}
	h[headerZorvenRequestID] = []string{zid}
}

// stampIDs, ziyaretci yanitina kimlikleri yazar (baslik gitmeden, bir kez).
func (rec *recorder) stampIDs() {
	if rec.zid == "" || rec.stamped {
		return
	}
	rec.stamped = true
	h := rec.Header()
	h.Set(headerZorvenRequestID, rec.zid)
	if h.Get(obs.HeaderRequestID) == "" {
		h.Set(obs.HeaderRequestID, rec.fwdID)
	}
}
