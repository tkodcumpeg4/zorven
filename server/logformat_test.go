package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogHandlerFormats(t *testing.T) {
	var b bytes.Buffer
	slog.New(newLogHandler(&b, "json", slog.LevelInfo)).Info("deneme", "tunnel", "tun_1")
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil || m["msg"] != "deneme" || m["tunnel"] != "tun_1" || m["level"] != "INFO" {
		t.Fatalf("json satiri: %q %v", b.String(), err)
	}
	b.Reset()
	slog.New(newLogHandler(&b, "", slog.LevelInfo)).Info("deneme")
	if !strings.Contains(b.String(), "msg=deneme") {
		t.Fatalf("varsayilan text degil: %q", b.String())
	}
	b.Reset()
	slog.New(newLogHandler(&b, "yaml", slog.LevelInfo)).Info("deneme")
	if !strings.Contains(b.String(), "bilinmeyen log bicimi") || !strings.Contains(b.String(), "msg=deneme") {
		t.Fatalf("bilinmeyen bicim text'e dusmeli: %q", b.String())
	}
}
