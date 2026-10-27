package agent

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// screen2 boru hatti.
//
// Eski dongu tek goroutine'de sirali yakaliyor -> olcekliyor -> fark ->
// kodluyor -> gonderiyordu; kare suresi asamalarin TOPLAMIydi. Simdi iki asama:
//
//	A (yakalama, OS thread'ine bagli): yakala -> olcekle -> karo farki -> is
//	B (gonderici): karolari paralel kodla -> paketle -> yaz -> uyarlama
//
// Aralarinda derinligi 1 "en son kare kazanir" yuvasi var (s2Slot). B mesgulken
// A yeni is koyarsa bekleyen is silinmez, BIRLESTIRILIR: eski isin kirli
// karolari yenisinin maskesine OR'lanir, anahtar bayragi korunur. Boylece
// ara kareler atilir ama hicbir karo guncellemesi kaybolmaz ve kuyruk birikmez.

// s2LayerJob, bir katmanin (ya da katmansiz akisin: layer=-1) gonderilecek isi.
type s2LayerJob struct {
	layer   int
	frame   *image.RGBA
	dirty   []bool
	tx, ty  int
	key     bool
	quality int // katman spec kalitesi (0 => denetleyici kalitesi)
}

type s2Job struct{ layers []s2LayerJob }

// s2Slot, derinligi 1 en-son-kazanir yuvasi.
type s2Slot struct {
	mu      sync.Mutex
	job     *s2Job
	wake    chan struct{}
	dropped int // birlestirilen (atilan) is sayisi
}

func newS2Slot() *s2Slot { return &s2Slot{wake: make(chan struct{}, 1)} }

