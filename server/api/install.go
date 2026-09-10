package api

import (
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/install.sh
var installShTemplate string

//go:embed templates/install.ps1
var installPs1Template string

// serveInstallSh, Linux ve macOS icin dinamik bash kurulum scriptini doner.
func (s *Server) serveInstallSh(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	server := strings.TrimSpace(r.URL.Query().Get("server"))
	if server == "" {
		server = r.Host
	}

	content := strings.ReplaceAll(installShTemplate, "__INJECTED_TOKEN__", token)
	content = strings.ReplaceAll(content, "__INJECTED_SERVER__", server)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content))
}

// serveInstallPs1, Windows icin dinamik PowerShell kurulum scriptini doner.
func (s *Server) serveInstallPs1(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	server := strings.TrimSpace(r.URL.Query().Get("server"))
	if server == "" {
		server = r.Host
	}

	content := strings.ReplaceAll(installPs1Template, "__INJECTED_TOKEN__", token)
	content = strings.ReplaceAll(content, "__INJECTED_SERVER__", server)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content))
}

// serveBinary, onceden derlenmis Zorven istemci ikililerini dagitir.
func (s *Server) serveBinary(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	cleanName := filepath.Base(filename)
	if cleanName == "." || cleanName == "/" || cleanName == "" {
		http.Error(w, "gecersiz dosya adi", http.StatusBadRequest)
		return
	}

	// server/bin altinda veya kok bin altinda ara
	searchPaths := []string{
		filepath.Join("server", "bin", cleanName),
		filepath.Join("bin", cleanName),
	}

	var foundPath string
	for _, p := range searchPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			foundPath = p
			break
		}
	}

	// Gelistirme ortaminda kolaylik: windows-amd64 istenmisse ve kokte client.exe varsa onu kullan
	if foundPath == "" && (cleanName == "zorven-windows-amd64.exe" || cleanName == "zorven.exe") {
		devExe := "client.exe"
		if fi, err := os.Stat(devExe); err == nil && !fi.IsDir() {
			foundPath = devExe
		}
	}

	if foundPath == "" {
		http.Error(w, fmt.Sprintf("ikili dosya bulunamadi: %s. Lutfen server/bin altina derleyin.", cleanName), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", cleanName))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, foundPath)
}
