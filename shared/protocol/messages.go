// Package protocol, tunnel-server ile tunnel-client arasindaki tel formatini tanimlar.
// Kaynak: api_contract.md §2. Sunucu ve istemci BU paketi paylasir; boylece
// mesaj sozlesmesi tek yerde tutulur ve iki taraf birbirinden kayamaz.
package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// Version, WSS upgrade sirasinda X-Tunnel-Client-Version basliginda gonderilir.
// Sunucu uyumsuz surumu 426 ile reddeder.
const Version = "0.1.0"

// MaxBodyBytes, tek bir istek veya yanit govdesinin ust siniri
// (api_contract.md karar 4). Asilirsa 413 / body_too_large donulur.
// Amac: tek bir istek sunucu bellegini tuketemesin.
const MaxBodyBytes = 32 << 20 // 32 MB

// BodyChunkSize, govde akitilirken kullanilan parca boyutu. Yuksek hizli
// planlarda (100-500 Mbps) cerceve basi ek yuku azaltmak icin 64 KB.
const BodyChunkSize = 64 << 10 // 64 KB

// WSReadLimit, tek bir WebSocket mesajinin ust siniri.
//
// coder/websocket varsayilani 32 KB'dir ve bu YETMEZ: BodyChunkSize (32 KB)
// + HeaderSize (14 bayt) tam olarak varsayilanin ustune tasar ve baglanti
// "message too big" ile kopar. Iki tarafta da acikca ayarlanmalidir.
//
// 1 MB, en buyuk cercevemizin (~32.8 KB) ve cok sayida basliga sahip kontrol
// mesajlarinin cok uzerinde; yine de cerceve basina bellegi sinirli tutar.
const WSReadLimit = 1 << 20 // 1 MB

// Hata kodlari — api_contract.md §3 tablosuyla ayni.
const (
	CodeTunnelNotFound   = "tunnel_not_found"
	CodeTunnelDisabled   = "tunnel_disabled"
	CodeClientOffline    = "client_offline"
	CodeLocalUnreachable = "local_unreachable"
	CodeUpstreamTimeout  = "upstream_timeout"
	CodeBodyTooLarge     = "body_too_large"
	CodeWebSocketUnsup   = "websocket_not_supported"
	CodeRateLimited      = "rate_limited"
	CodeIPForbidden      = "ip_forbidden"
	CodeAuthRequired     = "auth_required" // tunel erisim denetimi: kimlik gerekli (401)
	CodeAccessDenied     = "access_denied" // tunel erisim denetimi: reddedildi (403)
)

// MessageType, kontrol mesajlarinin ayrimini yapan zarf alani.
type MessageType string

const (
	TypeHello           MessageType = "hello"
	TypeHelloAck        MessageType = "hello_ack"
	TypePing            MessageType = "ping"
	TypePong            MessageType = "pong"
	TypeHTTPRequest     MessageType = "http_request"
	TypeHTTPResponse    MessageType = "http_response"
	TypeHTTPError       MessageType = "http_error"
	TypeCancel          MessageType = "cancel"
	TypeConfigUpdate    MessageType = "config_update"
	TypeUpdateAvailable MessageType = "update_available"
	TypeBye             MessageType = "bye"

	// WebSocket passthrough (yukseltilmis baglantilar)
	TypeWSOpen   MessageType = "ws_open"   // sunucu -> ajan: yerel WS'e baglan
	TypeWSAccept MessageType = "ws_accept" // ajan -> sunucu: 101 sonucu veya hata
	TypeWSClose  MessageType = "ws_close"  // her iki yon: WS akisini kapat

	// Ham TCP/UDP tunel (FAZ 3 / D2): ziyaretci baglantisini yerel hedefe kopruler.
	TypeStreamOpen  MessageType = "stream_open"  // sunucu -> ajan: yerel TCP/UDP hedefe baglan
	TypeStreamAck   MessageType = "stream_ack"   // ajan -> sunucu: baglanti sonucu (ok/hata)
	TypeStreamClose MessageType = "stream_close" // her iki yon: akisi kapat

	// Uzak terminal (dashboard -> sunucu -> istemci -> PTY).
	//
	// GUVENLIK: bu mesajlar istemci makinesinde KABUK CALISTIRIR. Sunucu
	// tarafinda yalnizca admin anahtariyla dogrulanmis dashboard baglantisi
	// bu mesajlari uretebilir; istemci token'i tek basina yetmez.
	TypeTerminalOpen   MessageType = "terminal_open"
	TypeTerminalInput  MessageType = "terminal_input"
	TypeTerminalResize MessageType = "terminal_resize"
	TypeTerminalOutput MessageType = "terminal_output"
	TypeTerminalClose  MessageType = "terminal_close"
	TypeTerminalExit   MessageType = "terminal_exit"

	// Uzak ekran (dashboard -> sunucu -> istemci -> ekran/girdi).
	TypeScreenOpen  MessageType = "screen_open"
	TypeScreenFrame MessageType = "screen_frame"
	TypeScreenInput MessageType = "screen_input"
	TypeScreenClose MessageType = "screen_close"
	TypeScreenError MessageType = "screen_error"

	TypeWindowUpdate MessageType = "window_update"
)

