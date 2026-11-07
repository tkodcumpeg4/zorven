package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// ctStore: istemci token'i ile ag oturumu testleri icin asgari depo.
type ctStore struct {
	store.Store
	clients map[string]store.Client // tokenID -> istemci
	roles   map[string]string       // userID -> rol
}

func (s *ctStore) GetClientByTokenID(_ context.Context, id string) (store.Client, error) {
	c, ok := s.clients[id]
	if !ok {
		return store.Client{}, store.ErrNotFound
	}
	return c, nil
}

func (s *ctStore) GetMemberRole(_ context.Context, _, userID string) (string, error) {
	return s.roles[userID], nil
}

type ctFixture struct {
	srv    *httptest.Server
	tokens map[string]string // cihaz -> tam token
}

func newCTFixture(t *testing.T) *ctFixture {
	t.Helper()
	fx := &ctFixture{tokens: map[string]string{}}
	st := &ctStore{clients: map[string]store.Client{}, roles: map[string]string{"alice": "member", "bob": "member", "carol": "member"}}
	for _, d := range []struct{ id, user string }{{"nas", ""}, {"lap", "alice"}, {"pc", "bob"}, {"orphan", ""}} {
		full, tid, hash, err := auth.GenerateClient()
		if err != nil {
			t.Fatal(err)
		}
		st.clients[tid] = store.Client{ID: d.id, TenantID: "t1", UserID: d.user, Name: d.id, TokenID: tid, TokenHash: hash}
		fx.tokens[d.id] = full
	}
	dir := netctl.MapDirectory{
		Users:   map[string]map[string]string{"t1": {"alice": "member", "bob": "member", "carol": "member"}},
		Devices: map[string]map[string]string{"t1": {"nas": "nas", "lap": "lap", "pc": "pc", "orphan": "orphan"}},
	}
	mgr := netctl.New(netctl.NewMemStore(), dir, nil, nil)
	for _, in := range []netctl.ShareInput{
		{Name: "db", OwnerClientID: "nas", Port: 5432, Grantees: []netctl.Grantee{{Type: netctl.GranteeUser, ID: "alice"}}},
		{Name: "dev", OwnerClientID: "nas", Port: 8080, Grantees: []netctl.Grantee{{Type: netctl.GranteeDevice, ID: "pc"}}},
		{Name: "secret", OwnerClientID: "nas", Port: 22, Grantees: []netctl.Grantee{{Type: netctl.GranteeUser, ID: "carol"}}},
	} {
		if _, err := mgr.CreateShare(context.Background(), "t1", in, "test"); err != nil {
			t.Fatal(err)
		}
	}
	// Sahip cihaz cevrimici olmali (paylasimlar yalniz o zaman netmap'e girer).
	nasKey := make([]byte, 32)
	rand.Read(nasKey)
	nc := mgr.Attach(netctl.Identity{TenantID: "t1", Kind: netctl.KindDevice, ClientID: "nas", ClientName: "nas"}, netctl.NewOutbox())
	t.Cleanup(nc.Close)
	reg, _ := protocol.Marshal(protocol.NetRegister{Type: protocol.TypeNetRegister, WGPub: base64.StdEncoding.EncodeToString(nasKey), Name: "nas"}, protocol.TypeNetRegister)
	if err := nc.HandleControl(context.Background(), protocol.TypeNetRegister, reg); err != nil {
		t.Fatal(err)
	}
	api := &Server{Store: st, Net: mgr}
	mw := &Middleware{Store: st, Limiter: ratelimit.New(1000, 1000), Next: api.Routes(), PublicPaths: map[string]bool{}}
	fx.srv = httptest.NewServer(mw)
	t.Cleanup(fx.srv.Close)
	return fx
}

// sharesSeen: ag oturumunu istemci token'iyla acar, netmap'teki paylasim adlarini doner.
func (fx *ctFixture) sharesSeen(t *testing.T, token string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(fx.srv.URL, "http") + "/api/v1/net/session"
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatalf("oturum acilamadi: %v", err)
	}
	defer c.CloseNow()
	key := make([]byte, 32)
	rand.Read(key)
	reg, _ := protocol.Marshal(protocol.NetRegister{Type: protocol.TypeNetRegister, WGPub: base64.StdEncoding.EncodeToString(key), Name: "access"}, protocol.TypeNetRegister)
	if err := c.Write(ctx, websocket.MessageText, reg); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("netmap gelmedi: %v", err)
		}
		if tp, _ := protocol.PeekType(data); tp != protocol.TypeNetMap {
			continue
		}
		var nm protocol.NetMap
		if err := json.Unmarshal(data, &nm); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, s := range nm.SharesIn {
			names = append(names, s.Name)
		}
		sort.Strings(names)
		return names
	}
}

func (fx *ctFixture) status(t *testing.T, method, path, token string) int {
	t.Helper()
	req, _ := http.NewRequest(method, fx.srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestClientTokenAccessSeesOnlyAddressedShares(t *testing.T) {
	fx := newCTFixture(t)
	// alice'in cihazi: kullaniciya acilan "db"; baskasinin cihaz/kullanici paylasimlari gorunmez.
	if got := fx.sharesSeen(t, fx.tokens["lap"]); strings.Join(got, ",") != "db" {
		t.Fatalf("alice cihazi: %v", got)
	}
	// bob'un cihazi: cihaza acilan "dev".
	if got := fx.sharesSeen(t, fx.tokens["pc"]); strings.Join(got, ",") != "dev" {
		t.Fatalf("bob cihazi: %v", got)
	}
	// sahipsiz cihaz: hicbir sey adreslenmemis.
	if got := fx.sharesSeen(t, fx.tokens["orphan"]); len(got) != 0 {
		t.Fatalf("sahipsiz cihaz paylasim gormemeli: %v", got)
	}
}

func TestClientTokenCannotUseOtherEndpoints(t *testing.T) {
	fx := newCTFixture(t)
	tok := fx.tokens["lap"]
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/net/shares"},
		{"POST", "/api/v1/net/shares"},
		{"DELETE", "/api/v1/net/shares/x"},
		{"GET", "/api/v1/tunnels"},
		{"GET", "/api/v1/clients"},
		{"POST", "/api/v1/net/session"},
	} {
		if code := fx.status(t, c.m, c.p, tok); code != http.StatusForbidden {
			t.Errorf("%s %s: %d (403 beklenir)", c.m, c.p, code)
		}
	}
}

func TestClientTokenInvalidRejected(t *testing.T) {
	fx := newCTFixture(t)
	bad := fx.tokens["lap"][:len(fx.tokens["lap"])-3] + "xxx"
	if code := fx.status(t, "GET", "/api/v1/net/session", bad); code != http.StatusUnauthorized {
		t.Errorf("bozuk token: %d", code)
	}
	if code := fx.status(t, "GET", "/api/v1/net/session", ""); code != http.StatusUnauthorized {
		t.Errorf("kimliksiz: %d", code)
	}
}
