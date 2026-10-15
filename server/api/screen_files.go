package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Ekran oturumunda dosya aktarimi (panel <-> uzak makine). Protokol:
// shared/protocol/files.go. Uclar (admin/oturum middleware'inden gecer):
//
//	POST /api/v1/clients/{id}/screen-files?name=...            yukleme (govde = ham dosya)
//	GET  /api/v1/clients/{id}/screen-files                     izinli klasor listesi
//	GET  /api/v1/clients/{id}/screen-files/download?name=...   indirme (akis)
//
// GUVENLIK: her uc ayrica izleyiciye ozel "dosya belirteci" ister (ekran WS'inin
// {"type":"files"} mesajiyla yalniz KONTROLCU izleyiciye verilir; X-File-Token
// basligi veya ?token=). Belirtec her istekte hub'daki izleyicinin su anki
// view_only durumuyla eslestirilir: salt-izleyen biri belirtec bilse de
// reddedilir. Boylece yetki, ekran girdisi/panoyla ayni yerde (hub) zorlanir.

// fileAgent, istemci oturumunun dosya aktarimi yuzeyi (*tunnel.Session karsilar;
// testler sahte uygular). screen2Agent'in parcasi degildir: eski sahteler bozulmaz.
type fileAgent interface {
	SupportsScreenFiles() bool
	FilePut(ctx context.Context, name string, size int64, r io.Reader) (protocol.FileDone, error)
	FileGet(ctx context.Context, name string, onOpen func(size int64) error, w io.Writer) (protocol.FileEnd, error)
	FileList(ctx context.Context) (protocol.FileListResult, error)
}

// filesMsg, izleyiciye dosya aktarimi durumunu bildirir. Belirtec YALNIZ
// kontrolcu (salt-izleme olmayan) izleyiciye verilir.
func (v *screen2Viewer) filesMsg() []byte {
	h := v.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	m := map[string]any{"type": "files", "enabled": h.filesOK, "controller": h.filesOK && !v.viewOnly}
	if h.filesOK && !v.viewOnly {
		m["token"] = v.fileTok
	}
	b, _ := json.Marshal(m)
	return b
}

// Dosya belirteci reddi nedenleri.
const (
	fileDenyUnknown     = "unknown"
	fileDenyViewOnly    = "view_only"
	fileDenyUnsupported = "unsupported"
)

// fileViewer, belirtece karsilik gelen izleyiciyi bulur; reason bos degilse
// istek reddedilmelidir.
func (r *screen2Registry) fileViewer(clientID, token string) (*screen2Hub, *screen2Viewer, string) {
	r.mu.Lock()
	h := r.hubs[clientID]
	r.mu.Unlock()
	if h == nil || token == "" {
		return nil, nil, fileDenyUnknown
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, fileDenyUnknown
	}
	for v := range h.viewers {
		if subtle.ConstantTimeCompare([]byte(v.fileTok), []byte(token)) == 1 {
			switch {
			case !h.filesOK:
				return h, v, fileDenyUnsupported
			case v.viewOnly:
				return h, v, fileDenyViewOnly
			}
			return h, v, ""
		}
	}
	return nil, nil, fileDenyUnknown
}

