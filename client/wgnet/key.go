// Package wgnet, Zorven Net istemci tarafi: wireguard-go + gVisor netstack
// (kullanici alani, root gerektirmez) ve agent WSS uzerinden relay eden ozel
// conn.Bind. Ozel anahtar cihazdan CIKMAZ; sunucu yalniz acik anahtari gorur.
package wgnet

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/curve25519"
)

// Key, Curve25519 ozel anahtari.
type Key [32]byte

// GenerateKey, yeni bir WireGuard ozel anahtari uretir.
func GenerateKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k, nil
}

// Public, acik anahtar.
func (k Key) Public() [32]byte {
	p, _ := curve25519.X25519(k[:], curve25519.Basepoint)
	var out [32]byte
	copy(out[:], p)
	return out
}

// PublicB64: sunucuya gonderilen bicim.
func (k Key) PublicB64() string {
	p := k.Public()
	return base64.StdEncoding.EncodeToString(p[:])
}

func (k Key) hex() string { return hex.EncodeToString(k[:]) }

// LoadOrCreateKey, anahtari dosyadan okur; yoksa uretir ve 0600 ile yazar.
func LoadOrCreateKey(path string) (Key, error) {
	var k Key
	if b, err := os.ReadFile(path); err == nil {
		raw, derr := base64.StdEncoding.DecodeString(string(trimSpace(b)))
		if derr != nil || len(raw) != 32 {
			return k, fmt.Errorf("wg anahtar dosyasi bozuk: %s", path)
		}
		copy(k[:], raw)
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	k, err := GenerateKey()
	if err != nil {
		return k, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return k, err
	}
	enc := base64.StdEncoding.EncodeToString(k[:]) + "\n"
	if err := os.WriteFile(path, []byte(enc), 0o600); err != nil {
		return k, err
	}
	return k, nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
