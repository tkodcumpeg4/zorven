package agent

import (
	"net/url"
	"testing"
)

// "@" yasagi kaldirildiktan sonra da hileli yollar yerel hedefin host'unu
// degistiremez: yol "/" ile basladigi icin authority base'den gelir.
func TestAtInPathCannotChangeHost(t *testing.T) {
	bases := []string{"http://127.0.0.1:3000", "http://localhost:8080", "http://localhost:8080/api"}
	paths := []string{"/@evil.com", "/@evil.com:80/x", "//evil.com/x", "/%40evil.com", "/a@b@c", "/@169.254.169.254/latest", "/@[::1]:22/"}
	for _, b := range bases {
		bu, _ := url.Parse(b)
		for _, p := range paths {
			if !validLocalPath(p) {
				continue
			}
			u, err := url.Parse(b + p)
			if err != nil {
				continue
			}
			if u.Host != bu.Host || u.User != nil {
				t.Errorf("%s + %s -> host=%q user=%v (base host %q)", b, p, u.Host, u.User, bu.Host)
			}
		}
	}
}
