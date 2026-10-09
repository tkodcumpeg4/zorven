package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInstallScripts(t *testing.T) {
	srv := &Server{}
	mux := srv.Routes()

	// 1. Linux install.sh
	reqSh := httptest.NewRequest("GET", "/install.sh?token=zrv_live_abc123&server=custom.domain:8443", nil)
	recSh := httptest.NewRecorder()
	mux.ServeHTTP(recSh, reqSh)

	if recSh.Code != http.StatusOK {
		t.Fatalf("install.sh beklenen durum 200, alinan %d", recSh.Code)
	}
	bodySh := recSh.Body.String()
	if !strings.Contains(bodySh, `INJECTED_TOKEN='zrv_live_abc123'`) {
		t.Errorf("install.sh enjekte edilen token'i icermiyor:\n%s", bodySh[:200])
	}
	if !strings.Contains(bodySh, `INJECTED_SERVER='custom.domain:8443'`) {
		t.Errorf("install.sh enjekte edilen sunucuyu icermiyor")
	}

	// 2. Windows install.ps1
	reqPs := httptest.NewRequest("GET", "/install.ps1?token=zrv_live_xyz789&server=custom.domain:8443", nil)
	recPs := httptest.NewRecorder()
	mux.ServeHTTP(recPs, reqPs)

	if recPs.Code != http.StatusOK {
		t.Fatalf("install.ps1 beklenen durum 200, alinan %d", recPs.Code)
	}
	bodyPs := recPs.Body.String()
	if !strings.Contains(bodyPs, `$InjectedToken = 'zrv_live_xyz789'`) {
		t.Errorf("install.ps1 enjekte edilen token'i icermiyor:\n%s", bodyPs[:200])
	}
	if !strings.Contains(bodyPs, `$InjectedServer = 'custom.domain:8443'`) {
		t.Errorf("install.ps1 enjekte edilen sunucuyu icermiyor")
	}
}

func TestInstallScriptsRejectInjection(t *testing.T) {
	mux := (&Server{}).Routes()
	bad := []string{
		"$(id)", "`id`", "\"x", "x'y", "x;id", "x\ny", "x y", "x|id", "x&id", "$env:TEMP", "x\x60n", "${IFS}",
	}
	for _, path := range []string{"/install.sh", "/install.ps1"} {
		for _, b := range bad {
			for _, q := range []string{
				"token=" + url.QueryEscape("zrv_live_"+b),
				"token=" + url.QueryEscape(b),
				"server=" + url.QueryEscape("host.com"+b),
				"server=" + url.QueryEscape(b),
			} {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest("GET", path+"?"+q, nil))
				if rec.Code != http.StatusBadRequest {
					t.Errorf("%s?%s: beklenen 400, alinan %d", path, q, rec.Code)
				}
			}
		}
		for _, srv := range []string{"host:0", "host:65536", "host:99999", "-bad.com", "a..b:80x"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", path+"?server="+url.QueryEscape(srv), nil))
			if srv != "a..b:80x" && rec.Code != http.StatusBadRequest {
				t.Errorf("%s server=%s: beklenen 400, alinan %d", path, srv, rec.Code)
			}
			if srv == "a..b:80x" && rec.Code != http.StatusBadRequest {
				t.Errorf("server=%s 400 olmali", srv)
			}
		}
		// bos parametre: varsayilan davranis (200)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s bos parametre: beklenen 200, alinan %d", path, rec.Code)
		}
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path+"?server=host.example:65535&token=rpsh_live_ab-_9", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s gecerli: beklenen 200, alinan %d", path, rec.Code)
		}
	}
}
