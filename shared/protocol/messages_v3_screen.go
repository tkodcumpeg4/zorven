package protocol

// 0.3.0 uzak ekran eklemeleri (yalniz ekleme; mevcut mesajlar degismedi).
//
// Hepsi mevcut tasima yolunu kullanir; sunucu rolesinde degisiklik gerekmez:
//   - InputKindText, ScreenInput.Kind degeridir; metin Key alaninda tasinir
//     (IME/emoji/yapistirma). Tus olaylarindan ayri: istemci metni Unicode
//     olarak (UTF-16 vekil ciftleri dahil) yazar.
//   - InputStateDegraded, ScreenInputStatus.State degeridir; Detail alanindaki
//     kisa kod nedeni soyler (DegradedSecureDesktop vb.).
const (
	// InputKindText: Key alani yazilacak metni tasir (en fazla MaxScreenText bayt).
	InputKindText = "text"

	// InputStateDegraded: uzak makinede goruntu/girdi gecici olarak kisitli
	// (guvenli masaustu, yakalama yeniden denemesi, pano siniri...).
	InputStateDegraded = "degraded"

	// DegradedSecureDesktop: uzak makinede guvenli masaustu (UAC/kilit ekrani) gorunuyor.
	DegradedSecureDesktop = "secure_desktop"
	// DegradedCaptureRetry: yakalama gecici olarak basarisiz, istemci yeniden deniyor.
	DegradedCaptureRetry = "capture_retry"
	// DegradedClipboardTooLarge: pano metni sinirdan buyuk, esitlenmedi.
	DegradedClipboardTooLarge = "clipboard_too_large"
	// Detail bos ("") olan InputStateDegraded mesaji kisitlamanin KALKTIGINI bildirir.

	// MaxScreenText, tek "text" olayinin en buyuk bayt boyutu.
	MaxScreenText = 4096

	// Tarayici dugme numaralari (ScreenInput.Button): 3=geri (XBUTTON1), 4=ileri (XBUTTON2).
	InputButtonBack    = 3
	InputButtonForward = 4
)

// 0.3.10 uzak ekran eklemeleri (yalniz ekleme). Hepsi ozellik bayragi arkasinda:
// pazarlanmadiysa ilgili mesaj HIC gonderilmez (eski ajan/sunucu ile uyum).
const (
	// FeatureScreenInfo: ajan screen_info (monitor sayisi + secili monitor) yollar.
	FeatureScreenInfo = "screen_info"
	// FeatureScreenPing: ajan screen_ping'e screen_pong ile yanit verir.
	FeatureScreenPing = "screen_ping"
	// FeatureScreenClipboardImage: iki yonlu gorsel (PNG) panosu (FrameClipImage).
	// Yalniz FeatureScreenClipboard ile birlikte ilan edilir.
	FeatureScreenClipboardImage = "screen_clipboard_image"
)

// ScreenInfo (istemci -> sunucu): yakalanabilir monitor sayisi ve secili monitor.
// Oturum basinda ve secili monitor degisince gonderilir.
type ScreenInfo struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Displays  int         `json:"displays"`
	Display   int         `json:"display"`
}

// Valid, alanlarin makul sinirlarda oldugunu soyler.
func (s ScreenInfo) Valid() bool {
	return s.Displays >= 1 && s.Displays <= 32 && s.Display >= 0 && s.Display < s.Displays
}

// ScreenPing (sunucu -> istemci) ve ScreenPong (istemci -> sunucu): T sunucunun
// saatidir (opak sayi), istemci aynen yankilar.
type ScreenPing struct {
	Type MessageType `json:"type"`
	T    int64       `json:"t"`
}

type ScreenPong struct {
	Type MessageType `json:"type"`
	T    int64       `json:"t"`
}
