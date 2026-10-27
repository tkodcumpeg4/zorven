package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/mcpconf"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// MCPProbeStep, "baglantiyi sina" adimlarindan birinin sonucu.
type MCPProbeStep struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	MS     int64  `json:"ms,omitempty"`
	Error  string `json:"error,omitempty"`
}

// MCPProbeResult, sunucu tarafinda yapilan MCP sinamasinin sonucu.
type MCPProbeResult struct {
	Enabled         bool         `json:"enabled"`
	AgentOnline     bool         `json:"agent_online"`
	Initialize      MCPProbeStep `json:"initialize"`
	ToolsList       MCPProbeStep `json:"tools_list"`
	ToolCount       int          `json:"tool_count"`
	ToolNames       []string     `json:"tool_names,omitempty"`
	ProtocolVersion string       `json:"protocol_version,omitempty"`
	ServerName      string       `json:"server_name,omitempty"`
	Stream          string       `json:"response_type,omitempty"` // json | sse
}

// routeByTunnel, tunelin bir yonlendirme kaydini doner (nadir kullanim: tarama).
func (r *Router) routeByTunnel(tunnelID string) (hr struct {
	ClientIDs []string
	OK        bool
}) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, rt := range r.byHost {
		if rt.TunnelID == tunnelID {
			hr.ClientIDs, hr.OK = candidateClients(rt), true
			return
		}
	}
	for _, rt := range r.wild {
		if rt.TunnelID == tunnelID {
			hr.ClientIDs, hr.OK = candidateClients(rt), true
			return
		}
	}
	return
}

