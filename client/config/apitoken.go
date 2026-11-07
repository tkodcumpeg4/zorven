package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// APITokenPath, kullanicinin kayitli API token'inin (zrv_api_...) dosyasi.
// Cihaz (ajan) token'indan ayri tutulur: farkli bir kimliktir.
func APITokenPath() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "api_token"), nil
}

// LoadAPIToken, kayitli API token'ini okur; yoksa "" doner.
func LoadAPIToken() string {
	p, err := APITokenPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SaveAPIToken, API token'ini 0600 izinle (Windows'ta korumali DACL) yazar.
func SaveAPIToken(tok string) error {
	p, err := APITokenPath()
	if err != nil {
		return err
	}
	return WriteSecretFile(p, []byte(strings.TrimSpace(tok)+"\n"))
}

// DeleteAPIToken, kayitli API token'ini siler; dosya yoksa hata degildir.
// Silinip silinmedigini doner.
func DeleteAPIToken() (bool, error) {
	p, err := APITokenPath()
	if err != nil {
		return false, err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
