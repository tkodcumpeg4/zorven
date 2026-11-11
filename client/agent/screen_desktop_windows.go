//go:build windows

package agent

import (
	"sync"

	"golang.org/x/sys/windows"
)

var (
	modUser32Screen      = windows.NewLazySystemDLL("user32.dll")
	procOpenInputDesktop = modUser32Screen.NewProc("OpenInputDesktop")
	procSetThreadDesktop = modUser32Screen.NewProc("SetThreadDesktop")
	procCloseDesktop     = modUser32Screen.NewProc("CloseDesktop")

	procSetProcessDpiAwarenessContext = modUser32Screen.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = modUser32Screen.NewProc("SetProcessDPIAware")
	modShcoreScreen                   = windows.NewLazySystemDLL("shcore.dll")
	procSetProcessDpiAwareness        = modShcoreScreen.NewProc("SetProcessDpiAwareness")
)

// threadDesktops, her is parcacigi icin ACTIGIMIZ son masaustu tutamagi.
// Yeni masaustune baglanma basarili olunca eskisi kapatilir (aksi halde her
// yeniden baglanmada bir tutamak sizardi).
var (
	threadDesktopMu sync.Mutex
	threadDesktops  = map[uint32]uintptr{}
)

// attachToInputDesktop, calisan thread'i kullanicinin aktif masaustune (Default) baglar.
// Windows'ta arka plan islemleri veya CLI araclari farkli bir masaustunde calisiyor
// olabilir; aktif girdi masaustune gecilmezse BitBlt "The handle is invalid" hatasiyla
// basarisiz olur.
func attachToInputDesktop() bool {
	hInputDesk, _, _ := procOpenInputDesktop.Call(0, 0, 0x01FF) // GENERIC_ALL
	if hInputDesk == 0 {
		return false
	}
	ret, _, _ := procSetThreadDesktop.Call(hInputDesk)
	if ret == 0 {
		// Baglanamadi: yeni tutamagi kapat (kullanimda degil).
		procCloseDesktop.Call(hInputDesk)
		return false
	}
	tid := windows.GetCurrentThreadId()
	threadDesktopMu.Lock()
	prev := threadDesktops[tid]
	threadDesktops[tid] = hInputDesk
	threadDesktopMu.Unlock()
	if prev != 0 && prev != hInputDesk {
		// Is parcacigi artik yeni masaustunde; eski tutamak serbest.
		procCloseDesktop.Call(prev)
	}
	return true
}

// dpiOnce, DPI farkindaligi ayarini surec basina bir kez yapar.
var dpiOnce sync.Once

// ensureDPIAware, sureci PER_MONITOR_AWARE_V2 yapar (geri dusmelerle).
//
// NEDEN: DPI-unaware surecte GetSystemMetrics/GetDC sanallastirilmis (olceklenmis)
// degerler doner; yakalanan goruntu bulanik/kirpik olur ve SendInput mutlak
// koordinatlari yanlis yere gider. Surec basina bir kez, ekran yakalama/girdi
// baslamadan ONCE cagrilmali; sonradan cagirmak etkisizdir. Zaten ayarli ise
// (manifest) Windows hata doner; bu zararsizdir.
func ensureDPIAware() {
	dpiOnce.Do(func() {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if procSetProcessDpiAwarenessContext.Find() == nil {
			ctx := ^uintptr(3) // -4
			if r, _, _ := procSetProcessDpiAwarenessContext.Call(ctx); r != 0 {
				return
			}
		}
		// Windows 8.1+: PROCESS_PER_MONITOR_DPI_AWARE = 2
		if modShcoreScreen.Load() == nil && procSetProcessDpiAwareness.Find() == nil {
			if hr, _, _ := procSetProcessDpiAwareness.Call(2); hr == 0 {
				return
			}
		}
		if procSetProcessDPIAware.Find() == nil {
			procSetProcessDPIAware.Call()
		}
	})
}

// secureDesktopActive, girdi masaustu (Winlogon: UAC onayi, kilit ekrani, Ctrl+Alt+Del)
// bu surece kapaliysa true. Normal Default masaustu acilabilir; guvenli masaustu
// OpenInputDesktop'ta erisim reddi verir. Yalniz yakalama BASARISIZ olunca
// nedeni ayirmak icin kullanilir (yanlis pozitif zararsiz: yalnizca ipucu).
func secureDesktopActive() bool {
	const desktopReadObjects = 0x0001
	h, _, _ := procOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if h == 0 {
		return true
	}
	procCloseDesktop.Call(h)
	return false
}
