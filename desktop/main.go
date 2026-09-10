package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "Zorven",
		Width:  460,
		Height: 820,
		// Dar panel varsayilan; Loglar acilinca pencere genisler (bkz. ResizeWindow).
		MinWidth:  420,
		MinHeight: 600,
		MaxWidth:  1400,
		MaxHeight: 1000,

		AssetServer: &assetserver.Options{Assets: assets},

		// Pencere arka plani tasarim token'imizla ayni (#09090b):
		// aksi halde acilista beyaz bir kare gorunur.
		BackgroundColour: &options.RGBA{R: 9, G: 9, B: 11, A: 1},

		OnStartup: app.startup,
		Bind:      []any{app},

		// Kapatma tusu uygulamayi KAPATMAZ, tepsiye indirir.
		//
		// Tunel istemcisi arka planda calismaya devam etmeli; pencereyi
		// kapatmak "tuneli kes" anlamina gelmemeli. Kullanici gercekten
		// cikmak isterse tepsi menusundeki "Cikis"i kullanir.
		//
		// true donmek Wails'e kapanmayi IPTAL ETTIRIR.
		// macOS'ta tepsi olmadigi icin (bkz. tray_darwin.go) normal kapanir —
		// aksi halde uygulama kapatilamaz hale gelirdi.
		OnBeforeClose: func(_ context.Context) bool {
			// Tepsi yoksa (macOS) veya "tepsiye gizle" ayari kapaliysa normal kapan.
			if !trayEnabled || !app.hideToTrayEnabled() {
				return false
			}
			app.HideWindow()
			return true
		},

		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
