package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A5 #7: tunel istegine kimlik. Hedefe X-Request-Id + X-Zorven-Request-Id gider,
// ziyaretciye X-Zorven-Request-Id doner; hedefin kendi X-Request-Id'si ezilmez;
// request_logs kimligi Zorven kimligiyle ayni.
func TestTunnelRequestIDPropagation(t *testing.T) {
	a, dc, h := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	type seen struct{ fwd, zid string }
	got := make(chan seen, 1)
	respond := func(hdr map[string][]string) {
		req := a.nextReq(t)
		hh := http.Header(req.Headers)
		got <- seen{hh.Get("X-Request-Id"), hh.Get("X-Zorven-Request-Id")}
		a.head(t, req.ReqID, hdr)
		a.body(t, req.ReqID, []byte("ok"), true)
	}
	do := func(rid string) *http.Response {
		r := visitorReq(t, context.Background(), srv.URL+"/x", "GET", nil)
		if rid != "" {
			r.Header.Set("X-Request-Id", rid)
		}
		r.Header.Set("X-Zorven-Request-Id", "sahte")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}

	// 1) Gecerli ziyaretci kimligi korunur; hedef X-Request-Id yollamaz -> yankilanir.
	go respond(map[string][]string{"Content-Type": {"text/plain"}})
	resp := do("musteri-1")
	s := <-got
	zid := resp.Header.Get("X-Zorven-Request-Id")
	if s.fwd != "musteri-1" || !strings.HasPrefix(s.zid, "req_") || s.zid == "sahte" {
		t.Fatalf("hedef basliklari: %+v", s)
	}
	if zid != s.zid || resp.Header.Get("X-Request-Id") != "musteri-1" {
		t.Fatalf("ziyaretci basliklari: zid=%q rid=%q (hedef zid %q)", zid, resp.Header.Get("X-Request-Id"), s.zid)
	}
	// request_logs kimligi ayni.
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		for _, e := range h.ReqLog.List(100, "") {
			if e.ID == zid {
				found = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatalf("request_logs kimligi %q ile eslesmedi", zid)
	}

	// 2) Gecersiz ziyaretci kimligi hedefe gitmez (Zorven kimligi gider);
	//    hedefin kendi X-Request-Id'si ziyaretciye aynen doner.
	go respond(map[string][]string{"X-Request-Id": {"uygulama-77"}})
	resp = do("evil-<script>")
	s = <-got
	if s.fwd != s.zid || strings.Contains(s.fwd, "<") {
		t.Fatalf("gecersiz kimlik hedefe gitti: %+v", s)
	}
	if resp.Header.Get("X-Request-Id") != "uygulama-77" || resp.Header.Get("X-Zorven-Request-Id") != s.zid {
		t.Fatalf("uygulamanin kimligi ezildi: %v", resp.Header)
	}
}
