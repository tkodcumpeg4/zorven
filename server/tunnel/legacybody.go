package tunnel

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Eski ajanla yarida kalan yanit govdesi icin sunucu guvenlik agi (B5, Y4).
//
// 0.3.8 oncesi ajan, yerel servisin yanit govdesi okunurken hata alinca
// (hedef baglantiyi kopardi) sunucuya HICBIR SEY yollamaz: ne kalan govde ne
// EOF ne http_error. Ziyaretci kendi zaman asimina kadar (tarayicida dakikalar)
// asili kalir. 0.3.8+ ayni durumda http_error yollar ve govde hatayla kapanir.
//
// Ag YALNIZ su durumda devreye girer, hepsi birlikte:
//   - ajan surumu 0.3.8'den eski (hello client_version; "dev" ya da cozulemeyen
//     surum = yeni sayilir, ag kapali: yanlis pozitif yerine eski davranis),
//   - yanit Content-Length bildirdi ve gelen bayt ondan az,
//   - okuyucu kuyrugu BOS bulup legacyBodyStall boyunca bekledi: hic cerceve
//     gelmedi. Kuyruk bosken okuyucu her seyi tuketmis ve kredinin en az yarisi
//     ajanda oldugundan bu sessizlik geri basinc degil, ajan/hedef sessizligidir.
//
// SSE, uzun yoklama ve chunked akislar (Content-Length yok) ASLA etkilenmez;
// yeni ajanlar da etkilenmez. Hata, ingress'te mevcut "govde ajan tarafinda
// kesildi" yolunu izler: ajana cancel(upstream_aborted), ziyaretciye kesik yanit
// (HTTP/1.1 baglanti kesme, HTTP/2 RST_STREAM) — tamamlanmis gibi gorunmez.
//
// Tel protokolu degismez: yeni alan/mesaj yok, yalniz sunucu tarafli zaman asimi.

// legacyBodyStall, eski ajanla Content-Length'li govdede kabul edilen en uzun
// sessizlik (degisken: testler kisaltir). Hedefin mesru olarak govde ortasinda
// bir dakikadan uzun durmasi nadirdir; eski davranista ziyaretci zaten tarayici
// zaman asimina kadar bekliyordu.
var legacyBodyStall = 60 * time.Second

// ErrBodyStalled, eski ajanla bildirilen uzunluga ulasmadan govde sessiz kaldi.
var ErrBodyStalled = errors.New("yanit govdesi eksik kaldi: eski ajan veri yollamayi kesti")

// firstBodyErrorVersion, govde okuma hatasini http_error ile bildiren ilk surum.
var firstBodyErrorVersion = [3]int{0, 3, 8}

// agentLacksBodyError, ajanin govde hatasini bildirmeyen eski bir surum olup
// olmadigi. Bos surum (cok eski istemci) eski sayilir; "dev" ve cozulemeyen
// surumler yeni sayilir (ag kapali, eski davranis korunur).
func agentLacksBodyError(clientVersion string) bool {
	v := strings.TrimSpace(clientVersion)
	if v == "" {
		return true
	}
	p, ok := parseAgentVersion(v)
	if !ok {
		return false
	}
	for i := range p {
		if p[i] != firstBodyErrorVersion[i] {
			return p[i] < firstBodyErrorVersion[i]
		}
	}
	return false
}

// parseAgentVersion, "0.3.7", "v0.3.7", "0.3.7-rc1" bicimlerini cozer.
func parseAgentVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// declaredLength, ajan yanit basliklarindaki gecerli Content-Length (yoksa -1).
// Ajan basliklari kanonik olmayabilir: anahtar harf duyarsiz aranir. Birden
// fazla/tutarsiz deger = bilinmiyor (ag kapali).
func declaredLength(h map[string][]string) int64 {
	var vals []string
	for k, v := range h {
		if strings.EqualFold(k, "Content-Length") {
			vals = append(vals, v...)
		}
	}
	if len(vals) == 0 {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(vals[0]), 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	for _, s := range vals[1:] {
		if m, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err != nil || m != n {
			return -1
		}
	}
	return n
}
