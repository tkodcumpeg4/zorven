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
