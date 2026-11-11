package ingress

import (
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// HTTP tunel akisinin zaman ve kaynak korumalari (runtime denetimi 2026-11-11:
// Y4, Y5, O11, O14).
//
// main.go'daki http.Server'da ReadTimeout/WriteTimeout YOK (tunel yanitlari
// uzun omurlu: SSE, buyuk indirme). Bu yuzden toplam sureyi sinirlamak yerine
// yalniz ILERLEME OLMAYAN baglantilar kesilir: govde okunurken ve yanit
// yazilirken kayan (her ilerlemede ileri alinan) deadline'lar.

// Degiskenler testlerde kisaltilir; calisma zamaninda degistirilmez.
var (
	// requestBodyIdleTimeout, istek govdesinde ilerleme penceresi: bu surede
	// requestBodyMinProgress bayt gelmezse yukleme kesilir (408). Mesru yavas
	// mobil yukleme (2G bile >1 KB/sn) etkilenmez; 1 bayt/sn gonderen
	// slowloris govdesi 60 sn'de kesilir.
	requestBodyIdleTimeout       = 60 * time.Second
	requestBodyMinProgress int64 = 1 << 10

	// responseWriteStallTimeout, ziyaretciye tek bir yazimin (en fazla
	// responseWriteSlice bayt) tamamlanmasi icin taninan sure. Geri basinc
	// zaten calisiyor (kredi); bu yalniz HIC okumayan ziyaretcinin ajan akisini
	// ve sunucu goroutine'ini suresiz tutmasini onler. 16 KB / 120 sn ~ 136 B/sn
	// alt hiz: en yavas mesru baglantinin cok altinda.
	responseWriteStallTimeout = 120 * time.Second
)

// responseWriteSlice, stallWriter'in tek deadline altinda yazdigi en buyuk parca.
const responseWriteSlice = 16 << 10

// copyBufPool, yanit govdesi kopyalama tamponlari. O14: io.Copy her istekte
// yeni 32 KB tampon ayiriyordu (kucuk istekte toplam tahsisin ucte biri).
// Boyut ajan cerceve boyuyla (BodyChunkSize) ayni: cerceve basina tek
// yazim + flush.
var copyBufPool = sync.Pool{New: func() any {
	b := make([]byte, protocol.BodyChunkSize)
	return &b
}}

// copyResponseBody, src'yi dst'ye havuzlu tamponla kopyalar ve hatanin
// HANGI taraftan geldigini ayirir: rerr ajan/akis tarafi (govde yarida kesildi,
// baglam iptali), werr ziyaretci tarafi (koptu, yazim tikandi). io.Copy bu
// ayrimi yapmaz; ingress'in tepkisi ise tamamen farklidir (ajana iptal mi,
// ziyaretci baglantisini abort mu).
//
// io.CopyBuffer yerine elle dongu: dst (flushWriter/MultiWriter) ReaderFrom
// sunmaz, src (Exchange) WriterTo sunmaz; yine de kisayollarin hata kaynagini
// gizlememesi icin acikca yazildi.
func copyResponseBody(dst io.Writer, src io.Reader) (rerr, werr error) {
	bp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bp)
	buf := *bp
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, we := dst.Write(buf[:n]); we != nil {
				return nil, we
			}
		}
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return err, nil
		}
	}
}

// stallWriter, her yazimdan once ziyaretci baglantisina yazma deadline'i koyar
// ve yazim bitince kaldirir. Deadline'in yazim ARASINDA kalmamasi sarttir:
// HTTP/2'de akis deadline'i bir zamanlayicidir ve yazim olmasa da dolunca akisi
// RST eder; SSE'nin mesru uzun sessizliklerini (ajan veri yollamiyor) bozardi.
// Destek yoksa (or. httptest.ResponseRecorder) seffaftir.
type stallWriter struct {
	w      io.Writer
	rc     *http.ResponseController
	d      time.Duration
	off    bool // SetWriteDeadline desteklenmiyor
	failed bool // bir yazim hata verdi; deadline bilerek temizlenmez
}

