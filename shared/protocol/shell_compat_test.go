package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Eski istemci hello'su (shells alani yok) yeni sunucuda sorunsuz cozulur.
func TestHelloWithoutShellsDecodes(t *testing.T) {
	var h Hello
	if err := json.Unmarshal([]byte(`{"type":"hello","client_version":"0.2.0","platform":"linux/amd64"}`), &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Shells) != 0 || h.DefaultShell != "" {
		t.Fatalf("beklenmeyen: %+v", h)
	}
}

// Yeni istemci, kabuk bilmeyen eski sunucuya alan yollayabilir: bilinmeyen
// alanlar JSON cozumunde yok sayilir (eski Hello yapisini taklit eder).
func TestNewHelloDecodesIntoOldShape(t *testing.T) {
	data, err := json.Marshal(Hello{
		Type: TypeHello, ClientVersion: "1", Platform: "linux/amd64",
		Shells:       []ShellInfo{{ID: "zsh", Name: "Zsh", Path: "/bin/zsh"}},
		DefaultShell: "zsh",
	})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Type          MessageType `json:"type"`
		ClientVersion string      `json:"client_version"`
		Platform      string      `json:"platform"`
	}
	if err := json.Unmarshal(data, &old); err != nil || old.ClientVersion != "1" {
		t.Fatalf("eski yapi cozemedi: %v %+v", err, old)
	}
}

// Kabuk listesi yoksa alanlar JSON'a hic yazilmaz (omitempty).
func TestEmptyShellFieldsOmitted(t *testing.T) {
	h, _ := json.Marshal(Hello{Type: TypeHello})
	if strings.Contains(string(h), "shell") {
		t.Fatalf("hello'da shell alani olmamali: %s", h)
	}
	o, _ := json.Marshal(TerminalOpen{Type: TypeTerminalOpen, SessionID: "s", Cols: 80, Rows: 24})
	if strings.Contains(string(o), "shell") {
		t.Fatalf("terminal_open'da shell alani olmamali: %s", o)
	}
}

// Eski sunucunun gonderdigi terminal_open (shell yok) yeni istemcide varsayilan kabugu verir.
func TestOldTerminalOpenDecodes(t *testing.T) {
	var m TerminalOpen
	if err := json.Unmarshal([]byte(`{"type":"terminal_open","session_id":"term_1","cols":80,"rows":24}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Shell != "" || m.SessionID != "term_1" {
		t.Fatalf("beklenmeyen: %+v", m)
	}
}

// Yeni sunucunun terminal_open'i eski istemci yapisina cozulur.
func TestNewTerminalOpenDecodesIntoOldShape(t *testing.T) {
	data, _ := json.Marshal(TerminalOpen{Type: TypeTerminalOpen, SessionID: "term_1", Cols: 100, Rows: 30, Shell: "bash"})
	var old struct {
		Type      MessageType `json:"type"`
		SessionID string      `json:"session_id"`
		Cols      uint16      `json:"cols"`
		Rows      uint16      `json:"rows"`
	}
	if err := json.Unmarshal(data, &old); err != nil || old.Cols != 100 || old.SessionID != "term_1" {
		t.Fatalf("eski yapi cozemedi: %v %+v", err, old)
	}
}