func (q *s2Slot) droppedCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// put, isi yuvaya koyar; bekleyen is varsa birlestirir.
func (q *s2Slot) put(j *s2Job) {
	q.mu.Lock()
	if q.job != nil {
		mergeJobs(q.job, j)
		q.dropped++
	}
	q.job = j
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// take, is gelene veya ctx bitene kadar bekler.
func (q *s2Slot) take(ctx context.Context) (*s2Job, bool) {
	for {
		q.mu.Lock()
		j := q.job
		q.job = nil
		q.mu.Unlock()
		if j != nil {
			return j, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-q.wake:
		}
	}
}

// mergeJobs, gonderilmemis eski isi (old) yeni isin (nw) icine katar: ayni
// katmanda kirli maskeler OR'lanir ve anahtar bayragi korunur; yenide
// bulunmayan katmanlar eski halleriyle tasinir (kareleri o katmanin son
// durumudur: yeni karede degisiklik yoktu).
func mergeJobs(old, nw *s2Job) {
	for _, o := range old.layers {
		found := false
		for i := range nw.layers {
			n := &nw.layers[i]
			if n.layer != o.layer {
				continue
			}
			found = true
			n.key = n.key || o.key
			if !n.key && len(n.dirty) == len(o.dirty) {
				for k, d := range o.dirty {
					if d {
						n.dirty[k] = true
					}
				}
			} else if !n.key {
				n.key = true // boyut degisti: guvenli tarafta kal
			}
			break
		}
		if !found {
			nw.layers = append(nw.layers, o)
		}
	}
}

// run, ctx iptal olana veya kalici hata olusana kadar calisir. Capture hatasi
// bir kez yeniden denenir (onAttach ile). Yakalama bu goroutine'de (OS
// thread'ine bagli) kalir; kodlama+gonderme ayri goroutine'dedir.
func (s *screen2Session) run(ctx context.Context) error {
	if s.cap.NumDisplays() == 0 {
		return errScreen2Unsupported
	}
	if s.display < 0 || s.display >= s.cap.NumDisplays() {
		s.display = 0
	}
	s.notifyDisplay()

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	slot := newS2Slot()
	done := make(chan error, 1)
	go func() {
		err := s.sendLoop(ctx2, slot)
		if err != nil {
			cancel()
		}
		done <- err
	}()
	if s.onStats != nil {
		go s.statsLoop(ctx2, slot)
	}
	cerr := s.captureLoop(ctx2, slot)
	cancel()
	if c, ok := s.cap.(io.Closer); ok {
		c.Close()
	}
	serr := <-done
	if serr != nil {
		return serr
	}
	return cerr
}

// captureLoop (A asamasi): yakala, olcekle, karo farki bul, isi yuvaya koy.
func (s *screen2Session) captureLoop(ctx context.Context, slot *s2Slot) error {
	var layers []*layerState
	if !s.layered {
		layers = []*layerState{{spec: protocol.Screen2LayerSpec{ID: -1}, needKey: true}}
	}
	next := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	if s.vid != nil {
		go s.vid.encodeLoop(ctx) // H.264 modu (screen2_video.go)
	}

	for {
		if d := time.Until(next); d > 0 {
			timer.Reset(d)
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
		} else if ctx.Err() != nil {
			return nil
		}
		start := time.Now()

		select {
		case p := <-s.setCh:
			if s.applyParams(p) {
				s.notifyDisplay()
				for _, st := range layers {
					st.prev, st.needKey = nil, true
				}
			}
		default:
		}
		if s.keyReq.Swap(false) {
			if s.vid != nil {
				s.vid.onKeyReq()
			}
			for _, st := range layers {
				st.needKey = true
			}
		}
		if s.layered {
			layers = applyLayerUpdate(layers, s.takeLayerUpdate())
			if len(layers) == 0 {
				next = start.Add(screen2IdleTick)
				continue
			}
		}

		s.setLayerInfo(layers)
		// Katman basina farkli fps: hicbir katman vadesinde degilse yakalama atlanir.
		fast := s.pace(layers, start)
		if !anyLayerDue(layers, start, fast) {
			next = start.Add(fast)
			if now := time.Now(); next.Before(now) {
				next = now
			}
			continue
		}

		t0 := time.Now()
		img, hint, err := s.captureOnce()
		if err != nil {
			if s.onAttach != nil {
				s.onAttach()
			}
			if img, hint, err = s.captureOnce(); err != nil {
				return fmt.Errorf("ekran yakalanamadi: %w", err)
			}
		}
		s.met.observe(stCapture, time.Since(t0))
		if img == nil {
			// Ipucu: onceki yakalamadan beri degisim yok.
			needKey := false
			for _, st := range layers {
				needKey = needKey || st.needKey
			}
			if s.vid != nil && s.vid.idle(start) == vidLeave {
				for _, st := range layers {
					st.prev, st.needKey = nil, true // videodan karoya: tam kare
				}
				needKey = true
			}
			if !needKey || s.lastSrc == nil {
				s.met.addIdle()
				next = start.Add(s.pace(layers, time.Now()))
				if now := time.Now(); next.Before(now) {
					next = now
				}
				continue
			}
			img = s.lastSrc // anahtar kare: son goruntuyle (ipucu bos => fark yok)
		}
		s.lastSrc = img

		job := &s2Job{}
		var included []*layerState
		counted := false
		for i, st := range layers {
			if !layerDue(st, start, fast) {
				// Bu katmanin vadesi gelmedi: onceki karesi (prev) korunur, degisiklikler
				// bir sonraki vadede tek farkta birlesir (karo guncellemesi kaybolmaz).
				continue
			}
			width := st.width
			if !s.layered {
				width = s.maxWidth
			}
			t1 := time.Now()
			frame := st.scaler.ScaleHinted(img, width, hint)
			s.met.observe(stScale, time.Since(t1))
			if frame.Rect.Dx() > 65535 || frame.Rect.Dy() > 65535 {
				return errors.New("ekran cozunurlugu screen2 icin cok buyuk")
			}
			if st.prev != nil && (st.prev.Rect.Dx() != frame.Rect.Dx() || st.prev.Rect.Dy() != frame.Rect.Dy()) {
				st.needKey = true
			}
			if st.needKey {
				st.prev = nil
			}
			t2 := time.Now()
			dirty, tx, ty := diffFrame(st.prev, frame, hint, img.Rect.Dx(), img.Rect.Dy()) // prev nil => hepsi kirli
			s.met.observe(stDiff, time.Since(t2))
			st.prev = frame
			st.nextDue = advanceDue(st.nextDue, start, s.ctl.LayerInterval(st.spec.ID, start))
			if i == 0 {
				s.canvas.Store(uint64(frame.Rect.Dx())<<32 | uint64(frame.Rect.Dy()))
			}
			if !counted {
				counted = true
				s.met.addCaptured(img.Rect.Dx(), img.Rect.Dy(), frame.Rect.Dx(), frame.Rect.Dy())
			}
			if s.vid != nil {
				// H.264 modu: katman 0 karesi video denetleyicisine gider.
				act := vidNone
				if i == 0 {
					act = s.vid.frame(frame, dirtyRatio(dirty), start)
				} else if s.vid.active {
					act = vidSkip
				}
				if act == vidLeave {
					for _, l := range layers {
						l.prev, l.needKey = nil, true
					}
					continue
				}
				if act == vidSkip || (act == vidNone && s.vid.active) {
					st.needKey = false // video bu kareyi kapsar
					continue
				}
			}
			if !st.needKey && !anyTrue(dirty) {
				continue
			}
			job.layers = append(job.layers, s2LayerJob{
				layer: st.spec.ID, frame: frame, dirty: dirty, tx: tx, ty: ty,
				key: st.needKey, quality: st.spec.Quality,
			})
			included = append(included, st)
		}
		if len(job.layers) > 0 {
			for _, st := range included {
				st.needKey = false
			}
			slot.put(job)
		}

		next = start.Add(s.pace(layers, time.Now()))
		if now := time.Now(); next.Before(now) {
			next = now // yetisemiyoruz: birikmis kareleri yakalamaya calisma
		}
	}
}

// captureOnce, yakalayicinin ipucu yolunu (varsa) ya da duz Capture'i cagirir.
// img==nil (hint.ok) => degisim yok.
func (s *screen2Session) captureOnce() (*image.RGBA, srcHint, error) {
	if hc, ok := s.cap.(hintCapturer); ok {
		img, rects, hinted, err := hc.CaptureHinted(s.display)
		if err != nil {
			return nil, srcHint{}, err
		}
		if hinted {
			if len(rects) == 0 {
				return nil, srcHint{ok: true}, nil
			}
			if img != nil {
				return img, srcHint{ok: true, rects: rects}, nil
			}
		}
		if img == nil {
			return nil, srcHint{}, errors.New("yakalayici goruntu dondurmedi")
		}
		return img, srcHint{}, nil
	}
	img, err := s.cap.Capture(s.display)
	return img, srcHint{}, err
}

func anyTrue(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
}

// sendLoop (B asamasi): isleri alir, paralel kodlar, paketler, gonderir.
func (s *screen2Session) sendLoop(ctx context.Context, slot *s2Slot) error {
	workers := screen2EncodeWorkers()
	for {
		job, ok := slot.take(ctx)
		if !ok {
			return nil
		}
		var wrote time.Duration
		sent := false
		for _, lj := range job.layers {
			frame := lj.frame
			w, h := frame.Rect.Dx(), frame.Rect.Dy()
			var rects []image.Rectangle
			switch {
			case lj.layer < 0 && lj.key:
				rects = splitRect(frame.Rect, screen2MaxRectDim)
			case lj.layer < 0:
				for _, r := range mergeDirty(lj.dirty, lj.tx, lj.ty, w, h) {
					rects = append(rects, splitRect(r, screen2MaxRectDim)...)
				}
			default:
				dirty := lj.dirty
				if lj.key {
					dirty = make([]bool, lj.tx*lj.ty)
					for i := range dirty {
						dirty[i] = true
					}
				}
				rects = layerTileRects(dirty, lj.tx, lj.ty, w, h)
			}
			if len(rects) == 0 {
				continue
			}
			sq := 0 // katmansiz akis: tavan yok (denetleyici kalitesi)
			if lj.layer >= 0 {
				sq = lj.quality
				if sq <= 0 || sq > 100 {
					sq = 70
				}
			}
			q := s.ctl.LayerQuality(lj.layer, sq, time.Now())
			te := time.Now()
			wire, err := encodeRectsParallel(s.enc, frame, rects, q, workers)
			s.met.observe(stEncode, time.Since(te))
			if err != nil {
				return fmt.Errorf("kare kodlanamadi: %w", err)
			}
			var msgs [][]byte
			tp := time.Now()
			if lj.layer < 0 {
				msgs, err = protocol.PackScreen2(s.streamID, s.seq, lj.key, uint16(w), uint16(h), wire, 0)
			} else {
				msgs, err = protocol.PackScreen2Layer(s.streamID, s.seq, lj.key, uint8(lj.layer), uint16(w), uint16(h), wire, 0)
			}
			s.met.observe(stPack, time.Since(tp))
			if err != nil {
				return fmt.Errorf("kare paketlenemedi: %w", err)
			}
			s.seq++
			wstart := time.Now()
			var n int64
			for _, m := range msgs {
				if err := s.send(ctx, m); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				n += int64(len(m))
			}
			wrote += time.Since(wstart)
			s.met.observe(stWrite, time.Since(wstart))
			s.met.addSent(n)
			sent = true
			s.statMu.Lock()
			s.stats.Frames++
			if lj.key {
				s.stats.Keyframes++
			}
			s.stats.Bytes += n
			s.statMu.Unlock()
		}
		if sent {
			s.ctl.Observe(wrote)
		}
	}
}

// pace, yakalama turunun araligi: etkin katmanlarin en kisa kare araligi (en
// hizli katman yakalamayi belirler; yavas katmanlar vadesine kadar atlanir).
// Katman yoksa genel denetleyici araligi.
func (s *screen2Session) pace(layers []*layerState, now time.Time) time.Duration {
	if len(layers) == 0 {
		return s.ctl.Interval()
	}
	d := time.Duration(0)
	for _, st := range layers {
		if iv := s.ctl.LayerInterval(st.spec.ID, now); d == 0 || iv < d {
			d = iv
		}
	}
	return d
}

// layerDue, katmanin bu turda islenip islenmeyecegi. Anahtar kare bekleyen ya
// da henuz hic islenmemis katman hep vadesindedir; aksi halde vadesine en hizli
// katman araliginin yarisi kadar yaklasmis olmak yeter (tur titresimi payi).
func layerDue(st *layerState, now time.Time, fast time.Duration) bool {
	if st.needKey || st.nextDue.IsZero() {
		return true
	}
	return !now.Add(fast / 2).Before(st.nextDue)
}

func anyLayerDue(layers []*layerState, now time.Time, fast time.Duration) bool {
	for _, st := range layers {
		if layerDue(st, now, fast) {
			return true
		}
	}
	return false
}

// advanceDue, sonraki vadeyi hesaplar: duzgun tempo icin eski vadeye aralik
// eklenir; geride kalindiysa (ya da ilk kez) simdiden baslar.
func advanceDue(prev, now time.Time, iv time.Duration) time.Time {
	if prev.IsZero() || now.Sub(prev) > iv {
		return now.Add(iv)
	}
	return prev.Add(iv)
}
