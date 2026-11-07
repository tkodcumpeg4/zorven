package main

import "strings"

// hostURL, sunucunun verdigi ad/adres icin tam URL uretir: "host" -> https://host;
// "host:port" (TCP/UDP) ve "tcp://" gibi semalar oldugu gibi kalir.
func hostURL(h string) string {
	h = strings.TrimSpace(h)
	if strings.Contains(h, "://") {
		return h
	}
	if i := strings.LastIndex(h, ":"); i > 0 && isDigits(h[i+1:]) {
		return "tcp://" + h
	}
	return "https://" + h
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
