package agent

// Servis modunda hangi kullanici oturumuna ekran yardimcisi baslatilacagi
// (platformdan bagimsiz mantik; Windows baglantisi helper_service_windows.go).
//
// NEDEN: onceden yalniz WTSGetActiveConsoleSessionId kullaniliyordu. Sunucu
// (RDP ile baglanilan) makinelerde konsol oturumunda kullanici yoktur; aktif
// kullanici RDP oturumundadir ve ekran paylasimi "kullanici yok" derdi.

const (
	wtsNoSession     = 0xFFFFFFFF
	wtsMaxUserSessID = 65535 // 65536+ dinleyici (listener) oturumlari
	wtsStateActive   = 0
)

// sessInfo, WTS oturum listesi girdisi.
type sessInfo struct {
	ID     uint32
	Active bool // WTSActive
}

// pickSessions, yardimci icin aday oturumlari SIRAYLA doner: once fiziksel
// konsol oturumu (kullanicisi varsa onu tercih ederiz), sonra diger aktif
// (ornegin RDP) oturumlar. Oturum 0 (servis) ve dinleyici oturumlari dahil degildir.
func pickSessions(console uint32, list []sessInfo) []uint32 {
	var out []uint32
	seen := map[uint32]bool{}
	add := func(id uint32) {
		if id == 0 || id == wtsNoSession || id > wtsMaxUserSessID || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(console)
	for _, s := range list {
		if s.Active {
			add(s.ID)
		}
	}
	return out
}
