package wgnet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// SessionConfig: `zorven access` oturum dugumu (API token ile).
type SessionConfig struct {
	ServerAddr string // host:port
	Token      string // API token
	Insecure   bool   // ws://
	HTTPClient *http.Client
	Log        *slog.Logger
	Name       string
}

// FatalSessionError: yeniden denemenin anlami olmayan hata (401/403/...).
type FatalSessionError struct{ Msg string }

func (e *FatalSessionError) Error() string { return e.Msg }

type wsTransport struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (t *wsTransport) SendControl(ctx context.Context, v any, mt protocol.MessageType) error {
	b, err := protocol.Marshal(v, mt)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return t.c.Write(wctx, websocket.MessageText, b)
}

func (t *wsTransport) SendBinary(ctx context.Context, b []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return t.c.Write(wctx, websocket.MessageBinary, b)
}

// RunSession, tek bir oturum baglantisi kurar; baglanti bitince doner.
// Dugum ilk netmap'i alinca onReady cagrilir.
func RunSession(ctx context.Context, cfg SessionConfig, onReady func(*Node)) error {
	scheme := "wss"
	if cfg.Insecure {
		scheme = "ws"
	}
	u := url.URL{Scheme: scheme, Host: cfg.ServerAddr, Path: "/api/v1/net/session"}
	dctx, dcancel := context.WithTimeout(ctx, 20*time.Second)
	c, resp, err := websocket.Dial(dctx, u.String(), &websocket.DialOptions{
		HTTPClient: cfg.HTTPClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + cfg.Token}},
	})
	dcancel()
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return &FatalSessionError{"API token gecersiz (401)"}
			case http.StatusForbidden:
				return &FatalSessionError{"ag oturumu reddedildi (403): token kullaniciya bagli ve net:read kapsamli olmali"}
			case http.StatusServiceUnavailable, http.StatusPaymentRequired:
				return &FatalSessionError{fmt.Sprintf("sunucu Zorven Net'i sunmuyor (%d)", resp.StatusCode)}
			}
		}
		return fmt.Errorf("baglanilamadi: %w", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(protocol.WSReadLimit)

	key, err := GenerateKey() // oturum anahtari: surece ozel, diske YAZILMAZ
	if err != nil {
		return err
	}
	tr := &wsTransport{c: c}
	node := NewNode(cfg.Log, tr, key, cfg.Name)
	node.DisableShares = true
	defer node.Close()
	if err := node.Register(ctx); err != nil {
		return err
	}
	failed, failErr := node.Failed()
	go func() {
		if node.WaitReady(ctx) == nil && onReady != nil {
			onReady(node)
		}
	}()
	go func() {
		select {
		case <-failed:
			c.Close(websocket.StatusPolicyViolation, "net error")
		case <-ctx.Done():
		}
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			select {
			case <-failed:
				return &FatalSessionError{failErr().Error()}
			default:
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if typ == websocket.MessageBinary {
			if f, err := protocol.DecodeBodyFrame(data); err == nil && f.FrameType == protocol.FrameWG {
				node.HandleFrame(f.ReqID, f.Payload)
			}
			continue
		}
		if mt, err := protocol.PeekType(data); err == nil {
			node.HandleControl(ctx, mt, data)
		}
	}
}

// Accessor, yeniden baglanan bir oturum dugumu tutar ve hedeflere baglanti acar.
type Accessor struct {
	Cfg SessionConfig

	mu    sync.Mutex
	cur   *Node
	ready chan struct{}
}

func NewAccessor(cfg SessionConfig) *Accessor { return &Accessor{Cfg: cfg, ready: make(chan struct{})} }

// Run, ctx bitene veya kalici hataya kadar baglanti kurar/yeniler.
func (a *Accessor) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		started := time.Now()
		err := RunSession(ctx, a.Cfg, func(n *Node) {
			a.mu.Lock()
			a.cur = n
			select {
			case <-a.ready:
			default:
				close(a.ready)
			}
			a.mu.Unlock()
		})
		a.mu.Lock()
		a.cur = nil
		a.mu.Unlock()
		if ctx.Err() != nil {
			return nil
		}
		var fatal *FatalSessionError
		if errors.As(err, &fatal) {
			return err
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		if a.Cfg.Log != nil {
			a.Cfg.Log.Warn("ag oturumu koptu, yeniden denenecek", "hata", err, "bekleme", backoff)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff + time.Duration(rand.Int64N(int64(backoff/4)+1))):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// WaitReady: ilk kez hazir olana kadar bekler.
func (a *Accessor) WaitReady(ctx context.Context) error {
	select {
	case <-a.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Accessor) node() *Node {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur
}

// State: guncel netmap (baglantisizken bos).
func (a *Accessor) State() *protocol.NetState {
	if n := a.node(); n != nil {
		return n.State()
	}
	return protocol.NewNetState()
}

// Dialer, "ad[:port]" hedefi icin DialFunc dondurur; her cagrida guncel
// netmap'ten paylasimi cozer (yeniden baglanmaya dayanikli). network: paylasimin proto'su.
func (a *Accessor) Dialer(target string) (DialFunc, string, error) {
	n := a.node()
	if n == nil {
		return nil, "", errors.New("ag oturumu hazir degil")
	}
	sh, err := n.Resolve(target)
	if err != nil {
		return nil, "", err
	}
	id := sh.ID
	return func(ctx context.Context) (net.Conn, error) {
		cur := a.node()
		if cur == nil {
			return nil, errors.New("ag oturumu yeniden baglaniyor")
		}
		st := cur.State()
		s, ok := st.SharesIn[id]
		if !ok {
			// Paylasim yeniden adlandirilmis/iptal olabilir.
			if s2, err := ResolveShare(st, target); err == nil {
				s, ok = s2, true
			}
		}
		if !ok {
			return nil, errors.New("paylasim artik erisilebilir degil (iptal edilmis olabilir)")
		}
		return cur.DialContext(ctx, s.Proto, net.JoinHostPort(s.OwnerIP, fmt.Sprint(s.Port)))
	}, sh.Proto, nil
}

// SplitTarget: yardimci (ornek: "nas:5432").
func SplitTarget(t string) string { return strings.TrimSpace(t) }
