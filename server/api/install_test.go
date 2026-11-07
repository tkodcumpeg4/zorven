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

func TestInstallScriptsHardening(t *testing.T) {
	mux := (&Server{}).Routes()
	get := func(path string) string {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "zorven.example"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control no-store olmali", path)
		}
		return rec.Body.String()
	}
	sh := get("/install.sh?token=zrv_live_abc")
	ps := get("/install.ps1?token=zrv_live_abc")

	for name, body := range map[string]string{"sh": sh, "ps1": ps} {
		if strings.Contains(body, "__INJECTED_TOKEN__") || strings.Contains(body, "__INJECTED_SERVER__") {
			t.Errorf("%s: sentinel degistirilmemis", name)
		}
		if strings.Contains(body, "localhost") || strings.Contains(body, "127.0.0.1") {
			t.Errorf("%s: localhost varsayilani bulunmamali", name)
		}
		if !strings.Contains(body, "zorven.example") {
			t.Errorf("%s: sunucu adresi enjekte edilmemis", name)
		}
		if !strings.Contains(body, "--token-file") {
			t.Errorf("%s: token --token-file ile verilmeli", name)
		}
		if strings.Contains(body, "config set-token") {
			t.Errorf("%s: token komut satirina yazilmamali", name)
		}
	}
	if strings.Contains(sh, "curl -kfsSL") || strings.Contains(sh, "CURL_FLAGS") {
		t.Error("sh: kosulsuz curl -k olmamali")
	}
	if !strings.Contains(sh, "--no-check-certificate") || !strings.Contains(sh, `[ "$INSECURE" = true ]`) {
		t.Error("sh: TLS atlama yalniz INSECURE kosuluyla olmali")
	}
	if strings.Contains(ps, "StartsWith(\"localhost\")") {
		t.Error("ps1: localhost icin otomatik TLS atlama olmamali")
	}
	if !strings.Contains(ps, "if ($skipTls)") || !strings.Contains(ps, "$prevCb") {
		t.Error("ps1: TLS atlama kapsamli (geri alinan) olmali")
	}
	if !strings.Contains(sh, "sha256_of") || !strings.Contains(sh, "SHA-256 uyusmuyor") || !strings.Contains(sh, "manifest.json") {
		t.Error("sh: sha256 dogrulamasi eksik")
	}
	if !strings.Contains(ps, "Get-FileHash") || !strings.Contains(ps, "SHA-256 uyusmuyor") {
		t.Error("ps1: sha256 dogrulamasi eksik")
	}
	if !strings.Contains(sh, "mv -f") || !strings.Contains(sh, "--uninstall") || !strings.Contains(sh, "--purge") {
		t.Error("sh: atomik mv / uninstall eksik")
	}
	if !strings.Contains(ps, "Move-Item -Force") || !strings.Contains(ps, "$Uninstall") || !strings.Contains(ps, "-Verb RunAs") {
		t.Error("ps1: Move-Item / Uninstall / UAC yukseltme eksik")
	}
	if strings.Contains(sh, "|| true\n$SUDO \"$INSTALL_PATH\" service") || strings.Contains(sh, "service install || true") || strings.Contains(sh, "service start || true") {
		t.Error("sh: servis hatalari gizlenmemeli")
	}
	if strings.Contains(ps, `-notlike "*$installDir*"`) {
		t.Error("ps1: PATH alt-dizge karsilastirmasi olmamali")
	}
	if !strings.Contains(ps, "32-bit Windows desteklenmiyor") {
		t.Error("ps1: 386 acik hata vermeli")
	}
	if strings.Contains(sh, "#!/bin/bash") || strings.Contains(sh, "[[") {
		t.Error("sh: POSIX sh uyumlu olmali")
	}
}

func TestInstallScriptsNoHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/install.sh", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	(&Server{}).Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bos Host: beklenen 400, alinan %d", rec.Code)
	}
}
