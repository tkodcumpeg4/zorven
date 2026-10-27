package ingress

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MCP JSON-RPC incelemesi. Amac: istekten yalnizca yonlendirme/politika icin
// gereken alanlari (method, id, params.name) cikarmak. Ayrıstırıcı KATI'dir:
// politika kararinda kullanilan alanlarin yinelenmesi veya buyuk/kucuk harf
// farkiyla belirsizlestirilmesi (ornegin "name" ve "NAME") hata sayilir; cunku
// Go tabanli MCP sunuculari alan adlarini buyuk/kucuk harf duyarsiz eslestirir,
// biz farkli bir degeri gorursek politika atlatilabilirdi (fail-closed).

var (
	errRPCParse     = errors.New("json-rpc govdesi ayristirilamadi")
	errRPCAmbiguous = errors.New("json-rpc alani belirsiz (yinelenen veya buyuk/kucuk harf farkli anahtar)")
)

// rpcMsg, tek bir JSON-RPC mesaji (istek veya bildirim) hakkinda cikarilan alanlar.
type rpcMsg struct {
	Raw    json.RawMessage
	ID     string // normalize edilmis ham id ("1", "\"abc\""); bos = bildirim
	HasID  bool
	Method string
	Tool   string // tools/call icin params.name
	// ToolOK, tools/call'da params.name'in bir dize olarak okunabildigi.
	ToolOK bool
	// URI, resources/read icin yalniz sema+host (log).
	URI string
	// Response, mesaj bir YANIT (result/error) mi (istemci -> sunucu yaniti).
	Response bool
}

// relevantTop, ust duzeyde buyuk/kucuk harf duyarsiz tekillik aranan anahtarlar.
var relevantTop = map[string]bool{"jsonrpc": true, "id": true, "method": true, "params": true, "result": true, "error": true}
var relevantParams = map[string]bool{"name": true, "uri": true, "arguments": true}

// parseRPC, govdeyi (tek nesne veya dizi) ayristirir.
func parseRPC(body []byte) (msgs []rpcMsg, batch bool, err error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, false, errRPCParse
	}
	switch body[0] {
	case '{':
		m, err := parseRPCObject(body)
		if err != nil {
			return nil, false, err
		}
		return []rpcMsg{m}, false, nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, true, errRPCParse
		}
		if len(items) == 0 || len(items) > 100 {
			return nil, true, errRPCParse
		}
		for _, it := range items {
			it = bytes.TrimSpace(it)
			if len(it) == 0 || it[0] != '{' {
				return nil, true, errRPCParse
			}
			m, err := parseRPCObject(it)
			if err != nil {
				return nil, true, err
			}
			msgs = append(msgs, m)
		}
		return msgs, true, nil
	}
	return nil, false, errRPCParse
}

// objectEntries, bir JSON nesnesinin ust duzey anahtar/deger ciftlerini sirayla
// verir (yinelenenler dahil). Degerler ham tutulur.
func objectEntries(raw []byte) ([]string, []json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, errRPCParse
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, errRPCParse
	}
	var keys []string
	var vals []json.RawMessage
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, errRPCParse
		}
		k, ok := kt.(string)
		if !ok {
			return nil, nil, errRPCParse
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, errRPCParse
		}
		keys = append(keys, k)
		vals = append(vals, v)
	}
	if _, err := dec.Token(); err != nil { // kapanis '}'
		return nil, nil, errRPCParse
	}
	if _, err := dec.Token(); err != io.EOF { // govdede artik veri olmamali
		return nil, nil, errRPCParse
	}
	return keys, vals, nil
}

// pick, anahtarlari buyuk/kucuk harf duyarsiz tekillik icin tarar; ilgili
// anahtarlar tam kucuk harf ve tek olmali.
func pick(keys []string, vals []json.RawMessage, relevant map[string]bool) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for i, k := range keys {
		lk := strings.ToLower(k)
		if !relevant[lk] {
			continue
		}
		if k != lk {
			return nil, errRPCAmbiguous
		}
		if _, dup := out[lk]; dup {
			return nil, errRPCAmbiguous
		}
		out[lk] = vals[i]
	}
	return out, nil
}