// Envelope yalnizca tip alanini cozer; gercek mesaj ikinci gecerde cozulur.
type Envelope struct {
	Type MessageType `json:"type"`
}

// --- Client -> Server ------------------------------------------------------

type Hello struct {
	Type          MessageType `json:"type"`
	ClientVersion string      `json:"client_version"`
	Platform      string      `json:"platform"` // "windows/amd64"

	// Features, istemcinin destekledigi opsiyonel yetenekler.
	// Eski istemcilerde BOS gelir; sunucu o zaman eski davranisa duser.
	Features []string `json:"features,omitempty"`

	// RequestedTarget, CLI'dan tek komutla port paylasiminda (or. "http://localhost:8080")
	// sunucunun bu istemciye aninda tunel acmasini veya var olan uygun tuneli baglamasini ister.
	RequestedTarget string `json:"requested_target,omitempty"`

	// RequestedTTLSec (FAZ 2 / F07), RequestedTarget ile acilacak tunelin GECICI
	// olmasini ister; sure dolunca sunucu tuneli kaldirir. 0 / yok = kalici.
	// Eski istemciler bu alani hic gondermez, sunucu kalici tunel acar.
	RequestedTTLSec int `json:"requested_ttl_sec,omitempty"`

	// IsService, istemcinin arka plan sistem servisi (Windows Service / systemd)
	// olarak calisip calismadigini belirtir.
	IsService bool `json:"is_service,omitempty"`

	// Cihaz kimligi (FAZ 3 / F14). Baglanti kopunca kaybolmamasi icin sunucuda
	// kalicilastirilir: cihaz OFFLINE iken de "bu neydi" sorusu cevaplanabilsin.
	// Eski istemciler bu alanlari gondermez; gondermemeleri hata DEGILDIR.
	Hostname string   `json:"hostname,omitempty"`
	IPs      []string `json:"ips,omitempty"` // yerel adresler (loopback haric)

	// Metrics, istemcinin ilk baglanti anindaki canli donanim metrikleri.
	Metrics *Metrics `json:"metrics,omitempty"`
}

// Metrics, istemcinin canlı donanım kullanım verilerini taşır.
type Metrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	MemoryUsedMB  uint64  `json:"memory_used_mb,omitempty"`
	MemoryTotalMB uint64  `json:"memory_total_mb,omitempty"`
	DiskPercent   float64 `json:"disk_percent,omitempty"`
}

type Pong struct {
	Type    MessageType `json:"type"`
	TS      time.Time   `json:"ts"`
	Metrics *Metrics    `json:"metrics,omitempty"`
}

