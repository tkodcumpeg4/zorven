package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ekran oturumu dosya aktarimi (istemci tarafi). Protokol: shared/protocol/files.go.
//
// GUVENLIK:
//   - Tum dosya islemleri TEK izinli klasorde (varsayilan <Indirilenler>/Zorven)
//     yapilir; istemci yol KABUL ETMEZ, yalniz dosya adi (yol ayiricili ad reddedilir).
//   - Yukleme asla mevcut dosyanin ustune yazmaz (" (n)" soneki, O_EXCL).
//   - Indirme yalniz duz dosyalara izin verir (sembolik baglanti/aygit reddedilir).
//   - Es zamanli aktarim ve dosya boyutu sinirlidir.
//   - --no-file-transfer ile ozellik hic ilan edilmez.

// fileSink, fileManager'in kullandigi gonderim yuzeyi (*clientSession karsilar).
type fileSink interface {
	sendControl(ctx context.Context, v any, t protocol.MessageType) error
	sendBinary(ctx context.Context, b []byte) error
}

// defaultFileDir, izinli klasorun varsayilani: <ev>/Downloads/Zorven.
func defaultFileDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "Zorven")
	}
	return filepath.Join(home, "Downloads", "Zorven")
}

// fileEv, bir yukleme isci goroutine'ine giden olay.
type fileEv struct {
	data []byte
	end  *protocol.FileEnd
}

type agentXfer struct {
	id     uint64
	dir    string
	cancel context.CancelFunc
	ev     chan fileEv          // put: veri + bitis
	credit *protocol.FileCredit // get: gonderim kredisi
}

type fileManager struct {
	sink    fileSink
	log     *slog.Logger
	dir     string // izinli klasor
	maxSize int64
	rate    int64 // bayt/sn gonderim tavani (indirme)

	mu    sync.Mutex
	xfers map[uint64]*agentXfer
}

func newFileManager(sink fileSink, log *slog.Logger, dir string, maxSize int64) *fileManager {
	if dir == "" {
		dir = defaultFileDir()
	}
	if maxSize <= 0 || maxSize > protocol.MaxFileSize {
		maxSize = protocol.MaxFileSize
	}
	return &fileManager{sink: sink, log: log, dir: dir, maxSize: maxSize,
		rate: protocol.FileRateBytesPerSec, xfers: map[uint64]*agentXfer{}}
}

// closeAll, oturum biterken tum aktarimlari iptal eder (yarim dosyalar silinir).
func (fm *fileManager) closeAll() {
	fm.mu.Lock()
	xs := make([]*agentXfer, 0, len(fm.xfers))
	for _, x := range fm.xfers {
		xs = append(xs, x)
	}
	fm.mu.Unlock()
	for _, x := range xs {
		x.cancel()
	}
}

func (fm *fileManager) finish(id uint64) {
	fm.mu.Lock()
	delete(fm.xfers, id)
	fm.mu.Unlock()
}

func (fm *fileManager) get(id uint64) *agentXfer {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return fm.xfers[id]
}

func (fm *fileManager) nack(ctx context.Context, id uint64, code, msg string) {
	_ = fm.sink.sendControl(ctx, protocol.FileOpenAck{
		Type: protocol.TypeFileOpenAck, XferID: id, Code: code, Message: msg,
	}, protocol.TypeFileOpenAck)
}

func (fm *fileManager) abort(ctx context.Context, id uint64, code, msg string) {
	_ = fm.sink.sendControl(ctx, protocol.FileAbort{
		Type: protocol.TypeFileAbort, XferID: id, Code: code, Message: msg,
	}, protocol.TypeFileAbort)
}

// register, es zamanli sinir icinde yeni aktarim kaydeder.
func (fm *fileManager) register(x *agentXfer) string {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	if _, dup := fm.xfers[x.id]; dup {
		return protocol.FileErrProtocol
	}
	if len(fm.xfers) >= protocol.MaxFileXfers {
		return protocol.FileErrBusy
	}
	fm.xfers[x.id] = x
	return ""
}

