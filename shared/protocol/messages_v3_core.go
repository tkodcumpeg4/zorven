package protocol

// 0.3.0 ajan cekirdegi eklemeleri (additif; eski taraflar bilinmeyen alanlari
// yok sayar, yeni mesajlar yetenek pazarligi ile korunur).

// FeatureDeviceUpdate, istemcinin hello'dan sonra cihaz IP/hostname
// degisikligini device_update ile bildirebildigini ilan eder. Sunucu
// destekliyorsa hello_ack'te geri onaylar; onaylamayan sunucuya mesaj gonderilmez.
const FeatureDeviceUpdate = "device_update"

// TypeDeviceUpdate: ajan -> sunucu, cihaz adresi/ad degisti.
const TypeDeviceUpdate MessageType = "device_update"

// DeviceUpdate, baglanti suresince degisen cihaz bilgisini tasir. Bos alan
// "degismedi" demektir (sunucuda mevcut deger EZILMEZ).
type DeviceUpdate struct {
	Type     MessageType `json:"type"`
	Hostname string      `json:"hostname,omitempty"`
	IPs      []string    `json:"ips,omitempty"`
}

// Bye.Code degerleri (opsiyonel, yapisal): istemci kalici hata ayrimini
// "reason" metnine bakmadan yapar.
const (
	// ByeCodeAuthRevoked: token/cihaz anahtari gecersiz kilindi; yeniden denemek anlamsiz.
	ByeCodeAuthRevoked = "auth_revoked"
	// ByeCodeReplaced: ayni cihaz kimligiyle yeni bir baglanti bu oturumun yerini aldi.
	ByeCodeReplaced = "replaced"
)