type HTTPResponse struct {
	Type    MessageType         `json:"type"`
	ReqID   uint64              `json:"req_id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	HasBody bool                `json:"has_body"`
}

// HTTPError, istemcinin yerel servise ulasamadigi durumlar icin.
// Code degerleri api_contract.md §3 hata tablosuyla ayni.
type HTTPError struct {
	Type    MessageType `json:"type"`
	ReqID   uint64      `json:"req_id"`
	Code    string      `json:"code"` // local_unreachable | upstream_timeout | body_too_large
	Message string      `json:"message"`
}

// --- Server -> Client ------------------------------------------------------

// TunnelSpec, istemciye "senin adina hangi hostname'ler dinleniyor" bilgisini tasir.
//
// Hostnames COGULDUR: bir tunel birden cok adla yayinlanabilir (kisa global ad
// + kiraci kapsamli ad). Alan yalnizca gosterim/log icindir; yonlendirme
// kararini sunucu verir, istemci yalnizca Target'a proxy'ler.
type TunnelSpec struct {
	ID        string   `json:"id"`
	Hostnames []string `json:"hostnames"`
	Target    string   `json:"target"`
}

type HelloAck struct {
	Type               MessageType  `json:"type"`
	ClientID           string       `json:"client_id"`
	SessionID          string       `json:"session_id"`
	HeartbeatIntervalS int          `json:"heartbeat_interval_s"`
	Tunnels            []TunnelSpec `json:"tunnels"`

	// Features, sunucunun bu oturum icin ETKINLESTIRDIGI yetenekler
	// (istemcinin istedikleri ile sunucunun destekledeklerinin kesisimi).
	Features []string `json:"features,omitempty"`

	// Settings (FAZ 3 / F15), cihaz icin kayitli uzak ayarlar. Baglanti
	// aninda gonderilir ki ajan ilk andan itibaren dogru yapilandirmayla
	// calissin; sonraki degisiklikler config_update ile iletilir.
	Settings *AgentSettings `json:"settings,omitempty"`
}

type Ping struct {
	Type MessageType `json:"type"`
	TS   time.Time   `json:"ts"`
}

type HTTPRequest struct {
	Type     MessageType         `json:"type"`
	ReqID    uint64              `json:"req_id"`
	TunnelID string              `json:"tunnel_id"`
	Method   string              `json:"method"`
	Path     string              `json:"path"`
	Query    string              `json:"query"`
	Headers  map[string][]string `json:"headers"`
	HasBody  bool                `json:"has_body"`
	// ContentLength, orijinal istegin bilinen govde uzunlugu (bayt). >0 ise ajan
	// yerel istegi CHUNKED yerine Content-Length ile gonderir (Content-Length
	// bekleyen backend'ler bos govde gormesin). -1/0: bilinmiyor -> chunked.
	ContentLength int64  `json:"content_length,omitempty"`
	RemoteAddr    string `json:"remote_addr"`
}

type Cancel struct {
	Type   MessageType `json:"type"`
	ReqID  uint64      `json:"req_id"`
	Reason string      `json:"reason"`
}

type ConfigUpdate struct {
	Type    MessageType  `json:"type"`
	Tunnels []TunnelSpec `json:"tunnels"`

	// Settings (FAZ 3 / F15), uzaktan ajan yapilandirmasi. nil ise ajan
	// mevcut ayarlarini korur — "ayar gonderilmedi" ile "ayarlari sifirla"
	// ayni sey DEGILDIR.
	Settings *AgentSettings `json:"settings,omitempty"`
}

// AgentSettings, panelden yonetilen ajan ayarlari (FAZ 3 / F15).
//
// GUVENLIK ILKESI: bu ayarlar yalnizca KISITLAYABILIR. Yerel olarak
// --no-terminal / --no-screen ile kapatilmis bir izin sunucudan ACILAMAZ.
// Aksi halde sunucuyu ele geciren biri, kullanicinin kasitla kapattigi uzak
// kabugu geri acabilirdi. Uygulama yeri: client/agent/settings.go.
//
// Isaretci alanlar: nil = "sunucu bu konuda bir sey soylemiyor", false =
// "kapat". Ikisi ayni sey degildir.
type AgentSettings struct {
	AutoUpdate    *bool `json:"auto_update,omitempty"`
	AllowTerminal *bool `json:"allow_terminal,omitempty"`
	AllowScreen   *bool `json:"allow_screen,omitempty"`

	// MetricsIntervalSec, 0 ise ajan varsayilanini korur.
	MetricsIntervalSec int `json:"metrics_interval_sec,omitempty"`
	// LogLevel: debug | info | warn | error. Bos ise degistirilmez.
	LogLevel string `json:"log_level,omitempty"`
	// ReconnectMaxBackoffSec, 0 ise ajan varsayilanini korur.
	ReconnectMaxBackoffSec int `json:"reconnect_max_backoff_sec,omitempty"`
}

// UpdateAvailable, sunucudan ajana: "yeni bir istemci surumu yayinlandi, simdi
// manifest'i kontrol et ve gerekirse kendini guncelle" sinyali. Version bos
// gecilebilir; ajan kararini /bin/manifest.json'a bakarak verir (tek dogruluk
// kaynagi orasi). Boylece sunucunun istemcinin tam surumunu bilmesi gerekmez.
type UpdateAvailable struct {
	Type    MessageType `json:"type"`
	Version string      `json:"version,omitempty"`
}

// --- WebSocket passthrough -------------------------------------------------

// WSOpen, sunucudan ajana: yerel servise bir WebSocket yukseltme baglantisi
// ac. ReqID bu WS akisini tekil tanimlar; sonraki FrameWSData cerceveleri ve
// WSClose ayni ReqID'yi kullanir.
type WSOpen struct {
	Type     MessageType         `json:"type"`
	ReqID    uint64              `json:"req_id"`
	TunnelID string              `json:"tunnel_id"`
	Path     string              `json:"path"`
	Query    string              `json:"query"`
	Headers  map[string][]string `json:"headers"`
}

// WSAccept, ajandan sunucuya: yerel servise yapilan yukseltmenin sonucu.
// Code bos ise Status/Headers 101 el sikismasini tasir; degilse hata.
type WSAccept struct {
	Type    MessageType         `json:"type"`
	ReqID   uint64              `json:"req_id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Code    string              `json:"code,omitempty"`
	Message string              `json:"message,omitempty"`
}

