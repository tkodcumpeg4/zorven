// Package netctl, Zorven Net (WireGuard Mod A) kontrol duzlemidir: dugum
// kaydi, kiraci basina IP tahsisi, paylasimlar, filtreli netmap uretimi ve
// sifreli WireGuard paketlerinin (FrameWG) kiraci icinde relay'i.
//
// Paket yaprak bir pakettir: tunnel/api/store paketlerine BAGIMLI DEGILDIR.
// Kalicilik Store, kimlik dizini Directory, gorunurluk Visibility arayuzleriyle
// verilir. Ticari katman (etiket/kosul ACL, plan kapilari) bu arayuzlere kanca
// takar; acik varsayilanlar bu pakette (OpenVisibility, no-op Hooks).
package netctl

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

const (
	KindDevice  = "device"
	KindSession = "session"

	ProtoTCP = "tcp"
	ProtoUDP = "udp"

	GranteeUser   = "user"
	GranteeGroup  = "group"
	GranteeDevice = "device"
)

// Hatalar (API katmani HTTP koduna cevirir).
var (
	ErrInvalid  = errors.New("gecersiz istek")
	ErrConflict = errors.New("cakisma")
	ErrNotFound = errors.New("bulunamadi")
	ErrDenied   = errors.New("izin yok")
)

// CodedError, makine-okur kodlu hata.
type CodedError struct {
	Kind error
	Code string
	Msg  string
}

func (e *CodedError) Error() string { return e.Msg }
func (e *CodedError) Unwrap() error { return e.Kind }

func invalid(code, msg string) error  { return &CodedError{ErrInvalid, code, msg} }
func conflict(code, msg string) error { return &CodedError{ErrConflict, code, msg} }
func notFound(code, msg string) error { return &CodedError{ErrNotFound, code, msg} }

// ErrorCode, hatanin kodunu doner.
func ErrorCode(err error) string {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return "error"
}

// Grantee, paylasim alicisi.
type Grantee struct {
	Type string `json:"type"` // user | group | device
	ID   string `json:"id"`
}

// Share, bir cihazin yerel portunun paylasimi.
type Share struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	OwnerClientID string     `json:"owner_client_id"`
	Name          string     `json:"name"`
	Proto         string     `json:"proto"`
	Port          int        `json:"port"`
	Grantees      []Grantee  `json:"grantees"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	// Ephemeral: sahip cihazin oturumu bitince silinir (zorven share).
	Ephemeral bool `json:"ephemeral"`
}

// NodeRec, kalici dugum kaydi (yalniz cihaz dugumleri saklanir).
type NodeRec struct {
	ID        string
	TenantID  string
	Kind      string
	ClientID  string
	Name      string
	IP        netip.Addr
	WGPub     string // base64
	CreatedAt time.Time
	LastSeen  time.Time
}

// Store, kalicilik arayuzu.
type Store interface {
	ListNodes(ctx context.Context, tenantID string) ([]NodeRec, error)
	PutNode(ctx context.Context, n NodeRec) error
	DeleteNode(ctx context.Context, tenantID, id string) error
	ListShares(ctx context.Context, tenantID string) ([]Share, error)
	PutShare(ctx context.Context, s Share) error
	DeleteShare(ctx context.Context, tenantID, id string) error
}

// Directory, kimlik dizini (kiraci uyeleri, cihazlar).
type Directory interface {
	// UserRole: kullanicinin organizasyon rolu; uye degilse "".
	UserRole(ctx context.Context, tenantID, userID string) (string, error)
	// DeviceName: cihaz bu kiracida var mi ve adi.
	DeviceName(ctx context.Context, tenantID, clientID string) (string, bool, error)
}

// GranteeResolver (opsiyonel, Directory ile birlikte uygulanir): kullanici icin
// e-posta, cihaz icin ad gibi okunur basvurulari kimlige cevirir.
type GranteeResolver interface {
	ResolveGrantee(ctx context.Context, tenantID string, g Grantee) (Grantee, error)
}

// Subject, bir dugumun gorunurluk kararinda kullanilan kimligi.
type Subject struct {
	Kind     string
	ClientID string // device
	UserID   string // session
	Role     string // session: organizasyon rolu
	Tags     map[string]string
}

// Visibility, "bu dugum bu paylasimin alicisi mi" karari. Ticari katman
// etiket/kosul tabanli bir uygulama kaydedebilir.
type Visibility interface {
	Matches(g Grantee, sub Subject) bool
	ValidateGrantee(ctx context.Context, tenantID string, g Grantee) error
}

// Hooks, ticari/ek kancalar; hepsi nil olabilir (no-op).
type Hooks struct {
	// AllowShare: paylasim olusturma kapisi (plan kotasi vb.). existing = kiracidaki aktif paylasim sayisi.
	AllowShare func(ctx context.Context, tenantID string, existing int) error
	// Audit: denetim kaydi.
	Audit func(tenantID, actor, action, target, detail string)
	// Enrich: dugum kimligine ek bilgi (etiket) ekler.
	Enrich func(ctx context.Context, tenantID string, sub *Subject)
	// Prepare (Z2): kiraci durumu ilk yuklenirken cagrilir (m.mu altinda; DB
	// erisimi serbest). Gate'in bellek anlik goruntusunu isitmak icin.
	Prepare func(ctx context.Context, tenantID string)
	// Gate (Z2): paylasim ACL'ine EK cihaz-baglami kapisi (posture, zaman penceresi).
	// ownerClientID = paylasimi yayinlayan cihaz (kaynak). false ise dugum o
	// paylasimdan GORUNMEZ. m.mu altinda cagrilir: SAF BELLEK olmali, DB/ag yok.
	Gate func(tenantID, ownerClientID string, sub Subject) bool
	// TunRules (W3/Z5): ek Mod B kurallari (ticari katman resource politikalarindan
	// turetir). tunClients = o an Mod B'de (TUN) ve cevrimici cihaz kimlikleri.
	// Kilit altinda cagrilir: hizli ve bloklamasiz (SAF BELLEK) olmali. Donen
	// kurallarin Origin alani dolu olmali (SrcDevices/DstDevices ile eslesir).
	TunRules func(tenantID string, tunClients []string) []TunRule
}

// NodeInfo, dis dunyaya (API) gosterilen dugum goruntusu.
type NodeInfo struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	ClientID string    `json:"client_id,omitempty"`
	UserID   string    `json:"user_id,omitempty"`
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen,omitempty"`
	// Iletilen bayt (relay sayaci, yalniz teshis).
	RelayedBytes int64 `json:"relayed_bytes"`
	// Tun (W3): dugum Mod B'de (OS TUN, tam ag katmani).
	Tun bool `json:"tun,omitempty"`
	// Path (W2, yalniz ShareInfo.Connected'ta): paylasim sahibiyle yol: direct | relay | "".
	Path  string `json:"path,omitempty"`
	RTTMs int    `json:"rtt_ms,omitempty"`
}

// ShareInfo, API goruntusu: share + canli durum.
type ShareInfo struct {
	Share
	OwnerName   string `json:"owner_name"`
	OwnerIP     string `json:"owner_ip,omitempty"`
	OwnerOnline bool   `json:"owner_online"`
	// Connected: simdi cevrimici olan, bu paylasima erisebilen dugumler.
	Connected []NodeInfo `json:"connected"`
	// Path (W2): bagli alicilarin yol ozeti: direct | relay | mixed | "".
	Path string `json:"path,omitempty"`
}