// MCPProbe, tunelin MCP sunucusuna ajan uzerinden initialize + tools/list gonderir.
// AG CIKISI YOKTUR (SSRF'siz): istek dogrudan tunel oturumuna yazilir; hedef,
// tunelin kendi ajaninin yerel hizmetidir. Kimlik basligi eklenmez.
func (h *Handler) MCPProbe(ctx context.Context, tunnelID string) MCPProbeResult {
	var res MCPProbeResult
	rt, ok := h.Router.mcpFor(tunnelID)
	if !ok {
		return res
	}
	res.Enabled = true
	info := h.Router.routeByTunnel(tunnelID)
	var sess *tunnel.Session
	if info.OK {
		for _, cid := range info.ClientIDs {
			if s, ok := h.Hub.Get(cid); ok {
				sess = s
				break
			}
		}
	}
	if sess == nil {
		res.Initialize.Error = "ajan bagli degil"
		return res
	}
	res.AgentOnline = true
	path := rt.cfg.Path

	call := func(body string, hdr map[string][]string) (status int, rh map[string][]string, resp []byte, ms int64, err string) {
		start := time.Now()
		cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		h2 := map[string][]string{
			"Content-Type":    {"application/json"},
			"Accept":          {"application/json, text/event-stream"},
			"Accept-Encoding": {"identity"},
			"User-Agent":      {"zorven-mcp-probe/1"},
			"X-Zorven-Probe":  {"1"},
		}
		for k, v := range hdr {
			h2[k] = v
		}
		ex, e := sess.SendRequest(cctx, protocol.HTTPRequest{TunnelID: tunnelID, Method: "POST", Path: path,
			Headers: h2, HasBody: true, ContentLength: int64(len(body)), RemoteAddr: "127.0.0.1"})
		if e != nil {
			return 0, nil, nil, 0, "istek gonderilemedi"
		}
		defer sess.FinishExchange(ex.ReqID())
		if e := sess.StreamRequestBody(cctx, ex.ReqID(), io.NopCloser(strings.NewReader(body))); e != nil {
			return 0, nil, nil, 0, "govde gonderilemedi"
		}
		select {
		case <-cctx.Done():
			sess.SendCancel(context.Background(), ex.ReqID(), "probe_timeout")
			return 0, nil, nil, time.Since(start).Milliseconds(), "zaman asimi"
		case head := <-ex.Head():
			if head.ErrCode != "" {
				return 0, nil, nil, time.Since(start).Milliseconds(), "hedef servise ulasilamadi"
			}
			var buf bytes.Buffer
			done := make(chan struct{})
			go func() {
				defer close(done)
				if head.HasBody {
					_, _ = io.Copy(&buf, io.LimitReader(ex.Body(), 512<<10))
				}
			}()
			select {
			case <-done:
			case <-cctx.Done():
				sess.SendCancel(context.Background(), ex.ReqID(), "probe_timeout")
				<-done
			}
			return head.Status, head.Headers, buf.Bytes(), time.Since(start).Milliseconds(), ""
		}
	}

	hv := func(h map[string][]string, k string) string {
		for kk, v := range h {
			if strings.EqualFold(kk, k) && len(v) > 0 {
				return v[0]
			}
		}
		return ""
	}
	// extract, JSON veya SSE yanit govdesinden id'ye ait JSON-RPC mesajini bulur.
	extract := func(h map[string][]string, body []byte, id string) (json.RawMessage, string) {
		ct := strings.ToLower(hv(h, "Content-Type"))
		if strings.HasPrefix(ct, "text/event-stream") {
			res.Stream = "sse"
			buf := append(append([]byte(nil), body...), '\n', '\n')
			for {
				ev, r2, ok := splitSSE(buf)
				if !ok {
					break
				}
				buf = r2
				data, _, has := sseData(ev)
				if has {
					if ri := probeResp(data); ri.hasID && ri.id == id {
						return data, ""
					}
				}
			}
			return nil, "yanit olayi bulunamadi"
		}
		res.Stream = "json"
		b := bytes.TrimSpace(body)
		if len(b) > 0 && b[0] == '[' {
			var items []json.RawMessage
			if json.Unmarshal(b, &items) == nil {
				for _, it := range items {
					if ri := probeResp(it); ri.hasID && ri.id == id {
						return it, ""
					}
				}
			}
			return nil, "yanit bulunamadi"
		}
		return b, ""
	}

	st, rh, body, ms, errMsg := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"zorven-probe","version":"1"}}}`, nil)
	res.Initialize = MCPProbeStep{Status: st, MS: ms, Error: errMsg}
	if errMsg != "" {
		return res
	}
	if st < 200 || st >= 300 {
		res.Initialize.Error = "beklenmeyen HTTP durumu"
		return res
	}
	raw, e := extract(rh, body, "1")
	if e != "" {
		res.Initialize.Error = e
		return res
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if json.Unmarshal(raw, &init) != nil || init.Error != nil || init.Result.ProtocolVersion == "" {
		res.Initialize.Error = "gecersiz initialize yaniti"
		return res
	}
	res.Initialize.OK = true
	res.ProtocolVersion = sanitizeHeaderValue(init.Result.ProtocolVersion, 32)
	res.ServerName = sanitizeHeaderValue(init.Result.ServerInfo.Name, 64)

	hdr := map[string][]string{"MCP-Protocol-Version": {init.Result.ProtocolVersion}}
	if sid := hv(rh, "Mcp-Session-Id"); sid != "" {
		hdr["Mcp-Session-Id"] = []string{sid}
	}
	_, _, _, _, _ = call(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, hdr)

	st, rh, body, ms, errMsg = call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, hdr)
	res.ToolsList = MCPProbeStep{Status: st, MS: ms, Error: errMsg}
	if errMsg != "" {
		return res
	}
	raw, e = extract(rh, body, "2")
	if e != "" {
		res.ToolsList.Error = e
		return res
	}
	var tl struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	if json.Unmarshal(raw, &tl) != nil || tl.Error != nil {
		res.ToolsList.Error = "gecersiz tools/list yaniti"
		return res
	}
	res.ToolsList.OK = true
	for _, t := range tl.Result.Tools {
		res.ToolCount++
		if len(res.ToolNames) < 50 {
			res.ToolNames = append(res.ToolNames, sanitizeHeaderValue(t.Name, 64))
		}
	}
	return res
}

var _ = mcpconf.AuthOAuth
