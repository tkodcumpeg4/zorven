//go:build windows

package agent

import (
	"errors"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// Servis modu (oturum 0) kancalari: yakalama, girdi ve pano platX islevlerinden
// yardimciya yonlendirilir. Bu dosya ile capture_*.go / screen_*_windows.go
// arasindaki tek baglanti noktalaridir.

var (
	s0Once sync.Once
	s0     bool
)

// inSession0, surecin Windows servisi gibi oturum 0'da calisip calismadigi.
func inSession0() bool {
	s0Once.Do(func() {
		var sid uint32
		if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sid); err == nil && sid == 0 {
			s0 = true
		}
	})
	return s0
}

var (
	svcHelperOnce sync.Once
	svcHelper     *helperClient
)

// serviceHelper, oturum 0'da surec-genel yardimci istemcisi; aksi halde nil.
func serviceHelper() *helperClient {
	if !inSession0() {
		return nil
	}
	svcHelperOnce.Do(func() { svcHelper = newHelperClient(&winHelperConnector{}) })
	return svcHelper
}

// stopServiceHelper, ajan kapanirken yardimciyi kapatir.
func stopServiceHelper() {
	if svcHelper != nil {
		_ = svcHelper.Close()
	}
}

// screenUnsupportedErr, ekran acilamadiginda gosterilecek hata: servis modunda
// nedeni (kullanici yok / yardimci baslamadi) belirtilir.
func screenUnsupportedErr() error {
	if h := serviceHelper(); h != nil {
		if err := h.Err(); err != nil {
			return err
		}
		return errors.New(i18n.T("scr.err.no_user"))
	}
	return errScreen2Unsupported
}

// captureInit, yakalama katmanina istemci surumunu bildirir (sentinel kaydi).
func captureInit(version string) { captureVersion = version }

// captureShutdown, ajan kapanirken sentinel'i siler ve yardimciyi kapatir.
func captureShutdown() {
	shutdownCaptureGuard()
	stopServiceHelper()
}