// WSClose, her iki yon: WS akisini sonlandir.
type WSClose struct {
	Type   MessageType `json:"type"`
	ReqID  uint64      `json:"req_id"`
	Reason string      `json:"reason,omitempty"`
}

// Ham tunel protokol sabitleri (StreamOpen.Proto). Sunucu store katmanindaki
// ProtoTCP/ProtoUDP ile ayni degerler; protokol paketi bagimsiz kalsin diye burada.
const (
	ProtoTCP = "tcp"
	ProtoUDP = "udp"
)

// --- Ham TCP/UDP tunel (FAZ 3 / D2) ----------------------------------------

// StreamOpen, sunucudan ajana: yerel hedefe ham TCP/UDP baglantisi ac. ReqID
// bu akisi (stream/flow) tekil olarak tanimlar; sonraki FrameStreamData /
// FrameDatagram cerceveleri ve StreamClose ayni ReqID'yi tasir.
type StreamOpen struct {
	Type       MessageType `json:"type"`
	ReqID      uint64      `json:"req_id"`
	TunnelID   string      `json:"tunnel_id"`
	Proto      string      `json:"proto"`                 // tcp | udp
	RemoteAddr string      `json:"remote_addr,omitempty"` // ziyaretci ip:port (log)

	// Dest (FAZ 3 / F20): alt ag yonlendirmesinde istenen hedef "ip:port".
	// Ajan bunu YALNIZCA tunel hedefi "subnet:<CIDR>" ise ve ip o araligin
	// icindeyse kullanir; aksi halde yok sayar (tunel tanimindaki hedefe gider).
	Dest string `json:"dest,omitempty"`
}

