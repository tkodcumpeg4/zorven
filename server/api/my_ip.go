package api

import (
	"net"
	"net/http"
)

// myIP, GET /api/v1/my-ip: cagiranin sunucuya gorunen IP adresi. Panelin
// "IP'mi algila" dugmesi bunu kullanir; eskiden ucuncu taraf bir servise
// (api64.ipify.org) istek atiliyordu, kullanicinin IP'si disariya gidiyordu.
// Kaynak baglanti adresidir; X-Forwarded-For bilerek kullanilmaz (uydurulabilir).
func (s *Server) myIP(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if p := net.ParseIP(ip); p != nil {
		if v4 := p.To4(); v4 != nil {
			ip = v4.String()
		} else {
			ip = p.String()
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"ip": ip})
}
