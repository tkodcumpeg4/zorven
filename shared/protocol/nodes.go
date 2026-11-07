package protocol

// Dugum listesi (Faz 0C-4): sunucu, HelloAck ile kumedeki dugumleri ajana
// bildirir; ajan bunu onbellege alip baglanti koptugunda yakin/saglikli
// dugume yuk devri icin kullanir. Tum alanlar geriye uyumludur: eski ajanlar
// bilinmeyen JSON alanlarini yok sayar, eski sunucular alani hic gondermez
// (ajan o zaman tek adresli eski davranisa duser).

// NodeHint, kumedeki tek bir dugumun ajana bildirilen ozeti.
type NodeHint struct {
	NodeID string `json:"node_id"`
	// Address "host:port" bicimindedir (sema yok); ajan wss/ws semasini kendi
	// ayarina (Insecure) gore ekler. Sunucu "https://host:port" gibi bir adres
	// tutuyorsa NormalizeNodeAddress ile sadelestirilir.
	Address string `json:"address"`
	Region  string `json:"region,omitempty"`
	// Priority kucuk olan once denenir (0 en yuksek oncelik).
	Priority int `json:"priority,omitempty"`
	// Healthy false ise ajan dugumu yalnizca baska aday kalmadiginda dener.
	Healthy bool `json:"healthy"`
}

// NormalizeNodeAddress, "https://host:443/yol" gibi bir adresi "host:443"
// bicimine indirger. Gecersiz/bos girdide bos doner.
func NormalizeNodeAddress(a string) string {
	for _, p := range []string{"https://", "http://", "wss://", "ws://"} {
		if len(a) >= len(p) && a[:len(p)] == p {
			a = a[len(p):]
			break
		}
	}
	for i := 0; i < len(a); i++ {
		if a[i] == '/' || a[i] == '?' || a[i] == '#' || a[i] == ' ' {
			a = a[:i]
			break
		}
	}
	return a
}