func parseRPCObject(raw []byte) (rpcMsg, error) {
	keys, vals, err := objectEntries(raw)
	if err != nil {
		return rpcMsg{}, err
	}
	top, err := pick(keys, vals, relevantTop)
	if err != nil {
		return rpcMsg{}, err
	}
	m := rpcMsg{Raw: append(json.RawMessage(nil), raw...)}
	if id, ok := top["id"]; ok {
		s := strings.TrimSpace(string(id))
		if s != "null" {
			m.ID, m.HasID = s, true
		}
	}
	if _, ok := top["result"]; ok {
		m.Response = true
	}
	if _, ok := top["error"]; ok && top["method"] == nil {
		m.Response = true
	}
	if mv, ok := top["method"]; ok {
		var method string
		if json.Unmarshal(mv, &method) != nil {
			return rpcMsg{}, errRPCParse
		}
		m.Method = method
	}
	pv, hasParams := top["params"]
	if !hasParams || m.Method == "" {
		return m, nil
	}
	pt := bytes.TrimSpace(pv)
	if len(pt) == 0 || pt[0] != '{' {
		return m, nil
	}
	pk, pvals, err := objectEntries(pt)
	if err != nil {
		return rpcMsg{}, err
	}
	params, err := pick(pk, pvals, relevantParams)
	if err != nil {
		return rpcMsg{}, err
	}
	switch m.Method {
	case "tools/call":
		if nv, ok := params["name"]; ok {
			var name string
			if json.Unmarshal(nv, &name) == nil {
				m.Tool, m.ToolOK = name, true
			}
		}
	case "prompts/get":
		if nv, ok := params["name"]; ok {
			_ = json.Unmarshal(nv, &m.Tool)
		}
	case "resources/read":
		if uv, ok := params["uri"]; ok {
			var u string
			if json.Unmarshal(uv, &u) == nil {
				m.URI = uriSchemeHost(u)
			}
		}
	}
	return m, nil
}

// uriSchemeHost, URI'nin yalniz sema+host kismini doner (log gizliligi).
func uriSchemeHost(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		if i := strings.Index(u, ":"); i > 0 && i < 16 {
			return u[:i] + ":"
		}
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	if i := strings.IndexAny(host, "?#"); i >= 0 {
		host = host[:i]
	}
	if len(host) > 80 {
		host = host[:80]
	}
	return scheme + "://" + host
}

// rpcDenyBody, reddedilen istek icin JSON-RPC hata yaniti. Mesaj aracin adini
// ICERMEZ (model ezberleme/bilgi sizdirmasi riskini azaltmak icin sabit metin);
// ad yalniz data.tool alaninda geri verilir (istemcinin kendi gonderdigi deger).
func rpcDenyBody(id string, tool, reason string) []byte {
	idRaw := json.RawMessage("null")
	if id != "" {
		idRaw = json.RawMessage(id)
	}
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      idRaw,
		"error": map[string]any{
			"code":    -32602,
			"message": "Bu arac yonetici politikasiyla kapali",
			"data":    map[string]any{"tool": tool, "reason": reason},
		},
	})
	return b
}

func rpcErrorBody(id string, code int, msg, reason string) []byte {
	idRaw := json.RawMessage("null")
	if id != "" {
		idRaw = json.RawMessage(id)
	}
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      idRaw,
		"error":   map[string]any{"code": code, "message": msg, "data": map[string]any{"reason": reason}},
	})
	return b
}

// rewriteBatch, batch'ten reddedilen ogeleri cikarip kalanlari yeni bir dizi
// olarak birlestirir.
func rewriteBatch(msgs []rpcMsg, keep []bool) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	first := true
	for i, m := range msgs {
		if !keep[i] {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.Write(m.Raw)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

func (m rpcMsg) String() string { return fmt.Sprintf("%s/%s", m.Method, m.Tool) }