// open, file_open mesajini isler (okuma dongusunu bloklamaz: isci goroutine acar).
func (fm *fileManager) open(ctx context.Context, m protocol.FileOpen) {
	switch m.Dir {
	case protocol.FileDirPut:
		fm.openPut(ctx, m)
	case protocol.FileDirGet:
		fm.openGet(ctx, m)
	default:
		fm.nack(ctx, m.XferID, protocol.FileErrProtocol, "bilinmeyen yon")
	}
}

// createUnique, dir icinde name'i ASLA ezmeden olusturur (" (n)" soneki).
func createUnique(dir, name string) (*os.File, string, error) {
	for n := 0; n < 1000; n++ {
		cand := protocol.UniqueFileName(name, n)
		f, err := os.OpenFile(filepath.Join(dir, cand), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, cand, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("benzersiz ad bulunamadi")
}

func (fm *fileManager) openPut(ctx context.Context, m protocol.FileOpen) {
	name, err := protocol.SanitizeFileName(m.Name)
	if err != nil {
		fm.nack(ctx, m.XferID, protocol.FileErrBadName, "gecersiz dosya adi")
		return
	}
	if m.Size < 0 || m.Size > fm.maxSize {
		fm.nack(ctx, m.XferID, protocol.FileErrTooLarge, fmt.Sprintf("dosya boyutu siniri: %d bayt", fm.maxSize))
		return
	}
	xctx, cancel := context.WithCancel(ctx)
	x := &agentXfer{id: m.XferID, dir: protocol.FileDirPut, cancel: cancel, ev: make(chan fileEv, 64)}
	if code := fm.register(x); code != "" {
		cancel()
		fm.nack(ctx, m.XferID, code, "es zamanli aktarim siniri")
		return
	}
	if err := os.MkdirAll(fm.dir, 0o755); err != nil {
		fm.finish(x.id)
		cancel()
		fm.nack(ctx, m.XferID, protocol.FileErrIO, "klasor olusturulamadi")
		return
	}
	f, final, err := createUnique(fm.dir, name)
	if err != nil {
		fm.finish(x.id)
		cancel()
		fm.nack(ctx, m.XferID, protocol.FileErrIO, "dosya olusturulamadi")
		return
	}
	_ = fm.sink.sendControl(ctx, protocol.FileOpenAck{
		Type: protocol.TypeFileOpenAck, XferID: m.XferID, OK: true, Name: final,
	}, protocol.TypeFileOpenAck)
	fm.log.Info("dosya yukleme basladi", "xfer", m.XferID, "ad", final, "boyut", m.Size)
	go fm.runPut(xctx, x, f, final, m.Size)
}

// runPut, gelen veriyi diske yazar; bitiste boyut ve sha256'yi dogrular.
// Basarisizlikta/iptalde yarim dosya SILINIR.
func (fm *fileManager) runPut(ctx context.Context, x *agentXfer, f *os.File, name string, size int64) {
	path := filepath.Join(fm.dir, name)
	defer fm.finish(x.id)
	defer x.cancel()
	h := sha256.New()
	var acker protocol.FileAcker
	var got int64
	fail := func(notify bool, code, msg string) {
		f.Close()
		os.Remove(path)
		if notify {
			fm.abort(context.Background(), x.id, code, msg)
		}
		fm.log.Warn("dosya yukleme basarisiz", "xfer", x.id, "ad", name, "kod", code)
	}
	for {
		select {
		case <-ctx.Done():
			fail(false, protocol.FileErrCanceled, "")
			return
		case ev := <-x.ev:
			if ev.data != nil {
				got += int64(len(ev.data))
				if got > size {
					fail(true, protocol.FileErrTooLarge, "bildirilen boyut asildi")
					return
				}
				if _, err := f.Write(ev.data); err != nil {
					fail(true, protocol.FileErrIO, "diske yazilamadi")
					return
				}
				h.Write(ev.data)
				if tot, ok := acker.Consume(len(ev.data)); ok {
					_ = fm.sink.sendControl(ctx, protocol.FileAck{Type: protocol.TypeFileAck, XferID: x.id, Bytes: tot}, protocol.TypeFileAck)
				}
				continue
			}
			if ev.end == nil {
				continue
			}
			sum := hex.EncodeToString(h.Sum(nil))
			done := protocol.FileDone{Type: protocol.TypeFileDone, XferID: x.id, Name: name, Size: got, SHA256: sum}
			if got != size || ev.end.Size != got || ev.end.SHA256 != sum {
				f.Close()
				os.Remove(path)
				done.Code, done.Message, done.Name = protocol.FileErrChecksum, "boyut veya sha256 uyusmuyor", ""
				_ = fm.sink.sendControl(context.Background(), done, protocol.TypeFileDone)
				fm.log.Warn("dosya sha256 uyusmazligi", "xfer", x.id, "ad", name)
				return
			}
			if err := f.Close(); err != nil {
				os.Remove(path)
				done.Code, done.Message, done.Name = protocol.FileErrIO, "dosya kapatilamadi", ""
				_ = fm.sink.sendControl(context.Background(), done, protocol.TypeFileDone)
				return
			}
			done.OK = true
			_ = fm.sink.sendControl(context.Background(), done, protocol.TypeFileDone)
			fm.log.Info("dosya yuklendi", "xfer", x.id, "ad", name, "boyut", got)
			return
		}
	}
}

func (fm *fileManager) openGet(ctx context.Context, m protocol.FileOpen) {
	if protocol.ValidExistingFileName(m.Name) != nil {
		fm.nack(ctx, m.XferID, protocol.FileErrBadName, "gecersiz dosya adi")
		return
	}
	path := filepath.Join(fm.dir, m.Name)
	li, err := os.Lstat(path)
	if err != nil || !li.Mode().IsRegular() {
		fm.nack(ctx, m.XferID, protocol.FileErrNotFound, "dosya bulunamadi")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fm.nack(ctx, m.XferID, protocol.FileErrNotFound, "dosya acilamadi")
		return
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(li, fi) || !fi.Mode().IsRegular() {
		f.Close()
		fm.nack(ctx, m.XferID, protocol.FileErrNotFound, "dosya bulunamadi")
		return
	}
	if fi.Size() > fm.maxSize {
		f.Close()
		fm.nack(ctx, m.XferID, protocol.FileErrTooLarge, "dosya boyutu siniri asiliyor")
		return
	}
	xctx, cancel := context.WithCancel(ctx)
	x := &agentXfer{id: m.XferID, dir: protocol.FileDirGet, cancel: cancel, credit: protocol.NewFileCredit(protocol.FileWindowBytes)}
	if code := fm.register(x); code != "" {
		f.Close()
		cancel()
		fm.nack(ctx, m.XferID, code, "es zamanli aktarim siniri")
		return
	}
	_ = fm.sink.sendControl(ctx, protocol.FileOpenAck{
		Type: protocol.TypeFileOpenAck, XferID: m.XferID, OK: true, Name: m.Name, Size: fi.Size(),
	}, protocol.TypeFileOpenAck)
	fm.log.Info("dosya indirme basladi", "xfer", m.XferID, "ad", m.Name, "boyut", fi.Size())
	go fm.runGet(xctx, x, f, fi.Size())
}

// runGet, dosyayi kredi penceresi ve hiz sinirina uyarak parca parca yollar.
func (fm *fileManager) runGet(ctx context.Context, x *agentXfer, f *os.File, size int64) {
	defer fm.finish(x.id)
	defer x.cancel()
	defer f.Close()
	rate := protocol.NewFileRate(fm.rate)
	h := sha256.New()
	buf := make([]byte, protocol.FileChunkSize)
	var sent int64
	var seq uint32
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if sent+int64(n) > size {
				fm.abort(ctx, x.id, protocol.FileErrChanged, "dosya aktarim sirasinda degisti")
				return
			}
			if x.credit.Reserve(ctx, int64(n)) != nil || rate.Wait(ctx, n) != nil {
				return // iptal / oturum bitti
			}
			fr := protocol.BodyFrame{FrameType: protocol.FrameFile, ReqID: x.id, Seq: seq, Payload: buf[:n]}
			if fm.sink.sendBinary(ctx, fr.Encode()) != nil {
				return
			}
			h.Write(buf[:n])
			sent += int64(n)
			seq++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fm.abort(ctx, x.id, protocol.FileErrIO, "dosya okunamadi")
			return
		}
	}
	if sent != size {
		fm.abort(ctx, x.id, protocol.FileErrChanged, "dosya aktarim sirasinda degisti")
		return
	}
	_ = fm.sink.sendControl(ctx, protocol.FileEnd{
		Type: protocol.TypeFileEnd, XferID: x.id, Size: sent, SHA256: hex.EncodeToString(h.Sum(nil)),
	}, protocol.TypeFileEnd)
	fm.log.Info("dosya indirme tamam", "xfer", x.id, "boyut", sent)
}

// data, FrameFile verisini yukleme iscisine verir. Kuyruk dolarsa (esik: kredi
// penceresi asildi = protokol ihlali) aktarim iptal edilir.
func (fm *fileManager) data(ctx context.Context, id uint64, payload []byte) {
	x := fm.get(id)
	if x == nil || x.ev == nil {
		return
	}
	select {
	case x.ev <- fileEv{data: append([]byte(nil), payload...)}:
	default:
		x.cancel()
		fm.abort(ctx, id, protocol.FileErrProtocol, "kredi penceresi asildi")
	}
}

func (fm *fileManager) end(ctx context.Context, m protocol.FileEnd) {
	x := fm.get(m.XferID)
	if x == nil || x.ev == nil {
		return
	}
	select {
	case x.ev <- fileEv{end: &m}:
	default:
		x.cancel()
		fm.abort(ctx, m.XferID, protocol.FileErrProtocol, "kredi penceresi asildi")
	}
}

func (fm *fileManager) ack(m protocol.FileAck) {
	x := fm.get(m.XferID)
	if x == nil || x.credit == nil {
		return
	}
	if !x.credit.Ack(m.Bytes) {
		x.cancel()
	}
}

// abortFromPeer, karsi tarafin iptali: yerel iscinin temizlemesini tetikler.
func (fm *fileManager) abortFromPeer(id uint64) {
	if x := fm.get(id); x != nil {
		x.cancel()
	}
}

// list, izinli klasordeki duz dosyalari (en fazla MaxFileListItems) listeler.
func (fm *fileManager) list(ctx context.Context, m protocol.FileList) {
	res := protocol.FileListResult{Type: protocol.TypeFileListResult, ReqID: m.ReqID, Dir: fm.dir}
	ents, err := os.ReadDir(fm.dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Klasor henuz yok: bos liste.
	case err != nil:
		res.Code, res.Message = protocol.FileErrIO, "klasor okunamadi"
	default:
		for _, e := range ents {
			if !e.Type().IsRegular() { // sembolik baglanti, klasor, aygit: yok
				continue
			}
			fi, err := e.Info()
			if err != nil || protocol.ValidExistingFileName(e.Name()) != nil {
				continue
			}
			res.Files = append(res.Files, protocol.FileInfo{Name: e.Name(), Size: fi.Size(), ModTime: fi.ModTime().Unix()})
		}
		sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].ModTime > res.Files[j].ModTime })
		if len(res.Files) > protocol.MaxFileListItems {
			res.Files = res.Files[:protocol.MaxFileListItems]
		}
	}
	_ = fm.sink.sendControl(ctx, res, protocol.TypeFileListResult)
}
