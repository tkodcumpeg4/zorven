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
)

// MessageType, kontrol mesajlarinin ayrimini yapan zarf alani.
type MessageType string

const (
	TypeHello        MessageType = "hello"
	TypeHelloAck     MessageType = "hello_ack"
	TypePing         MessageType = "ping"
	TypePong         MessageType = "pong"
	TypeHTTPRequest  MessageType = "http_request"
	TypeHTTPResponse MessageType = "http_response"
	TypeHTTPError    MessageType = "http_error"
	TypeCancel       MessageType = "cancel"
	TypeConfigUpdate MessageType = "config_update"
	TypeBye          MessageType = "bye"

	// WebSocket passthrough (yukseltilmis baglantilar)
	TypeWSOpen   MessageType = "ws_open"   // sunucu -> ajan: yerel WS'e baglan
	TypeWSAccept MessageType = "ws_accept" // ajan -> sunucu: 101 sonucu veya hata
	TypeWSClose  MessageType = "ws_close"  // her iki yon: WS akisini kapat

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

	// IsService, istemcinin arka plan sistem servisi (Windows Service / systemd)
	// olarak calisip calismadigini belirtir.
	IsService bool `json:"is_service,omitempty"`

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
}

type Ping struct {
	Type MessageType `json:"type"`
	TS   time.Time   `json:"ts"`
}

type HTTPRequest struct {
	Type       MessageType         `json:"type"`
	ReqID      uint64              `json:"req_id"`
	TunnelID   string              `json:"tunnel_id"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Query      string              `json:"query"`
	Headers    map[string][]string `json:"headers"`
	HasBody    bool                `json:"has_body"`
	RemoteAddr string              `json:"remote_addr"`
}

type Cancel struct {
	Type   MessageType `json:"type"`
	ReqID  uint64      `json:"req_id"`
	Reason string      `json:"reason"`
}

type ConfigUpdate struct {
	Type    MessageType  `json:"type"`
	Tunnels []TunnelSpec `json:"tunnels"`
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
	Codec string `json:"codec"`
}

// ScreenInput, fare/klavye olayi (dashboard -> sunucu -> istemci).
//
// X ve Y NORMALIZE'dir (0..1): dashboard karenin boyutunu bilir ama istemcinin
// gercek ekran cozunurlugunu bilmez. Istemci 0..1'i kendi ekranina olcekler.
// Boylece dashboard'daki olcekleme/oranla istemci cozunurlugu birbirinden bagimsiz kalir.
type ScreenInput struct {
	Type      MessageType `json:"type"`
	SessionID string      `json:"session_id"`
	Kind      string      `json:"kind"`    // mousemove|mousedown|mouseup|wheel|keydown|keyup
	X         float64     `json:"x"`       // 0..1 (fare olaylari)
	Y         float64     `json:"y"`       // 0..1
	Button    int         `json:"button"`  // 0=sol 1=orta 2=sag (tarayici standardi)
	DeltaY    float64     `json:"delta_y"` // tekerlek
	Key       string      `json:"key"`     // tarayici KeyboardEvent.key
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