// fileErrStatus, aktarim hata kodunu HTTP durumuna cevirir.
func fileErrStatus(code string) int {
	switch code {
	case protocol.FileErrBadName:
		return http.StatusBadRequest
	case protocol.FileErrTooLarge:
		return http.StatusRequestEntityTooLarge
	case protocol.FileErrNotFound:
		return http.StatusNotFound
	case protocol.FileErrBusy:
		return http.StatusTooManyRequests
	case protocol.FileErrDisabled, protocol.FileErrViewOnly:
		return http.StatusForbidden
	case protocol.FileErrUnsupported:
		return http.StatusNotImplemented
	case protocol.FileErrChecksum:
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadGateway
}

// fileErrParts, hatadan (kod, mesaj) cikarir.
func fileErrParts(err error) (string, string) {
	var f *tunnel.FileError
	if errors.As(err, &f) {
		return f.Code, f.Message
	}
	return protocol.FileErrIO, "aktarim basarisiz"
}

// authorizeScreenFiles ortak yetkiyi denetler: kiraci, istemci sahipligi, uzak
// erisim politikasi, belirtec/izleyici ve kontrolcu olma. Basarida hub ajanini
// ve kimlikleri doner.
func (s *Server) authorizeScreenFiles(w http.ResponseWriter, r *http.Request) (clientID, tenantID string, fa fileAgent, ok bool) {
	clientID = r.PathValue("id")
	tenantID, found := s.tenantFor(r)
	if !found {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.requireRemoteAccess(w, r, tenantID, clientID) {
		return
	}
	if _, err := s.Store.GetClient(r.Context(), tenantID, clientID); err != nil {
		writeJSONError(w, http.StatusNotFound, "client_not_found", "istemci bulunamadi")
		return
	}
	token := r.Header.Get("X-File-Token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	hub, _, reason := s.screen2Hubs.fileViewer(clientID, token)
	switch reason {
	case "":
	case fileDenyViewOnly:
		s.auditFor(r, tenantID, "screen.file_denied", clientID, "reason=view_only")
		writeJSONError(w, http.StatusForbidden, "view_only", "salt-izleme modunda dosya aktarilamaz")
		return
	case fileDenyUnsupported:
		writeJSONError(w, http.StatusNotImplemented, "unsupported", "istemci dosya aktarimini desteklemiyor")
		return
	default:
		writeJSONError(w, http.StatusForbidden, "invalid_file_token", "gecersiz dosya belirteci; ekrani yeniden acin")
		return
	}
	agent, isFA := hub.agent.(fileAgent)
	if !isFA || !agent.SupportsScreenFiles() {
		writeJSONError(w, http.StatusNotImplemented, "unsupported", "istemci dosya aktarimini desteklemiyor")
		return
	}
	return clientID, tenantID, agent, true
}

func writeFileError(w http.ResponseWriter, err error) {
	code, msg := fileErrParts(err)
	writeJSONError(w, fileErrStatus(code), code, msg)
}

// screenFilePut, POST /api/v1/clients/{id}/screen-files — panelden uzak makineye.
func (s *Server) screenFilePut(w http.ResponseWriter, r *http.Request) {
	clientID, tenantID, fa, ok := s.authorizeScreenFiles(w, r)
	if !ok {
		return
	}
	name, err := protocol.SanitizeFileName(r.URL.Query().Get("name"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, protocol.FileErrBadName, "gecersiz dosya adi")
		return
	}
	size := r.ContentLength
	if size < 0 {
		writeJSONError(w, http.StatusLengthRequired, "length_required", "Content-Length gerekli")
		return
	}
	if size > protocol.MaxFileSize {
		writeJSONError(w, http.StatusRequestEntityTooLarge, protocol.FileErrTooLarge, "dosya boyutu siniri asildi")
		return
	}
	done, err := fa.FilePut(r.Context(), name, size, r.Body)
	if err != nil {
		code, _ := fileErrParts(err)
		s.auditFor(r, tenantID, "screen.file_upload_failed", clientID,
			fmt.Sprintf("name=%s size=%d code=%s", strconv.Quote(name), size, code))
		if r.Context().Err() != nil {
			return // tarayici vazgecti
		}
		writeFileError(w, err)
		return
	}
	s.auditFor(r, tenantID, "screen.file_upload", clientID,
		fmt.Sprintf("name=%s saved_as=%s size=%d sha256=%s", strconv.Quote(name), strconv.Quote(done.Name), done.Size, done.SHA256))
	writeJSON(w, http.StatusOK, map[string]any{"name": done.Name, "size": done.Size, "sha256": done.SHA256})
}

// screenFileList, GET /api/v1/clients/{id}/screen-files.
func (s *Server) screenFileList(w http.ResponseWriter, r *http.Request) {
	_, _, fa, ok := s.authorizeScreenFiles(w, r)
	if !ok {
		return
	}
	res, err := fa.FileList(r.Context())
	if err != nil {
		writeFileError(w, err)
		return
	}
	files := res.Files
	if files == nil {
		files = []protocol.FileInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": res.Dir, "files": files})
}

// screenFileDownload, GET /api/v1/clients/{id}/screen-files/download?name= —
// uzak makineden panele AKIS (bellekte birikmez; tarayici yavassa istemci bekler).
func (s *Server) screenFileDownload(w http.ResponseWriter, r *http.Request) {
	clientID, tenantID, fa, ok := s.authorizeScreenFiles(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if protocol.ValidExistingFileName(name) != nil {
		writeJSONError(w, http.StatusBadRequest, protocol.FileErrBadName, "gecersiz dosya adi")
		return
	}
	started := false
	var total int64
	end, err := fa.FileGet(r.Context(), name, func(size int64) error {
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Length", strconv.FormatInt(size, 10))
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		started = true
		total = size
		return nil
	}, w)
	if err != nil {
		code, _ := fileErrParts(err)
		s.auditFor(r, tenantID, "screen.file_download_failed", clientID,
			fmt.Sprintf("name=%s code=%s", strconv.Quote(name), code))
		if started {
			// Basliklar gitti: tarayicinin basarisiz sayması icin baglanti kesilir
			// (checksum/boyut uyusmazligi dahil; yarim dosya "tamam" gorunmez).
			panic(http.ErrAbortHandler)
		}
		if r.Context().Err() == nil {
			writeFileError(w, err)
		}
		return
	}
	s.auditFor(r, tenantID, "screen.file_download", clientID,
		fmt.Sprintf("name=%s size=%d sha256=%s", strconv.Quote(name), total, end.SHA256))
}
