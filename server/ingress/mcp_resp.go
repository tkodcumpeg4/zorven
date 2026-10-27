package ingress

// MCP yanit isleme: log icin gozlem, tools/list suzme (JSON ve SSE olay olay),
// reddedilen batch ogeleri icin sentetik hata yanitlarini ekleme.
//
// Tasarim ilkeleri:
//   - Suzme/ekleme GEREKMIYORSA yanit baytlari DEGISTIRILMEDEN ve tamponlanmadan
//     (her yazimda flush) akar; gozlemci yalnizca bir kopya uzerinde calisir.
//   - Suzme GEREKIYORSA (arac politikasi + tools/list, veya sentetik yanit):
//     JSON yanit tamamen tamponlanir (kisa), SSE yanit olay-olay tutulup
//     yeniden yazilir; suzme basarisiz olursa fail-closed (502), asla suzulmemis
//     liste sizdirilmaz.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	mcpObsEventCap   = 64 << 10 // gozlemlenen tek SSE olayi tavani
	mcpJSONObsCap    = 256 << 10
	mcpHoldCap       = 1 << 20 // suzulen SSE olayi tavani
	mcpJSONBufferCap = 8 << 20 // suzulen JSON yanit tavani
)

type respMode int

const (
	modePass respMode = iota
	modeSSEHold
	modeJSONBuf
	modeDead
)

// mcpWriter, yaniti saran http.ResponseWriter.
type mcpWriter struct {
	http.ResponseWriter
	m *mcpReq

	hdrDone bool
	code    int
	mode    respMode
	written int64

	// gozlem (modePass)
	obs       []byte // SSE olay gozlemi
	jobs      []byte // JSON yanit gozlemi
	jobsTrunc bool

	// suzme
	pend []byte // modeSSEHold: tamamlanmamis olay
	jbuf []byte // modeJSONBuf
	sse  bool
}

func newMCPWriter(w http.ResponseWriter, m *mcpReq) *mcpWriter {
	return &mcpWriter{ResponseWriter: w, m: m}
}

func (mw *mcpWriter) needFilter() bool {
	return len(mw.m.listIDs) > 0 || len(mw.m.extras) > 0
}

func (mw *mcpWriter) WriteHeader(code int) {
	if mw.hdrDone {
		return
	}
	mw.hdrDone = true
	mw.code = code
	hd := mw.ResponseWriter.Header()
	mw.m.setSession(hd.Get("Mcp-Session-Id"))
	ct := strings.ToLower(hd.Get("Content-Type"))
	mw.sse = strings.HasPrefix(ct, "text/event-stream")
	if mw.sse {
		hd.Set("X-Accel-Buffering", "no")
		hd.Set("Cache-Control", "no-cache, no-transform")
		hd.Del("Content-Length")
	}
	if code >= 100 && code < 200 {
		mw.hdrDone = false
		mw.ResponseWriter.WriteHeader(code)
		return
	}
	// Eski HTTP+SSE: POST'a verilen 202, sonuc ayri akista; izlenemez -> kabul edildi say.
	if mw.m.legacy && code == http.StatusAccepted {
		for _, c := range mw.m.order {
			if !c.done {
				c.done = true
				mw.m.logCall(c, "ok", 0, 0, 0)
			}
		}
	}

	if mw.needFilter() && code < 300 || (len(mw.m.extras) > 0 && (code == http.StatusAccepted || code == http.StatusNoContent)) {
		enc := strings.ToLower(strings.TrimSpace(hd.Get("Content-Encoding")))
		compressed := enc != "" && enc != "identity"
		jsonish := strings.HasPrefix(ct, "application/json") || strings.HasSuffix(strings.SplitN(ct, ";", 2)[0], "+json")
		noBody := code == http.StatusAccepted || code == http.StatusNoContent
		switch {
		case (mw.sse || jsonish) && compressed:
			// Suzulemez: fail-closed.
			mw.fail("MCP yaniti sikistirilmis; arac listesi suzulemedi.")
			return
		case mw.sse:
			mw.mode = modeSSEHold
			mw.ResponseWriter.WriteHeader(code)
			mw.emitExtrasSSE()
			return
		case jsonish || (noBody && len(mw.m.extras) > 0):
			mw.mode = modeJSONBuf
			hd.Del("Content-Length")
			hd.Del("Content-Encoding")
			return // baslik finish'te yazilir
		}
	}
	mw.ResponseWriter.WriteHeader(code)
}

