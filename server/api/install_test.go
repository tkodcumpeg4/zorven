package api

import (
	"net/http"
	"net/http/httptest"
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
	if !strings.Contains(bodySh, `INJECTED_TOKEN="zrv_live_abc123"`) {
		t.Errorf("install.sh enjekte edilen token'i icermiyor:\n%s", bodySh[:200])
	}
	if !strings.Contains(bodySh, `INJECTED_SERVER="custom.domain:8443"`) {
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
	if !strings.Contains(bodyPs, `$InjectedToken = "zrv_live_xyz789"`) {
		t.Errorf("install.ps1 enjekte edilen token'i icermiyor:\n%s", bodyPs[:200])
	}
	if !strings.Contains(bodyPs, `$InjectedServer = "custom.domain:8443"`) {
		t.Errorf("install.ps1 enjekte edilen sunucuyu icermiyor")
	}
}
