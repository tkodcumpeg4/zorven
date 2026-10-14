package updatesig

// PublicKeys, istemci guncellemelerini (manifest.json / desktop.json) imzalayan
// yayin anahtarlarinin ACIK kisimlari (base64, 32 bayt ed25519). Birden fazla
// girdi anahtar donusu icindir: eski ve yeni anahtar birlikte listelenir, imza
// herhangi biriyle dogrulanirsa kabul edilir.
//
// DIKKAT: Liste bos iken hicbir guncelleme kabul edilmez (imza dogrulamasi
// atlanmaz). Bu yuzden ANAHTAR GOMULMEDEN istemci surumu yayinlanmamalidir;
// bkz. docs/release-signing.md. Ozel anahtar asla repoya girmez.
var PublicKeys = []string{
	"sEMP7Ls3HG+OSto0taPbYYlofslq3b38okgZKgVh6SQ=", // zorven-release, 2026-10-09
	// Kurtarma anahtari (2026-10-14): normal yayinda KULLANILMAZ, cevrimdisi saklanir.
	// Ana anahtar kaybolur ya da sizarsa yeni anahtari tasiyan surumu bununla imzalariz;
	// bkz. docs/release-signing.md "Kayip / sizinti".
	"AXW6CMqdchBeiNNJdnPbU9Mae3f8nL8euAu0ez/u35w=", // zorven-recovery, 2026-10-14
}
