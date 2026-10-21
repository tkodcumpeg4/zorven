package api

// Zorven Net (WireGuard Mod A) REST API'si.
//
//	GET    /api/v1/net/nodes          — dugumler (cihaz + oturum)
//	DELETE /api/v1/net/nodes/{id}     — dugumu iptal et (owner/admin)
//	GET    /api/v1/net/shares         — paylasimlar (benim + bana acilanlar)
//	POST   /api/v1/net/shares         — paylasim olustur
//	PATCH  /api/v1/net/shares/{id}    — alicilari/adi/suresi guncelle
//	DELETE /api/v1/net/shares/{id}    — paylasimi iptal et (peer aninda duser)
//	GET    /api/v1/net/session        — WebSocket; `zorven access` oturum dugumu (yalniz API token)
//
// GUVENLIK: oturum ucu cerezle KABUL EDILMEZ (CSWSH; network.go ile ayni gerekce).
// Sunucu yalniz sifreli WireGuard paketlerini iletir, icerigi goremez.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Kapsamlar.
const (
	ScopeNetRead  = "net:read"
	ScopeNetWrite = "net:write"
)

func (s *Server) netGate(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.Net == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "net_disabled", "Zorven Net bu sunucuda kapali")
		return "", false
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return "", false
	}
	if s.NetGate != nil {
		if err := s.NetGate(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return "", false
		}
	}
	return tenantID, true
}

func (s *Server) netFail(w http.ResponseWriter, err error) {
	var ce *netctl.CodedError
	if errors.As(err, &ce) {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, netctl.ErrConflict):
			status = http.StatusConflict
		case errors.Is(err, netctl.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, netctl.ErrDenied):
			status = http.StatusForbidden
		case errors.Is(err, netctl.ErrInvalid):
			status = http.StatusUnprocessableEntity
		}
		writeJSONError(w, status, ce.Code, ce.Msg)
		return
	}
	s.fail(w, err)
}

// netActor: created_by / denetim kimligi.
func netActor(r *http.Request, c netCallerInfo) string {
	if c.UserID != "" {
		return "user:" + c.UserID
	}
	return auditActor(r)
}

func (s *Server) netSubject(c netCallerInfo) netctl.Subject {
	return netctl.Subject{Kind: netctl.KindSession, UserID: c.UserID, Role: c.Role}
}

type netShareView struct {
	netctl.ShareInfo
	CanManage bool   `json:"can_manage"`
	Relation  string `json:"relation"` // owner | grantee | admin
}

