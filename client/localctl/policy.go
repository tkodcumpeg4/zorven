package localctl

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Es kimlik dogrulama politikasi: saf fonksiyonlar (tum platformlarda derlenir,
// test edilebilir). Gercek kimlik okuma platforma ozgu dosyalardadir.

const (
	sidSystem = "S-1-5-18"
	sidAdmins = "S-1-5-32-544"
)

// pipeAccessAuthUsers, servis kanalinda Authenticated Users'a verilen haklar:
// READ_CONTROL | SYNCHRONIZE | FILE_READ_ATTRIBUTES | FILE_READ_EA | FILE_WRITE_DATA |
// FILE_READ_DATA. pipeCreateInstance OLMAMALI: bu hak yerel bir kullaniciya ayni adla
// sahte kanal ornegi acip operator/yonetici isteklerini ele gecirme imkani verir.
const (
	pipeAccessAuthUsers = uint32(0x12008b)
	pipeCreateInstance  = uint32(0x4) // FILE_APPEND_DATA, kanalda FILE_CREATE_PIPE_INSTANCE
)

// uidAllowed (Unix): es kullanici (surecin euid'i) veya root kabul edilir.
// Servis soketi root sahibidir ve 0600'dur (grup yok); bu denetim dizin/soket
// izinlerine ek ikinci savunmadir.
func uidAllowed(peerUID, selfEUID uint32) bool {
	return peerUID == 0 || peerUID == selfEUID
}

// sidAllowed (Windows): es SYSTEM, ajanin kendi kullanicisi (servis modunda
// self = SYSTEM) veya Administrators grubunun ETKIN uyesi (yukseltilmis token;
// UAC ile suzulmus tokenda grup "deny-only" oldugundan sayilmaz) ise kabul.
func sidAllowed(peerSID string, peerEnabledGroups []string, selfSID string) bool {
	peerSID = strings.ToUpper(peerSID)
	if peerSID == "" {
		return false
	}
	if peerSID == sidSystem || (selfSID != "" && peerSID == strings.ToUpper(selfSID)) {
		return true
	}
	for _, g := range peerEnabledGroups {
		if strings.EqualFold(g, sidAdmins) {
			return true
		}
	}
	return false
}

// Erisim duzeyi (yalniz Windows servis kanali birden fazla duzey kullanir; digerlerinde
// kabul edilen es her zaman tam yetkilidir).
type access int

const (
	accessNone     access = iota // reddedildi
	accessReadOnly               // yerel ama yetkisiz kullanici: yalniz suzulmus durum
	accessOperator               // kayitli operator: masaustunun kullandigi islemler
	accessFull                   // SYSTEM / yonetici / kendi kullanici: her islem
)

// sidAccess (Windows): es kimligine gore erisim duzeyi. SYSTEM/etkin yonetici/self
// = tam; operators listesindeki SID (kurulumu baslatan etkilesimli kullanici) =
// operator; servis kanalinda diger yerel kullanici = salt okunur. serviceEP false
// ise (kullanici ajani) eski davranis: izinli degilse reddedilir.
func sidAccess(peerSID string, peerEnabledGroups []string, selfSID string, operators []string, serviceEP bool) access {
	if sidAllowed(peerSID, peerEnabledGroups, selfSID) {
		return accessFull
	}
	if !serviceEP || strings.TrimSpace(peerSID) == "" {
		return accessNone
	}
	for _, o := range operators {
		if strings.EqualFold(strings.TrimSpace(o), strings.TrimSpace(peerSID)) {
			return accessOperator
		}
	}
	return accessReadOnly
}

// operatorOps, operatorun kullanabildigi islemler (masaustu uygulamasinin
// kullandiklari); debug_stacks yalniz yonetici/SYSTEM icindir.
var operatorOps = map[string]bool{
	"status": true, "reload": true, "set": true, "update": true,
	"net_up": true, "net_down": true, "net_status": true, "net_paths": true,
	"share_create": true, "share_delete": true, "share_list": true,
	"access_open": true, "access_close": true,
}

// opAllowed: bu erisim duzeyi islemi yapabilir mi?
func opAllowed(a access, op string) bool {
	switch a {
	case accessFull:
		return true
	case accessOperator:
		return operatorOps[op]
	case accessReadOnly:
		return op == "status"
	}
	return false
}

// redactStatus, salt okunur eslere verilen durum yanitindan hassas alanlari
// (hata metni, tunel ayrintisi, dugum listesi, kesinti nedenleri, sunucu adresi) siler.
func redactStatus(r *Response) {
	r.LastError = ""
	r.Server = ""
	r.Disconnects = nil
	r.Tunnels = nil
	r.Nodes = nil
	r.Shares = nil
	r.Paths = nil
}

// pipeName, bir uc noktanin yolundan Windows named pipe adini turetir.
func pipeName(path string) string {
	h := sha256.Sum256([]byte(strings.ToLower(path)))
	return `\\.\pipe\zorven-ctl-` + hex.EncodeToString(h[:8])
}

var (
	rejMu   sync.Mutex
	rejLast time.Time
	rejSkip int
)

// logReject, reddedilen es icin en fazla dakikada bir kez uyari yazar.
func logReject(err error) {
	rejMu.Lock()
	defer rejMu.Unlock()
	if !rejLast.IsZero() && time.Since(rejLast) < time.Minute {
		rejSkip++
		return
	}
	slog.Warn("yerel kontrol: yetkisiz es reddedildi", "neden", err.Error(), "bastirilan", rejSkip)
	rejLast, rejSkip = time.Now(), 0
}
