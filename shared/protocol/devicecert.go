package protocol

import (
	"strconv"
	"strings"
)

// Z4 — Cihaz istemci sertifikasi (kiraci CA'si) ve imzali (attested) durus.
//
// Pazarlik: istemci Hello.Features'ta FeatureDeviceCert ilan eder; sunucu
// (kiraci Enterprise plan + CA anahtari yapilandirilmis) HelloAck.Features'ta
// yankilar ve HelloAck.DeviceChallenge'a oturuma ozel rastgele bir deger yazar.
// Yankilanmazsa istemci hicbir device_cert mesaji GONDERMEZ; eski istemci
// ozelligi ilan etmez ve durus raporu "imzasiz" (beyan) kalir.
//
// Akis:
//  1. Istemci yerel bir ECDSA P-256 anahtar cifti uretir (ozel anahtar cihazdan
//     ASLA ayrilmaz) ve CSR gonderir: device_cert_request.
//  2. Sunucu kiraci CA'si ile kisa omurlu (vars. 30 gun) istemci sertifikasi
//     verir: device_cert. Istemci suresinin 1/3'u kala yeniler.
//  3. posture_report icindeki Attest alani, raporun SigningBytes ciktisinin
//     ECDSA-SHA256 imzasini tasir; sunucu zinciri, iptal listesini ve imzayi
//     dogrular, basariliysa durusu "attested" isaretler.

// FeatureDeviceCert, istemcinin cihaz sertifikasi alip durus raporunu imzalayabildigini bildirir.
const FeatureDeviceCert = "device_cert"

const (
	// TypeDeviceCertRequest (istemci -> sunucu): sertifika (yeni/yenileme) istegi.
	TypeDeviceCertRequest MessageType = "device_cert_request"
	// TypeDeviceCert (sunucu -> istemci): verilen sertifika veya hata.
	TypeDeviceCert MessageType = "device_cert"
)

// DeviceCertRequest, PEM kodlu PKCS#10 CSR tasir. Sunucu CSR'deki konu alanini
// YOK SAYAR (kimlik oturumdan gelir); yalniz genel anahtar kullanilir.
type DeviceCertRequest struct {
	Type   MessageType `json:"type"`
	CSRPEM string      `json:"csr_pem"`
	// Renew, bilgi amaclidir (denetim kaydi): mevcut sertifikanin yenilenmesi.
	Renew bool `json:"renew,omitempty"`
}

// DeviceCert, sunucunun yaniti. Error doluysa sertifika yoktur.
type DeviceCert struct {
	Type      MessageType `json:"type"`
	CertPEM   string      `json:"cert_pem,omitempty"`
	CAPEM     string      `json:"ca_pem,omitempty"`
	Serial    string      `json:"serial,omitempty"` // onaltilik
	NotAfter  int64       `json:"not_after,omitempty"`
	Error     string      `json:"error,omitempty"`
	ErrorCode string      `json:"error_code,omitempty"` // plan_required | no_key | rate_limited | bad_csr
}

// PostureAttest, durus raporunun cihaz sertifikasiyla imzasi.
type PostureAttest struct {
	CertPEM string `json:"cert_pem"`
	// Sig, SigningBytes(challenge)'in ECDSA-SHA256 (ASN.1 DER) imzasi, base64.
	Sig string `json:"sig"`
}

// SigningBytes, imzalanan KANONIK baytlar. Alan sirasi ve ayirici sabittir;
// istemci ve sunucu ayni islevi kullanir (sozlesme degisirse surum oneki artar).
func (r PostureReport) SigningBytes(challenge string) []byte {
	clean := func(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }
	parts := []string{
		"zorven-posture-v1",
		clean(challenge),
		clean(r.OSName), clean(r.OSVersion), clean(r.OSBuild),
		clean(r.DiskEncrypted), clean(r.FirewallOn), clean(r.ScreenLock),
		clean(r.ClientVersion), strconv.FormatBool(r.IsService),
	}
	return []byte(strings.Join(parts, "\n"))
}
