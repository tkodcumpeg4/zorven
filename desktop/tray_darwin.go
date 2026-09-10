//go:build darwin

package main

import (
	"github.com/tkodcumpeg4/zorven/client/agent"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// macOS'ta sistem tepsisi HENUZ DESTEKLENMIYOR.
//
// Sebep: fyne.io/systray macOS'ta ana thread'de calismak zorundadir (Cocoa
// kisiti), Wails de ana thread'i kendi olay dongusu icin kullanir. Ikisi ayni
// anda calisamaz. Dogru cozum Wails'in kendi NSStatusItem entegrasyonunu
// beklemek ya da ayri bir helper surec calistirmaktir; ikisi de MVP kapsami
// disinda birakildi.
//
// Bu dosya, tray.go'daki API'nin darwin'de de derlenmesini saglayan bos
// karsiliklari icerir. Pencere kapatilinca uygulama NORMAL sekilde kapanir.
type tray struct{}

func (a *App) startTray()             {}
func (t *tray) update(_ agent.Status) {}
func (t *tray) stop()                 {}

// trayEnabled, main.go'nun kapatma davranisini platforma gore ayarlamasi icin.
const trayEnabled = false

func (a *App) ShowWindow() {
	if a.ctx != nil {
		wailsrt.WindowShow(a.ctx)
	}
}

func (a *App) HideWindow() {
	if a.ctx != nil {
		wailsrt.WindowHide(a.ctx)
	}
}

func (a *App) QuitApp() {
	a.Disconnect()
	if a.ctx != nil {
		wailsrt.Quit(a.ctx)
	}
}