// fail, suzulemeyen yanit yerine 502 yazar ve gerisini yutar.
func (mw *mcpWriter) fail(msg string) {
	hd := mw.ResponseWriter.Header()
	for k := range hd {
		delete(hd, k)
	}
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "no-store")
	mw.mode = modeDead
	mw.ResponseWriter.WriteHeader(http.StatusBadGateway)
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "mcp_filter_failed", "message": msg}})
	_, _ = mw.ResponseWriter.Write(b)
}

func (mw *mcpWriter) emitExtrasSSE() {
	for _, e := range mw.m.extras {
		_, _ = mw.ResponseWriter.Write(append(append([]byte("event: message\ndata: "), e...), '\n', '\n'))
	}
	mw.m.extras = nil
	if f, ok := mw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (mw *mcpWriter) Write(p []byte) (int, error) {
	if !mw.hdrDone {
		mw.WriteHeader(http.StatusOK)
	}
	n := len(p)
	mw.written += int64(n)
	switch mw.mode {
	case modeDead:
		return n, nil
	case modeJSONBuf:
		if len(mw.jbuf)+n > mcpJSONBufferCap {
			mw.fail("MCP yaniti cok buyuk; arac listesi suzulemedi.")
			return n, nil
		}
		mw.jbuf = append(mw.jbuf, p...)
		return n, nil
	case modeSSEHold:
		mw.pend = append(mw.pend, p...)
		for {
			ev, rest, ok := splitSSE(mw.pend)
			if !ok {
				break
			}
			out := mw.processEvent(ev)
			mw.pend = rest
			if _, err := mw.ResponseWriter.Write(out); err != nil {
				return n, err
			}
			if f, ok := mw.ResponseWriter.(http.Flusher); ok {
				f.Flush()
			}
		}
		if len(mw.pend) > mcpHoldCap {
			// Olay siniri asildi: suzemiyoruz; akisi kes.
			mw.pend = nil
			mw.mode = modeDead
			return n, nil
		}
		return n, nil
	}
	// modePass: oldugu gibi yaz, kopya uzerinde gozle.
	w, err := mw.ResponseWriter.Write(p)
	if w > 0 {
		mw.observe(p[:w])
	}
	return w, err
}

func (mw *mcpWriter) Flush() {
	if mw.mode == modeJSONBuf || mw.mode == modeDead {
		return
	}
	if f, ok := mw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap, http.ResponseController uyumu.
func (mw *mcpWriter) Unwrap() http.ResponseWriter { return mw.ResponseWriter }

// observe, gecen baytlari log icin izler (yanit degismez).
func (mw *mcpWriter) observe(p []byte) {
	if len(mw.m.calls) == 0 {
		return
	}
	if mw.sse {
		mw.obs = append(mw.obs, p...)
		for {
			ev, rest, ok := splitSSE(mw.obs)
			if !ok {
				break
			}
			if len(ev) <= mcpObsEventCap {
				mw.resolveEvent(ev)
			}
			mw.obs = rest
		}
		if len(mw.obs) > mcpObsEventCap*2 {
			mw.obs = nil
		}
		return
	}
	if !mw.jobsTrunc {
		if len(mw.jobs)+len(p) > mcpJSONObsCap {
			mw.jobsTrunc = true
			mw.jobs = nil
		} else {
			mw.jobs = append(mw.jobs, p...)
		}
	}
}

// --- SSE ---------------------------------------------------------------------

// splitSSE, tamponun basindaki ilk tam olayi (bos satira kadar) ayirir.
func splitSSE(buf []byte) (ev, rest []byte, ok bool) {
	i, lineStart := 0, 0
	for i < len(buf) {
		c := buf[i]
		if c != '\n' && c != '\r' {
			i++
			continue
		}
		j := i + 1
		if c == '\r' {
			if j < len(buf) && buf[j] == '\n' {
				j++
			} else if j == len(buf) {
				return nil, buf, false // "\r" sonu belirsiz: devamini bekle
			}
		}
		if i == lineStart { // bos satir
			if lineStart == 0 {
				buf = buf[j:] // bastaki bos satirlari atla
				i, lineStart = 0, 0
				continue
			}
			return buf[:lineStart], buf[j:], true
		}
		i, lineStart = j, j
	}
	return nil, buf, false
}

// sseData, olayin data satirlarini birlestirir; digerlerini ayri doner.
func sseData(ev []byte) (data []byte, other [][]byte, hasData bool) {
	var dl [][]byte
	for _, line := range splitLines(ev) {
		if v, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			v = bytes.TrimPrefix(v, []byte(" "))
			dl = append(dl, v)
			hasData = true
			continue
		}
		other = append(other, line)
	}
	return bytes.Join(dl, []byte("\n")), other, hasData
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' || b[i] == '\r' {
			out = append(out, b[start:i])
			if b[i] == '\r' && i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// processEvent, modeSSEHold'da bir olayi isler: suzulmesi gereken tools/list
// yanitini yeniden yazar, digerlerini oldugu gibi birakir; yanitlari loglar.
func (mw *mcpWriter) processEvent(ev []byte) []byte {
	orig := append(append([]byte(nil), ev...), '\n') // splitSSE bos satiri atar; yenisini ekle
	data, other, has := sseData(ev)
	if !has || len(data) == 0 {
		return orig
	}
	out, changed := mw.processPayload(data)
	if !changed {
		return orig
	}
	var b bytes.Buffer
	for _, l := range other {
		b.Write(l)
		b.WriteByte('\n')
	}
	b.WriteString("data: ")
	b.Write(out)
	b.WriteString("\n\n")
	return b.Bytes()
}

func (mw *mcpWriter) resolveEvent(ev []byte) {
	data, _, has := sseData(ev)
	if !has || len(data) == 0 {
		return
	}
	mw.resolvePayload(data)
}

// --- JSON-RPC yanit ogeleri --------------------------------------------------

type respInfo struct {
	id      string
	hasID   bool
	isResp  bool
	errCode int
	isErr   bool
	size    int64
}

func probeResp(raw []byte) respInfo {
	var p struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	ri := respInfo{size: int64(len(raw))}
	if json.Unmarshal(raw, &p) != nil || p.Method != "" {
		return ri
	}
	if id := strings.TrimSpace(string(p.ID)); id != "" && id != "null" {
		ri.id, ri.hasID = id, true
	}
	if p.Error != nil {
		ri.isResp, ri.isErr, ri.errCode = true, true, p.Error.Code
		if ri.errCode == 0 {
			ri.errCode = -1
		}
	} else if len(p.Result) > 0 {
		ri.isResp = true
		var r struct {
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(p.Result, &r) == nil && r.IsError {
			ri.isErr = true
		}
	}
	return ri
}

// resolvePayload, bir data yukunu (nesne veya dizi) gozlemler.
func (mw *mcpWriter) resolvePayload(data []byte) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return
	}
	if data[0] == '[' {
		var items []json.RawMessage
		if json.Unmarshal(data, &items) == nil {
			for _, it := range items {
				mw.resolveOne(it)
			}
		}
		return
	}
	mw.resolveOne(data)
}

func (mw *mcpWriter) resolveOne(raw []byte) {
	ri := probeResp(raw)
	if !ri.isResp || !ri.hasID {
		return
	}
	c := mw.m.calls[ri.id]
	if c == nil || c.done {
		return
	}
	c.done = true
	status := "ok"
	if ri.isErr {
		status = "error"
	}
	mw.m.logCall(c, status, ri.errCode, time.Since(c.start).Milliseconds(), ri.size)
}

// processPayload, suzme gereken yanitlari yeniden yazar.
func (mw *mcpWriter) processPayload(data []byte) ([]byte, bool) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return data, false
	}
	if data[0] == '[' {
		var items []json.RawMessage
		if json.Unmarshal(data, &items) != nil {
			return data, false
		}
		changed := false
		for i, it := range items {
			if out, ch := mw.processOne(it); ch {
				items[i], changed = out, true
			}
		}
		if !changed {
			return data, false
		}
		b, _ := json.Marshal(items)
		return b, true
	}
	return mw.processOne(data)
}

