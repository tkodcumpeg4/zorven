package protocol

// Z1 — Cihaz durusu (posture) raporu. Istemci -> sunucu.
//
// Pazarlik: istemci Hello.Features'ta FeaturePosture ilan eder; sunucu
// destekliyorsa HelloAck.Features'ta yankilar. Yankilanmazsa istemci rapor
// GONDERMEZ. Eski istemci rapor yollamaz -> sunucuda posture "yok" (kosullu
// politikalarda posture_missing).
//
// GUVEN: rapor istemci beyanidir (kok yetkili saldirgan sahteleyebilir).

// FeaturePosture, istemcinin posture_report gonderdigini bildirir.
const FeaturePosture = "posture"

// TypePostureReport, el sikismadan sonra, ardindan 5 dakikada bir (degismediyse
// en fazla 30 dakikada bir) gonderilir.
const TypePostureReport MessageType = "posture_report"

// Uc durumlu alan degerleri. Toplanamayan alan "unknown".
const (
	PostureYes     = "yes"
	PostureNo      = "no"
	PostureUnknown = "unknown"
)

// PostureReport, cihaz durusu. Her alan "toplanamadi" icin "unknown"/bos olabilir.
type PostureReport struct {
	Type          MessageType `json:"type"`
	OSName        string      `json:"os_name"`    // windows | macos | linux
	OSVersion     string      `json:"os_version"` // "10.0.22631", "14.5", "22.04"
	OSBuild       string      `json:"os_build,omitempty"`
	DiskEncrypted string      `json:"disk_encrypted"` // yes | no | unknown
	FirewallOn    string      `json:"firewall_on"`    // yes | no | unknown
	ScreenLock    string      `json:"screen_lock"`    // yes | no | unknown
	ClientVersion string      `json:"client_version,omitempty"`
	IsService     bool        `json:"is_service,omitempty"`
	// Attest (Z4, FeatureDeviceCert): cihaz sertifikasi imzasi. Yoksa rapor beyandir.
	Attest *PostureAttest `json:"attest,omitempty"`
}
