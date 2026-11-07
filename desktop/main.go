package main

import (
	"context"
	"embed"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// hiddenFlag: uygulamayi pencere acmadan tepsiye baslatir (otomatik baslatma).
const hiddenFlag = "--hidden"

// singleInstanceID, tek ornek kilidinin sabit kimligi (surumden bagimsiz).
const singleInstanceID = "app.zorven.desktop.7c1e4b52-3f0a-4d6e-9a58-1b2d0e6f4a93"

// parseArgs, komut satiri bayraklarini ayirir: --hidden ve --zv-wait-pid N.
func parseArgs(args []string) (hidden bool, waitPID int) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case hiddenFlag:
			hidden = true
		case waitPIDFlag:
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					waitPID = n
				}
				i++
			}
		}
	}
	return
}

func main() {
	// Windows kurulum/kaldirma/yardimci akislari (Wails baslatilmadan).
	if handled, code := handleSetupCommands(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == updateApplyFlag {
		os.Exit(runApplyUpdate(os.Args[2:]))
	}
	hidden, waitPID := parseArgs(os.Args[1:])
	// Guncelleme sonrasi yeniden baslatma: eski surec cikana kadar bekle ki tek
	// ornek kilidi eski surecte kalip yeni surecin hemen kapanmasina yol acmasin.
	if waitPID > 0 {
		waitPIDExit(waitPID, 20*time.Second)
	}
	app := NewApp(hidden && trayEnabled)

	// Acilis rengi ve baslik cubugu temasi: kayitli secim, yoksa sistem temasi.
	// Boylece pencere ilk acilista yanlis renkte parlamaz.
	theme := app.cfg.Theme
	light := theme == "light" || (theme == "" && systemPrefersLight())
	bg := &options.RGBA{R: 10, G: 10, B: 10, A: 1} // --bg (koyu)
	if light {
		bg = &options.RGBA{R: 248, G: 250, B: 252, A: 1} // --bg (acik)
	}
	winTheme := windows.SystemDefault
	switch theme {
	case "light":
		winTheme = windows.Light
	case "dark":
		winTheme = windows.Dark
	}

	err := wails.Run(&options.App{
		Title:  "Zorven",
		Width:  1040,
		Height: 680,
		// Yan menulu yerlesim: bu boyutun altinda menu simgelere daralir.
		MinWidth:  860,
		MinHeight: 560,

		// Cercevesiz: Windows baslik cubugu yok; surukleme ve pencere dugmeleri
		// arayuzun ust cubugunda (style.css --wails-draggable, app.js data-win).
		Frameless: true,

		// Otomatik baslatmada (--hidden) pencere acilmaz, tepside baslar.
		StartHidden: hidden && trayEnabled,

		AssetServer: &assetserver.Options{Assets: assets},

		// Pencere arka plani tema belirteciyle ayni (aksi halde acilista renk parlamasi).
		BackgroundColour: bg,

		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind:       []any{app},

		// Tek ornek: ikinci acilis yeni pencere acmak yerine var olani one getirir.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: singleInstanceID,
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				app.ShowWindow()
			},
		},

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
			app.toTray()
			return true
		},

		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			// Baslik cubugu uygulamanin renkleriyle (panel belirtecleri); varsayilan
			// beyaz Windows cubugu koyu arayuzle uyumsuz duruyordu.
			Theme: winTheme,
			CustomTheme: &windows.ThemeSettings{
				DarkModeTitleBar:           windows.RGB(10, 10, 10),
				DarkModeTitleBarInactive:   windows.RGB(10, 10, 10),
				DarkModeTitleText:          windows.RGB(245, 245, 245),
				DarkModeTitleTextInactive:  windows.RGB(138, 138, 138),
				DarkModeBorder:             windows.RGB(38, 38, 38),
				DarkModeBorderInactive:     windows.RGB(38, 38, 38),
				LightModeTitleBar:          windows.RGB(248, 250, 252),
				LightModeTitleBarInactive:  windows.RGB(248, 250, 252),
				LightModeTitleText:         windows.RGB(15, 23, 42),
				LightModeTitleTextInactive: windows.RGB(71, 85, 105),
				LightModeBorder:            windows.RGB(203, 213, 225),
				LightModeBorderInactive:    windows.RGB(203, 213, 225),
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