func (mw *mcpWriter) processOne(raw []byte) ([]byte, bool) {
	mw.resolveOne(raw)
	ri := probeResp(raw)
	if !ri.isResp || !ri.hasID || !mw.m.listIDs[ri.id] {
		return raw, false
	}
	failBody := func() ([]byte, bool) {
		return rpcErrorBody(ri.id, -32603, "Arac listesi suzulemedi", "mcp_filter_failed"), true
	}
	if ri.isErr {
		return raw, false // hata yaniti: liste yok
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return failBody()
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(obj["result"], &res) != nil {
		return failBody()
	}
	toolsRaw, ok := res["tools"]
	if !ok {
		return failBody()
	}
	var tools []json.RawMessage
	if json.Unmarshal(toolsRaw, &tools) != nil {
		return failBody()
	}
	kept := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		var nm struct {
			Name *string `json:"name"`
		}
		if json.Unmarshal(t, &nm) != nil || nm.Name == nil {
			continue // adsiz/okunamayan arac: fail-closed (listeden dusur)
		}
		if mw.m.rt.cfg.ToolAllowed(*nm.Name) {
			kept = append(kept, t)
		}
	}
	res["tools"], _ = json.Marshal(kept)
	obj["result"], _ = json.Marshal(res)
	b, _ := json.Marshal(obj)
	return b, true
}

