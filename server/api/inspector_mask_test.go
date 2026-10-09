package api

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

func TestMaskSensitiveHeaders(t *testing.T) {
	in := map[string][]string{
		"Authorization": {"Bearer abc"},
		"Cookie":        {"sid=1"},
		"Set-Cookie":    {"a=1", "b=2"},
		"X-Api-Key":     {"k"},
		"Content-Type":  {"text/plain"},
	}
	out := maskSensitiveHeaders(in)
	for _, k := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		if len(out[k]) != 1 || out[k][0] != maskedHeaderValue {
			t.Errorf("%s maskelenmedi: %v", k, out[k])
		}
	}
	if len(out["Set-Cookie"]) != 2 || out["Set-Cookie"][1] != maskedHeaderValue {
		t.Errorf("Set-Cookie maskelenmedi: %v", out["Set-Cookie"])
	}
	if out["Content-Type"][0] != "text/plain" {
		t.Error("hassas olmayan baslik degismemeli")
	}
	if in["Authorization"][0] != "Bearer abc" {
		t.Error("girdi degistirilmemeli")
	}
	if maskSensitiveHeaders(nil) != nil {
		t.Error("nil -> nil")
	}
}

func TestCaptureOutMaskingByRole(t *testing.T) {
	c := &reqlog.Capture{
		ReqHeaders:  map[string][]string{"Authorization": {"Bearer abc"}},
		RespHeaders: map[string][]string{"Set-Cookie": {"s=1"}},
	}
	member := captureOut(c, false)
	if member["req_headers"].(map[string][]string)["Authorization"][0] != maskedHeaderValue ||
		member["resp_headers"].(map[string][]string)["Set-Cookie"][0] != maskedHeaderValue {
		t.Fatal("member icin maskeli olmali")
	}
	owner := captureOut(c, true)
	if owner["req_headers"].(map[string][]string)["Authorization"][0] != "Bearer abc" ||
		owner["resp_headers"].(map[string][]string)["Set-Cookie"][0] != "s=1" {
		t.Fatal("owner/admin icin acik olmali")
	}
}