// StreamAck, ajandan sunucuya: baglanti kuruldu (Code bos) veya hata (Code dolu).
type StreamAck struct {
	Type    MessageType `json:"type"`
	ReqID   uint64      `json:"req_id"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// StreamClose, her iki yon: ham akisi sonlandir.
type StreamClose struct {
	Type   MessageType `json:"type"`
	ReqID  uint64      `json:"req_id"`
	Reason string      `json:"reason,omitempty"`
}

// --- Her iki yon -----------------------------------------------------------

type Bye struct {
	Type   MessageType `json:"type"`
	Reason string      `json:"reason"`
}

// --- Yardimcilar -----------------------------------------------------------

// PeekType, ham JSON'dan yalnizca zarf tipini okur.
func PeekType(data []byte) (MessageType, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("zarf cozulemedi: %w", err)
	}
	if env.Type == "" {
		return "", fmt.Errorf("zarfta type alani yok")
	}
	return env.Type, nil
}

// Marshal, mesaji JSON'a cevirir. Type alanini cagiranin doldurmasi gerekir;
// unutulursa burada yakalanir.
func Marshal(v any, t MessageType) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	got, err := PeekType(data)
	if err != nil {
		return nil, fmt.Errorf("%s mesaji serilestirilemedi: %w", t, err)
	}
	if got != t {
		return nil, fmt.Errorf("type alani %q olmali, %q bulundu", t, got)
	}
	return data, nil
}

// --- Uzak terminal ---------------------------------------------------------

// TerminalOpen, istemcide yeni bir kabuk oturumu acar (sunucu -> istemci).
type TerminalOpen struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Cols      uint16      `json:"cols"`
	Rows      uint16      `json:"rows"`
}

// TerminalInput, kullanicinin tus vuruslari (sunucu -> istemci).
// Data base64'tur: terminal ciktisi ikili olabilir ve JSON'da guvenli tasinmali.
type TerminalInput struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Data      []byte      `json:"data"`
}

// TerminalResize, pencere boyutu degisimi (sunucu -> istemci).
type TerminalResize struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Cols      uint16      `json:"cols"`
	Rows      uint16      `json:"rows"`
}

// TerminalOutput, kabugun ciktisi (istemci -> sunucu).
type TerminalOutput struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Data      []byte      `json:"data"`
}

// TerminalClose, oturumu sonlandirma istegi (sunucu -> istemci).
type TerminalClose struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
}

// TerminalExit, kabuk sonlandi (istemci -> sunucu).
type TerminalExit struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Code      int         `json:"code"`
	Message   string      `json:"message,omitempty"`
}

// --- Uzak ekran -----------------------------------------------------------

// ScreenOpen, istemcide ekran yakalamayi baslatir (sunucu -> istemci).
type ScreenOpen struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	FPS       int         `json:"fps"`       // saniyedeki kare (0 => varsayilan)
	Quality   int         `json:"quality"`   // JPEG kalitesi 1-100 (0 => varsayilan)
	MaxWidth  int         `json:"max_width"` // kareyi bu genislige olcekle (0 => tam)
	Display   int         `json:"display"`   // hangi monitor (0 => birincil)
	Mode      string      `json:"mode"`      // auto|mjpeg|h264 (bos => auto)
}

// ScreenFrame, tek bir ekran karesi (istemci -> sunucu -> dashboard).
// Data JPEG'dir; JSON'da base64 tasinir. Width/Height karenin gercek boyutu.
type ScreenFrame struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Data      []byte      `json:"data"`
	Width     int         `json:"width"`
	Height    int         `json:"height"`
	Seq       uint64      `json:"seq"`

	// ScreenW/ScreenH, uzak ekranin GERCEK (olceklenmemis) piksel boyutudur.
	//
	// Width/Height olceklenmis karenin boyutudur; ScreenOpen.MaxWidth
	// verilmediginde istemci yine de bir VARSAYILAN sinir uygular. Bu yuzden
	// dashboard, kare boyutuna bakarak gercek cozunurlugu ogrenemez — 4K bir
	// ekran 1600x900 gibi gorunurdu. Cozunurluk menusunun dogru secenekleri
	// (or. 3840x2160) sunabilmesi icin native boyut ayrica bildirilir.
	//
	// Eski istemcilerde 0 gelir; dashboard bu durumda kare boyutuna duser.
	ScreenW int `json:"screen_w,omitempty"`
	ScreenH int `json:"screen_h,omitempty"`

	// Codec: "mjpeg" => Data tek bir JPEG karedir (canvas'a cizilir).
	// "h264" => Data, fragmented-MP4 akisinin sirali bir PARCASIDIR; dashboard
	// tum parcalari MSE SourceBuffer'a ekler. Ilk h264 parcalari init segmentidir.
	//
	// ONEMLI: h264'te parcalar SIRALI ve EKSIKSIZ olmalidir. Bir parca
	// dusurulurse MSE akisi kurtarilamaz (bkz. tunnel.Session kare yonlendirme).
	Codec string `json:"codec"`

	// CaptureMS/EncodeMS, istemcide bu kareyi uretmenin maliyeti (milisaniye).
	//
	// Neden istemciden: dashboard yalnizca karelerin VARIS hizini olcebilir;
	// darbogazin yakalama mi, kodlama mi, yoksa ag mi oldugunu ayirt edemez.
	// Bu iki sayi metrik panelinde bunu ayirir. 0 => bildirilmedi.
	CaptureMS int `json:"capture_ms,omitempty"`
	EncodeMS  int `json:"encode_ms,omitempty"`
}

// ScreenInput, fare/klavye olayi (dashboard -> sunucu -> istemci).
//
// X ve Y NORMALIZE'dir (0..1): dashboard karenin boyutunu bilir ama istemcinin
// gercek ekran cozunurlugunu bilmez. Istemci 0..1'i kendi ekranina olcekler.
// Boylece dashboard'daki olcekleme/oranla istemci cozunurlugu birbirinden bagimsiz kalir.
type ScreenInput struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Kind      string      `json:"kind"`   // mousemove|mousedown|mouseup|wheel|keydown|keyup
	X         float64     `json:"x"`      // 0..1 (fare olaylari)
	Y         float64     `json:"y"`      // 0..1
	Button    int         `json:"button"` // 0=sol 1=orta 2=sag (tarayici standardi)

	// DeltaX/DeltaY, tekerlek. Tarayici birimi DeltaMode belirler:
	// 0=piksel, 1=satir, 2=sayfa. Istemci bunu platformun centik birimine
	// (Windows'ta WHEEL_DELTA=120) cevirir; ham deger gonderilirse
	// deltaMode=1'de (deltaY=3) neredeyse hic kaydirma olmaz.
	DeltaX    float64 `json:"delta_x"`
	DeltaY    float64 `json:"delta_y"`
	DeltaMode int     `json:"delta_mode"`

	// Key, KeyboardEvent.key — KLAVYE DUZENINE BAGLI yazilabilir deger
	// ("a", "A", "ğ"). Yazi girisi icin kullanilir.
	Key string `json:"key"`

	// Code, KeyboardEvent.code — FIZIKSEL tus ("KeyA", "ArrowLeft", "F5").
	// Duzenden bagimsizdir; ozel tuslar ve kisayollar bununla eslenir, cunku
	// Key ok tuslari icin "ArrowLeft" gibi ad verse de Ctrl basiliyken
	// harfler icin duzene gore degisir.
	Code string `json:"code"`

	// Modifier durumu: her olayda TARAYICININ bildirdigi anlik durum.
	//
	// Neden olay basina: uzak tarafta ayri bir modifier durumu tutmak
	// kacinilmaz olarak KAYAR (alt+tab ile pencere degisince keyup kaybolur ve
	// modifier sonsuza dek basili kalir). Tarayicinin gercegi her olayda
	// tasinirsa uzak taraf kendini her seferinde senkronlar.
	Ctrl  bool `json:"ctrl"`
	Shift bool `json:"shift"`
	Alt   bool `json:"alt"`
	Meta  bool `json:"meta"`

	// Repeat, tusun otomatik tekrar olayi oldugunu soyler.
	Repeat bool `json:"repeat"`
}

// ScreenClose, yakalamayi durdurma istegi (sunucu -> istemci).
type ScreenClose struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
}

// ScreenError, yakalama/girdi hatasi (istemci -> sunucu -> dashboard).
type ScreenError struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Message   string      `json:"message"`
}

// --- Akis kontrolu ---------------------------------------------------------

// FeatureFlowControl, kredi tabanli akis kontrolu yetenegi.
const FeatureFlowControl = "flow_control"

// InitialWindowBytes, her istek icin istemcinin IZINSIZ gonderebilecegi
// baslangic bayt miktari.
//
// Neden 2 MB: BodyChunkSize'in (64 KB) 32 kati. Sustained tunel throughput'u
// kabaca pencere/RTT ile sinirlidir; 256 KB pencere 50 ms RTT'de ~41 Mbps'e
// takiliyordu ve Pro (100) / Team (500) planlarini WAN'da veremiyorduk. 2 MB
// pencere 50 ms'de ~335 Mbps'e izin verir (istek basina ~2 MB bellek maliyeti).
// window_update byte-tabanli oldugu icin buyutmek geriye donuk uyumludur:
// eski istemciler kendi 256 KB'lik penceresini kullanmaya devam eder.
const InitialWindowBytes = 2 << 20

// WindowUpdate, sunucudan istemciye: "bu istek icin N bayt daha gonderebilirsin".
//
// NEDEN VAR: govde cerceveleri sunucunun TEK oturum okuma dongusunde islenir.
// Akis kontrolu olmadan, yavas bir tuketici (or. 100 KB/s indiren tarayici)
// o donguyu bloklar ve AYNI istemcideki tum diger istekler durur — olcumde
// throughput 7681 rps'den 15.3 rps'e dusuyordu. Kredi mekanizmasi sayesinde
// kredisi biten akis bekler, digerleri akmaya devam eder.
type WindowUpdate struct {
	Type      MessageType `json:"type"`
	ReqID     uint64      `json:"req_id"`
	Increment int         `json:"increment"`
}