func newStallWriter(w io.Writer, rc *http.ResponseController, d time.Duration) *stallWriter {
	return &stallWriter{w: w, rc: rc, d: d, off: rc == nil}
}

func (s *stallWriter) Write(p []byte) (int, error) {
	if s.off {
		return s.w.Write(p)
	}
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > responseWriteSlice {
			chunk = chunk[:responseWriteSlice]
		}
		if err := s.rc.SetWriteDeadline(time.Now().Add(s.d)); err != nil {
			// Desteklenmiyor: bundan sonra deadline'siz, tek parca yaz.
			s.off = true
			n, werr := s.w.Write(p)
			return total + n, werr
		}
		n, err := s.w.Write(chunk)
		total += n
		if err != nil {
			// Zaman asimina ugrayan TLS/TCP baglantisi zaten bozuktur; deadline'i
			// kaldirmak net/http'nin kapanis flush'ini tikanan sokete takardi.
			s.failed = true
			return total, err
		}
		_ = s.rc.SetWriteDeadline(time.Time{})
		p = p[n:]
	}
	return total, nil
}

// progressBody, istek govdesini okurken kayan okuma deadline'i uygular:
// her requestBodyMinProgress baytta deadline ileri alinir; pencerede yeterli
// bayt gelmezse alttaki Read zaman asimiyla doner (timedOut). Govde sonuna
// kadar okununca (EOF) deadline KALDIRILIR: HTTP/1'de net/http govde bitince
// arka plan okumasi baslatir ve o okumanin zaman asimi istek baglamini iptal
// eder; deadline birakilsa uzun yanitlar (SSE) yarida kesilirdi.
type progressBody struct {
	r        io.ReadCloser
	rc       *http.ResponseController
	d        time.Duration
	min      int64
	sinceExt int64
	off      bool // okuma deadline'i desteklenmiyor: seffaf
	cleared  bool
	// timedOut, govde kayan deadline nedeniyle kesildiyse true. Hata turune
	// bakmak yetmez: ajana yazim zaman asimi (context.DeadlineExceeded) da
	// Timeout() true doner ve yanlislikla 408 sayilirdi.
	timedOut bool
}

func newProgressBody(body io.ReadCloser, rc *http.ResponseController, d time.Duration, min int64) *progressBody {
	b := &progressBody{r: body, rc: rc, d: d, min: min}
	if rc == nil || rc.SetReadDeadline(time.Now().Add(d)) != nil {
		b.off = true
	}
	return b
}

func (b *progressBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if b.off {
		return n, err
	}
	if n > 0 && !b.cleared {
		b.sinceExt += int64(n)
		if b.sinceExt >= b.min {
			b.sinceExt = 0
			_ = b.rc.SetReadDeadline(time.Now().Add(b.d))
		}
	}
	switch {
	case err == io.EOF:
		b.clear()
	case err != nil && isDeadlineErr(err):
		b.timedOut = true
	}
	return n, err
}

// Close, deadline'i KALDIRMAZ. Yarim birakilan govdede (zaman asimi, 413,
// ziyaretci koptu) deadline bilerek kalir: net/http kucuk kalan govdeyi
// baglantiyi yeniden kullanmak icin Close'da ve yanit basligini yazarken
// SONUNA KADAR okur; deadline'siz bu okuma 1 bayt/sn gonderen istemcide yaniti
// (408) dakikalarca bekletirdi. Tam okunan govdede deadline EOF'ta kalkmistir.
func (b *progressBody) Close() error {
	return b.r.Close()
}

func (b *progressBody) clear() {
	if b.cleared {
		return
	}
	b.cleared = true
	_ = b.rc.SetReadDeadline(time.Time{})
}

// isDeadlineErr, okuma hatasinin bir deadline/zaman asimi oldugunu soyler.
func isDeadlineErr(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}