func (s *Server) listNetShares(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetRead) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	caller, err := s.netCaller(r, tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	all, err := s.Net.Shares(r.Context(), tenantID)
	if err != nil {
		s.netFail(w, err)
		return
	}
	actor := netActor(r, caller)
	out := make([]netShareView, 0, len(all))
	for _, si := range all {
		mine := si.CreatedBy == actor
		granted := s.Net.SubjectMatches(si.Share, s.netSubject(caller))
		if !caller.Privileged && !mine && !granted {
			continue
		}
		v := netShareView{ShareInfo: si, CanManage: caller.Privileged || mine}
		switch {
		case mine:
			v.Relation = "owner"
		case granted:
			v.Relation = "grantee"
		default:
			v.Relation = "admin"
		}
		// Alicilar baskasinin paylasimindaki cevrimici dugumleri gormez.
		if !v.CanManage {
			v.Connected = []netctl.NodeInfo{}
			v.Grantees = nil
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}

type createNetShareBody struct {
	Name       string           `json:"name"`
	ClientID   string           `json:"client_id"`
	Port       int              `json:"port"`
	Proto      string           `json:"proto"`
	To         []netctl.Grantee `json:"to"`
	ExpiresInS int              `json:"expires_in_sec"`
}

func (s *Server) createNetShare(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	var body createNetShareBody
	if !decode(w, r, &body) {
		return
	}
	caller, err := s.netCaller(r, tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !caller.Privileged {
		allowed, err := s.netDeviceAllowed(r, tenantID, body.ClientID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if !allowed {
			writeJSONError(w, http.StatusNotFound, "device_not_found", "cihaz bulunamadi")
			return
		}
	}
	var exp *time.Time
	if body.ExpiresInS > 0 {
		e := time.Now().Add(time.Duration(body.ExpiresInS) * time.Second)
		exp = &e
	}
	sh, err := s.Net.CreateShare(r.Context(), tenantID, netctl.ShareInput{
		Name: body.Name, OwnerClientID: body.ClientID, Port: body.Port, Proto: body.Proto,
		Grantees: body.To, ExpiresAt: exp,
	}, netActor(r, caller))
	if err != nil {
		s.netFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sh)
}

// manageableShare: paylasimi bulur ve cagiranin yonetebildigini dogrular.
func (s *Server) manageableShare(w http.ResponseWriter, r *http.Request, tenantID string) (netctl.Share, netCallerInfo, bool) {
	caller, err := s.netCaller(r, tenantID)
	if err != nil {
		s.fail(w, err)
		return netctl.Share{}, caller, false
	}
	sh, err := s.Net.GetShare(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.netFail(w, err)
		return sh, caller, false
	}
	if !caller.Privileged && sh.CreatedBy != netActor(r, caller) {
		writeJSONError(w, http.StatusNotFound, "share_not_found", "paylasim bulunamadi")
		return sh, caller, false
	}
	return sh, caller, true
}

type patchNetShareBody struct {
	Name       *string           `json:"name"`
	To         *[]netctl.Grantee `json:"to"`
	ExpiresInS *int              `json:"expires_in_sec"` // 0: suresiz
}

func (s *Server) updateNetShare(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	var body patchNetShareBody
	if !decode(w, r, &body) {
		return
	}
	sh, caller, ok := s.manageableShare(w, r, tenantID)
	if !ok {
		return
	}
	p := netctl.SharePatch{Name: body.Name, Grantees: body.To}
	if body.ExpiresInS != nil {
		var e *time.Time
		if *body.ExpiresInS > 0 {
			t := time.Now().Add(time.Duration(*body.ExpiresInS) * time.Second)
			e = &t
		}
		p.ExpiresAt = &e
	}
	out, err := s.Net.UpdateShare(r.Context(), tenantID, sh.ID, p, netActor(r, caller))
	if err != nil {
		s.netFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteNetShare(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	sh, caller, ok := s.manageableShare(w, r, tenantID)
	if !ok {
		return
	}
	if err := s.Net.DeleteShare(r.Context(), tenantID, sh.ID, netActor(r, caller)); err != nil {
		s.netFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listNetNodes(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetRead) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	caller, err := s.netCaller(r, tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	nodes, err := s.Net.Nodes(r.Context(), tenantID)
	if err != nil {
		s.netFail(w, err)
		return
	}
	out := make([]netctl.NodeInfo, 0, len(nodes))
	for _, n := range nodes {
		if !caller.Privileged {
			if n.Kind == netctl.KindSession && n.UserID != caller.UserID {
				continue
			}
			if n.Kind == netctl.KindDevice {
				if allowed, err := s.netDeviceAllowed(r, tenantID, n.ClientID); err != nil || !allowed {
					continue
				}
			}
		}
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
}

func (s *Server) revokeNetNode(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	caller, _ := s.netCaller(r, tenantID)
	if err := s.Net.RevokeNode(r.Context(), tenantID, r.PathValue("id"), netActor(r, caller)); err != nil {
		s.netFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// netSession, `zorven access`in baglandigi WebSocket ucu: kullanici adina
// gecici bir WireGuard dugumu. Metin = net_* kontrol, ikili = FrameWG.
func (s *Server) netSession(w http.ResponseWriter, r *http.Request) {
	if _, isToken := apiScopesFromContext(r.Context()); !isToken {
		writeJSONError(w, http.StatusForbidden, "token_required",
			"ag oturumu yalnizca API token ile acilabilir (zorven access)")
		return
	}
	if !requireScope(w, r, ScopeNetRead) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	caller, err := s.netCaller(r, tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if caller.UserID == "" {
		writeJSONError(w, http.StatusForbidden, "identity_required",
			"ag oturumu icin bir kullaniciya ait API token gerekir")
		return
	}
	role, err := s.Store.GetMemberRole(r.Context(), tenantID, caller.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if role == "" {
		writeJSONError(w, http.StatusForbidden, "not_member", "kullanici bu organizasyonun uyesi degil")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(protocol.WSReadLimit)
	s.runNetSession(r.Context(), c, netctl.Identity{
		TenantID: tenantID, Kind: netctl.KindSession, UserID: caller.UserID, Role: role,
	})
}

// RunNetSession, kimligi dogrulanmis bir WS baglantisini dugum olarak calistirir
// (testler ve ozel kapilar icin disa acik).
func (s *Server) RunNetSession(ctx context.Context, c *websocket.Conn, id netctl.Identity) {
	s.runNetSession(ctx, c, id)
}

func (s *Server) runNetSession(ctx context.Context, c *websocket.Conn, id netctl.Identity) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ob := netctl.NewOutbox()
	nc := s.Net.Attach(id, ob)
	defer nc.Close()

	go func() {
		_ = ob.Pump(ctx, func(ctx context.Context, bin bool, b []byte) error {
			wctx, wc := context.WithTimeout(ctx, 10*time.Second)
			defer wc()
			typ := websocket.MessageText
			if bin {
				typ = websocket.MessageBinary
			}
			return c.Write(wctx, typ, b)
		})
		cancel()
		c.Close(websocket.StatusTryAgainLater, "net queue")
	}()
	go func() { // canlilik: olu baglantiyi tespit et
		tk := time.NewTicker(25 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				pctx, pc := context.WithTimeout(ctx, 10*time.Second)
				err := c.Ping(pctx)
				pc()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if f, err := protocol.DecodeBodyFrame(data); err == nil && f.FrameType == protocol.FrameWG {
				nc.Relay(f.ReqID, f.Payload)
			}
			continue
		}
		t, err := protocol.PeekType(data)
		if err != nil {
			continue
		}
		switch t {
		case protocol.TypeNetRegister, protocol.TypeNetResync:
			_ = nc.HandleControl(ctx, t, data)
		}
	}
}
