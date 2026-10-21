package localctl

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Grantee, paylasim alicisi (type: user | group | device).
type Grantee struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ShareSpec, share_create istegi.
type ShareSpec struct {
	Name       string    `json:"name,omitempty"`
	Port       int       `json:"port"`
	Proto      string    `json:"proto,omitempty"`
	To         []Grantee `json:"to"`
	ExpiresSec int       `json:"expires_sec,omitempty"`
	// Persist: ajan kapaninca paylasim silinmesin. Servis ajaninda her zaman
	// kalici sayilir (netOp: Endpoint.Service).
	Persist bool `json:"persist,omitempty"`
}

// ShareInfo, bir paylasimin ozeti.
type ShareInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Port        int       `json:"port"`
	Proto       string    `json:"proto"`
	To          []Grantee `json:"to,omitempty"`    // yalniz bu ajanin IPC ile olusturdugu paylasimlarda bilinir
	Allow       []string  `json:"allow,omitempty"` // yetkili es IP'leri
	ExpiresUnix int64     `json:"expires_unix,omitempty"`
	Persistent  bool      `json:"persistent,omitempty"`
}

// NetController, ajanin Zorven Net (paylasim/erisim) IPC yuzeyi. Controller
// bunu da uygularsa share_* / access_* islemleri acilir.
type NetController interface {
	ShareCreate(ctx context.Context, sp ShareSpec) (ShareInfo, error)
	ShareDelete(ctx context.Context, idOrName string) (ShareInfo, error)
	ShareList() ([]ShareInfo, error)
	AccessOpen(ctx context.Context, target, local string) (string, error)
	AccessClose(local string) error
}

// ErrNoNet, ajanin Zorven Net dugumu olmadiginda doner.
var ErrNoNet = errors.New("bu istemcide Zorven Net etkin degil")

// IsUnsupported, eski bir ajanin bu islemi tanimadigini belirtir.
func IsUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "bilinmeyen islem")
}

func opTimeout(op string) time.Duration {
	switch op {
	case "share_create", "share_delete", "access_open":
		return 25 * time.Second
	}
	return 8 * time.Second
}

func (e Endpoint) netOp(ctl Controller, req Request, resp *Response) error {
	nc, ok := ctl.(NetController)
	if !ok {
		return ErrNoNet
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch req.Op {
	case "share_create":
		if req.Share == nil {
			return errors.New("paylasim bilgisi eksik")
		}
		sp := *req.Share
		if e.Service {
			sp.Persist = true
		}
		si, err := nc.ShareCreate(ctx, sp)
		if err != nil {
			return err
		}
		resp.Shares = []ShareInfo{si}
	case "share_delete":
		si, err := nc.ShareDelete(ctx, req.ShareID)
		if err != nil {
			return err
		}
		resp.Shares = []ShareInfo{si}
	case "share_list":
		l, err := nc.ShareList()
		if err != nil {
			return err
		}
		resp.Shares = l
	case "access_open":
		if req.Target == "" || req.Local == "" {
			return errors.New("hedef ve yerel adres gerekli")
		}
		addr, err := nc.AccessOpen(ctx, req.Target, req.Local)
		if err != nil {
			return err
		}
		resp.Local = addr
	case "access_close":
		return nc.AccessClose(req.Local)
	}
	return nil
}
