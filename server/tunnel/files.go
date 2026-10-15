package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ekran oturumu dosya aktarimi (sunucu tarafi). Protokol: shared/protocol/files.go.
// Sunucu panelin HTTP govdesi ile istemci arasinda KOPRU kurar: yukleme govdesi
// yalnizca istemci kredi verdikce okunur (tarayiciya geri basinc), indirmede
// istemci yalnizca tarayici veriyi tukettikce gonderir. Disk/bellek tamponu yok.

// FileError, istemcinin (veya sunucunun) bildirdigi aktarim hatasi.
type FileError struct {
	Code    string
	Message string
}

func (e *FileError) Error() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

// Hatalar.
var (
	ErrScreenFilesUnsupported = &FileError{Code: protocol.FileErrUnsupported, Message: "istemci dosya aktarimini desteklemiyor"}
	ErrFileBusy               = &FileError{Code: protocol.FileErrBusy, Message: "es zamanli aktarim siniri"}
	errFileShortBody          = &FileError{Code: protocol.FileErrIO, Message: "govde bildirilen boyuttan kisa"}
)

const (
	fileOpenTimeout = 15 * time.Second
	fileIdleTimeout = 60 * time.Second
	fileDoneTimeout = 60 * time.Second
)

// SupportsScreenFiles, istemci dosya aktarimini pazarlamis mi.
func (s *Session) SupportsScreenFiles() bool { return s.screenFiles }

type srvEv struct {
	data []byte
	ctl  any // FileOpenAck | FileEnd | FileDone | FileAbort
}

type srvXfer struct {
	id     uint64
	dir    string
	ev     chan srvEv
	credit *protocol.FileCredit
	done   chan struct{} // oturum/aktarim hatasiyla kapanir
	once   sync.Once
	err    error
}

func (x *srvXfer) fail(err error) {
	x.once.Do(func() {
		x.err = err
		if x.credit != nil {
			x.credit.Fail(err)
		}
		close(x.done)
	})
}

func (s *Session) newXfer(dir string) (*srvXfer, error) {
	if !s.screenFiles {
		return nil, ErrScreenFilesUnsupported
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if s.files == nil {
		s.files = map[uint64]*srvXfer{}
	}
	if len(s.files) >= protocol.MaxFileXfers {
		return nil, ErrFileBusy
	}
	x := &srvXfer{id: s.fileSeq.Add(1), dir: dir, ev: make(chan srvEv, 128), done: make(chan struct{})}
	if dir == protocol.FileDirPut {
		x.credit = protocol.NewFileCredit(protocol.FileWindowBytes)
	}
	s.files[x.id] = x
	return x, nil
}

func (s *Session) dropXfer(x *srvXfer) {
	s.fileMu.Lock()
	delete(s.files, x.id)
	s.fileMu.Unlock()
}

func (s *Session) lookupXfer(id uint64) *srvXfer {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	return s.files[id]
}

// closeAllFiles, oturum biterken bekleyen aktarim ve listeleri sonlandirir.
func (s *Session) closeAllFiles() {
	s.fileMu.Lock()
	xs := make([]*srvXfer, 0, len(s.files))
	for _, x := range s.files {
		xs = append(xs, x)
	}
	s.fileMu.Unlock()
	for _, x := range xs {
		x.fail(errors.New("istemci baglantisi koptu"))
	}
}

// push, olayi okuma dongusunu BLOKLAMADAN aktarima verir; kuyruk tasarsa
// (protokol ihlali) aktarim hata ile sonlandirilir.
func (s *Session) pushXfer(x *srvXfer, ev srvEv) {
	select {
	case x.ev <- ev:
	default:
		x.fail(&FileError{Code: protocol.FileErrProtocol, Message: "olay kuyrugu tasti"})
		go s.sendFileAbort(x.id, protocol.FileErrProtocol, "kuyruk tasti")
	}
}

// routeFileData, istemciden gelen FrameFile'i (indirme verisi) iletir.
func (s *Session) routeFileData(f protocol.BodyFrame) {
	if !s.screenFiles {
		return
	}
	x := s.lookupXfer(f.ReqID)
	if x == nil || x.dir != protocol.FileDirGet {
		return
	}
	s.pushXfer(x, srvEv{data: append([]byte(nil), f.Payload...)})
}

// routeFileCtl, istemciden gelen dosya kontrol mesajlarini yonlendirir.
func (s *Session) routeFileCtl(t protocol.MessageType, data []byte) {
	if !s.screenFiles {
		return
	}
	switch t {
	case protocol.TypeFileListResult:
		var m protocol.FileListResult
		if json.Unmarshal(data, &m) != nil {
			return
		}
		s.fileMu.Lock()
		ch := s.fileList[m.ReqID]
		s.fileMu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}
		return
	}
	var h struct {
		XferID uint64 `json:"xfer_id"`
	}
	if json.Unmarshal(data, &h) != nil {
		return
	}
	x := s.lookupXfer(h.XferID)
	if x == nil {
		return
	}
	switch t {
	case protocol.TypeFileAck:
		var m protocol.FileAck
		if json.Unmarshal(data, &m) != nil || x.credit == nil {
			return
		}
		if !x.credit.Ack(m.Bytes) {
			x.fail(&FileError{Code: protocol.FileErrProtocol, Message: "gecersiz file_ack"})
			go s.sendFileAbort(x.id, protocol.FileErrProtocol, "gecersiz file_ack")
		}
	case protocol.TypeFileOpenAck:
		var m protocol.FileOpenAck
		if json.Unmarshal(data, &m) == nil {
			s.pushXfer(x, srvEv{ctl: m})
		}
	case protocol.TypeFileEnd:
		var m protocol.FileEnd
		if json.Unmarshal(data, &m) == nil {
			s.pushXfer(x, srvEv{ctl: m})
		}
	case protocol.TypeFileDone:
		var m protocol.FileDone
		if json.Unmarshal(data, &m) == nil {
			s.pushXfer(x, srvEv{ctl: m})
		}
	case protocol.TypeFileAbort:
		var m protocol.FileAbort
		if json.Unmarshal(data, &m) == nil {
			// Kredi beklerken uyansin; ayrica olay olarak da ilet.
			x.fail(&FileError{Code: m.Code, Message: m.Message})
		}
	}
}

