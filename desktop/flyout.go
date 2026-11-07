package main

import (
	"sync"
	"sync/atomic"
	"time"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Tepsi acilir penceresi (flyout): tepsi simgesine tiklaninca standart menu
// yerine ana pencere kucuk, cercevesiz ve hep ustte bir panele donusur; gorev
// cubugunun ustunde acilir, odagi kaybedince gizlenir. "Uygulamayi ac" tam
// pencereye eski boyut ve konumuyla doner.
const (
	flyoutW = 360 // DIP
	flyoutH = 540
)

type flyoutState struct {
	mu       sync.Mutex
	active   bool
	saved    winBounds
	have     bool
	blurHide time.Time // odak kaybiyla son gizlenme
	wasShown bool      // panel acilmadan once ana pencere gorunur muydu
}

var flyout flyoutState

// winBounds, pencerenin fiziksel piksel cinsinden konumu ve boyutu.
type winBounds struct{ X, Y, W, H int }

// CloseMain: arayuzdeki kapat dugmesi. "Tepsiye gizle" aciksa pencere gizlenir,
// degilse uygulama kapanir (cercevesiz pencerede OnBeforeClose tetiklenmez).
func (a *App) CloseMain() {
	if trayEnabled && a.hideToTrayEnabled() {
		a.toTray()
		return
	}
	a.QuitApp()
}

var trayHintShown atomic.Bool

// toTray, pencereyi tepsiye gizler. Tepsi simgesi henuz yoksa (ya da
// eklenemediyse) pencere gizlenmez, simge durumuna kucultulur: aksi halde
// uygulama arka planda calisip geri acilamaz hale gelirdi. Ilk gizlemede
// kullaniciya nereden acacagi bildirilir (Windows 11 yeni simgeleri tasma
// alanina koyar).
func (a *App) toTray() {
	if a.ctx == nil {
		return
	}
	if a.tray == nil || !a.tray.isReady() {
		wailsrt.WindowMinimise(a.ctx)
		return
	}
	a.leaveFlyout()
	a.HideWindow()
	if trayHintShown.CompareAndSwap(false, true) {
		a.notify("tray-hint", tm("tray.hint.title"), tm("tray.hint.body"))
	}
}

// OpenFlyout, ana pencereyi tepsi paneli olarak gosterir.
func (a *App) OpenFlyout() {
	if a.ctx == nil {
		return
	}
	flyout.mu.Lock()
	if !flyout.active {
		flyout.wasShown = a.visible.Load()
		if b, ok := platWindowBounds(); ok && flyout.wasShown {
			flyout.saved, flyout.have = b, true
		}
		flyout.active = true
	}
	wasShown := flyout.wasShown
	flyout.mu.Unlock()
	// Titreme olmasin: pencere gizliyken gorunum degisir, boyut ve konum ayarlanir,
	// arayuz yeni duzeni cizdikten sonra gosterilir.
	if wasShown {
		wailsrt.WindowHide(a.ctx)
	}
	emit(a.ctx, "flyout:open", nil)
	wailsrt.WindowSetMinSize(a.ctx, 0, 0)
	platPlaceFlyout(a.ctx, flyoutW, flyoutH)
	time.Sleep(90 * time.Millisecond)
	a.visible.Store(true)
	wailsrt.WindowShow(a.ctx)
	wailsrt.WindowUnminimise(a.ctx)
	platFocus()
}

// ToggleFlyout, tepsi tiklamasi: panel aciksa kapatir, degilse acar. Panel acikken
// simgeye tiklamak once odagi kaybettirip paneli gizler; hemen ardindan gelen
// tiklama paneli yeniden acmasin.
func (a *App) ToggleFlyout() {
	flyout.mu.Lock()
	active, recent := flyout.active, time.Since(flyout.blurHide) < 400*time.Millisecond
	flyout.mu.Unlock()
	if recent {
		return
	}
	if active && a.visible.Load() {
		a.CloseFlyout(false)
		return
	}
	a.OpenFlyout()
}

// FlyoutBlur, panel odagi kaybedince arayuzden cagrilir.
func (a *App) FlyoutBlur() {
	flyout.mu.Lock()
	active := flyout.active
	if active {
		flyout.blurHide = time.Now()
	}
	flyout.mu.Unlock()
	if active {
		a.CloseFlyout(false)
	}
}

// CloseFlyout, paneli kapatir; openFull ise tam pencereye doner, degilse gizler.
func (a *App) CloseFlyout(openFull bool) {
	flyout.mu.Lock()
	wasShown := flyout.active && flyout.wasShown
	flyout.mu.Unlock()
	a.leaveFlyout()
	// Panel acilmadan once pencere zaten aciksa kapanista geri getir.
	if openFull || wasShown {
		a.ShowWindow()
	} else {
		a.HideWindow()
	}
}

// leaveFlyout, panel modundan cikar (pencereyi gostermez/gizlemez).
func (a *App) leaveFlyout() {
	if a.ctx == nil {
		return
	}
	flyout.mu.Lock()
	was, saved, have := flyout.active, flyout.saved, flyout.have
	flyout.active = false
	flyout.mu.Unlock()
	if !was {
		return
	}
	// Gizliyken eski duzene ve boyuta don (gosterecek olan cagiran).
	wailsrt.WindowHide(a.ctx)
	a.visible.Store(false)
	emit(a.ctx, "flyout:close", nil)
	wailsrt.WindowSetAlwaysOnTop(a.ctx, false)
	wailsrt.WindowSetMinSize(a.ctx, 860, 560)
	if have {
		platSetBounds(a.ctx, saved)
	} else {
		wailsrt.WindowSetSize(a.ctx, 1040, 680)
		wailsrt.WindowCenter(a.ctx)
	}
	time.Sleep(60 * time.Millisecond)
}