// finish, yanit bittiginde tamponu bosaltir, kalan cagrilari kapatir.
// ServeHTTP tarafindan her durumda (defer) cagrilir.
func (mw *mcpWriter) finish() {
	defer mw.m.release()
	if !mw.hdrDone {
		// Yanit hic yazilmadi (istemci koptu vb.).
		mw.closeCalls("error", -1)
		return
	}
	switch mw.mode {
	case modeJSONBuf:
		out := mw.finishJSON()
		mw.ResponseWriter.Header().Set("Content-Length", itoa(len(out)))
		code := mw.code
		if len(out) > 0 && (code == http.StatusAccepted || code == http.StatusNoContent) {
			code = http.StatusOK
			mw.ResponseWriter.Header().Set("Content-Type", "application/json")
		}
		mw.ResponseWriter.WriteHeader(code)
		_, _ = mw.ResponseWriter.Write(out)
	case modePass:
		if !mw.sse && !mw.jobsTrunc && len(mw.jobs) > 0 {
			mw.resolvePayload(mw.jobs)
		}
	}
	st, ec := "ok", 0
	if mw.code >= 400 {
		st, ec = "error", -mw.code
	}
	if mw.m.stream {
		// GET akisi tek satir: sure ve bayt.
		mw.m.logCall(&mcpCall{method: "stream", start: mw.m.started}, "ok", 0, time.Since(mw.m.started).Milliseconds(), mw.written)
	}
	mw.closeCalls(st, ec)
}

func (mw *mcpWriter) closeCalls(st string, ec int) {
	for _, c := range mw.m.order {
		if !c.done {
			c.done = true
			mw.m.logCall(c, st, ec, time.Since(c.start).Milliseconds(), 0)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// finishJSON, tamponlanmis JSON yaniti suzer, sentetik yanitlari ekler.
func (mw *mcpWriter) finishJSON() []byte {
	body := bytes.TrimSpace(mw.jbuf)
	extras := mw.m.extras
	mw.m.extras = nil
	if len(body) == 0 {
		if len(extras) == 0 {
			return nil
		}
		return append(append([]byte{'['}, bytes.Join(extras, []byte{','})...), ']')
	}
	if body[0] == '[' {
		var items []json.RawMessage
		if json.Unmarshal(body, &items) != nil {
			return mw.failJSON()
		}
		for i, it := range items {
			if out, ch := mw.processOne(it); ch {
				items[i] = out
			}
		}
		for _, e := range extras {
			items = append(items, e)
		}
		b, _ := json.Marshal(items)
		return b
	}
	out, _ := mw.processOne(body)
	if len(extras) == 0 {
		return out
	}
	items := []json.RawMessage{out}
	for _, e := range extras {
		items = append(items, e)
	}
	b, _ := json.Marshal(items)
	return b
}

func (mw *mcpWriter) failJSON() []byte {
	mw.code = http.StatusBadGateway
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "mcp_filter_failed", "message": "MCP yaniti ayristirilamadi."}})
	return b
}