func (s *Session) sendFileAbort(id uint64, code, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Send(ctx, protocol.FileAbort{Type: protocol.TypeFileAbort, XferID: id, Code: code, Message: msg}, protocol.TypeFileAbort)
}

// waitOpenAck, file_open_ack gelene kadar bekler.
func (s *Session) waitOpenAck(ctx context.Context, x *srvXfer) (protocol.FileOpenAck, error) {
	t := time.NewTimer(fileOpenTimeout)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return protocol.FileOpenAck{}, ctx.Err()
		case <-x.done:
			return protocol.FileOpenAck{}, x.err
		case <-t.C:
			return protocol.FileOpenAck{}, &FileError{Code: protocol.FileErrIO, Message: "istemci yanit vermedi"}
		case ev := <-x.ev:
			if a, ok := ev.ctl.(protocol.FileOpenAck); ok {
				if !a.OK {
					return a, &FileError{Code: a.Code, Message: a.Message}
				}
				return a, nil
			}
		}
	}
}

// FilePut, r'den okunan size baytlik icerigi istemcide name adiyla kaydettirir
// ve dogrulanmis sonucu (kaydedilen ad dahil) doner. r tam size bayt vermelidir.
func (s *Session) FilePut(ctx context.Context, name string, size int64, r io.Reader) (protocol.FileDone, error) {
	x, err := s.newXfer(protocol.FileDirPut)
	if err != nil {
		return protocol.FileDone{}, err
	}
	defer s.dropXfer(x)
	ok := false
	defer func() {
		if !ok {
			s.sendFileAbort(x.id, protocol.FileErrCanceled, "")
		}
	}()

	if err := s.Send(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: x.id,
		Dir: protocol.FileDirPut, Name: name, Size: size}, protocol.TypeFileOpen); err != nil {
		return protocol.FileDone{}, err
	}
	ack, err := s.waitOpenAck(ctx, x)
	if err != nil {
		return protocol.FileDone{}, err
	}
	_ = ack

	h := sha256.New()
	rate := protocol.NewFileRate(protocol.FileRateBytesPerSec)
	buf := make([]byte, protocol.FileChunkSize)
	var sent int64
	var seq uint32
	for sent < size {
		want := int64(len(buf))
		if size-sent < want {
			want = size - sent
		}
		n, rerr := io.ReadFull(r, buf[:want])
		if n > 0 {
			if err := x.credit.Reserve(ctx, int64(n)); err != nil {
				return protocol.FileDone{}, s.xferErr(x, err)
			}
			if err := rate.Wait(ctx, n); err != nil {
				return protocol.FileDone{}, err
			}
			if err := s.SendBinary(ctx, protocol.BodyFrame{FrameType: protocol.FrameFile, ReqID: x.id, Seq: seq, Payload: buf[:n]}); err != nil {
				return protocol.FileDone{}, err
			}
			h.Write(buf[:n])
			sent += int64(n)
			seq++
		}
		if rerr != nil {
			if sent < size {
				if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
					return protocol.FileDone{}, errFileShortBody
				}
				return protocol.FileDone{}, rerr
			}
			break
		}
	}
	if err := s.Send(ctx, protocol.FileEnd{Type: protocol.TypeFileEnd, XferID: x.id, Size: sent,
		SHA256: hex.EncodeToString(h.Sum(nil))}, protocol.TypeFileEnd); err != nil {
		return protocol.FileDone{}, err
	}
	t := time.NewTimer(fileDoneTimeout)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return protocol.FileDone{}, ctx.Err()
		case <-x.done:
			return protocol.FileDone{}, x.err
		case <-t.C:
			return protocol.FileDone{}, &FileError{Code: protocol.FileErrIO, Message: "istemci dogrulamayi tamamlamadi"}
		case ev := <-x.ev:
			if d, isDone := ev.ctl.(protocol.FileDone); isDone {
				ok = true // istemci aktarimi zaten sonlandirdi: abort gonderme
				if !d.OK {
					return d, &FileError{Code: d.Code, Message: d.Message}
				}
				return d, nil
			}
		}
	}
}

func (s *Session) xferErr(x *srvXfer, err error) error {
	select {
	case <-x.done:
		return x.err
	default:
		return err
	}
}

// FileGet, istemcideki (izinli klasorden) name dosyasini w'ye akitir. Boyut
// ogrenilince onOpen(size) cagrilir (HTTP basliklari icin); hata doner ise
// aktarim iptal edilir. Veri sonunda boyut ve sha256 dogrulanir.
func (s *Session) FileGet(ctx context.Context, name string, onOpen func(size int64) error, w io.Writer) (protocol.FileEnd, error) {
	x, err := s.newXfer(protocol.FileDirGet)
	if err != nil {
		return protocol.FileEnd{}, err
	}
	defer s.dropXfer(x)
	finished := false
	defer func() {
		if !finished {
			s.sendFileAbort(x.id, protocol.FileErrCanceled, "")
		}
	}()

	if err := s.Send(ctx, protocol.FileOpen{Type: protocol.TypeFileOpen, XferID: x.id,
		Dir: protocol.FileDirGet, Name: name}, protocol.TypeFileOpen); err != nil {
		return protocol.FileEnd{}, err
	}
	ack, err := s.waitOpenAck(ctx, x)
	if err != nil {
		return protocol.FileEnd{}, err
	}
	if onOpen != nil {
		if err := onOpen(ack.Size); err != nil {
			return protocol.FileEnd{}, err
		}
	}

	h := sha256.New()
	var acker protocol.FileAcker
	var got int64
	idle := time.NewTimer(fileIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return protocol.FileEnd{}, ctx.Err()
		case <-x.done:
			finished = true // istemci iptal etti / oturum koptu
			return protocol.FileEnd{}, x.err
		case <-idle.C:
			return protocol.FileEnd{}, &FileError{Code: protocol.FileErrIO, Message: "istemciden veri gelmedi"}
		case ev := <-x.ev:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(fileIdleTimeout)
			if ev.data != nil {
				got += int64(len(ev.data))
				if got > ack.Size {
					return protocol.FileEnd{}, &FileError{Code: protocol.FileErrChanged, Message: "boyut asildi"}
				}
				if _, err := w.Write(ev.data); err != nil {
					return protocol.FileEnd{}, err
				}
				h.Write(ev.data)
				if tot, due := acker.Consume(len(ev.data)); due {
					if err := s.Send(ctx, protocol.FileAck{Type: protocol.TypeFileAck, XferID: x.id, Bytes: tot}, protocol.TypeFileAck); err != nil {
						return protocol.FileEnd{}, err
					}
				}
				continue
			}
			if end, isEnd := ev.ctl.(protocol.FileEnd); isEnd {
				finished = true
				sum := hex.EncodeToString(h.Sum(nil))
				if end.Size != got || got != ack.Size || end.SHA256 != sum {
					return end, &FileError{Code: protocol.FileErrChecksum, Message: "boyut veya sha256 uyusmuyor"}
				}
				return end, nil
			}
		}
	}
}

// FileList, istemcinin izinli klasorundeki dosyalari listeler.
func (s *Session) FileList(ctx context.Context) (protocol.FileListResult, error) {
	if !s.screenFiles {
		return protocol.FileListResult{}, ErrScreenFilesUnsupported
	}
	id := s.fileSeq.Add(1)
	ch := make(chan protocol.FileListResult, 1)
	s.fileMu.Lock()
	if s.fileList == nil {
		s.fileList = map[uint64]chan protocol.FileListResult{}
	}
	s.fileList[id] = ch
	s.fileMu.Unlock()
	defer func() {
		s.fileMu.Lock()
		delete(s.fileList, id)
		s.fileMu.Unlock()
	}()
	if err := s.Send(ctx, protocol.FileList{Type: protocol.TypeFileList, ReqID: id}, protocol.TypeFileList); err != nil {
		return protocol.FileListResult{}, err
	}
	t := time.NewTimer(fileOpenTimeout)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return protocol.FileListResult{}, ctx.Err()
	case <-t.C:
		return protocol.FileListResult{}, &FileError{Code: protocol.FileErrIO, Message: "istemci yanit vermedi"}
	case res := <-ch:
		if res.Code != "" {
			return res, &FileError{Code: res.Code, Message: res.Message}
		}
		return res, nil
	}
}
