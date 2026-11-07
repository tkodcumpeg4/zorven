package main

import (
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// liveResult, ayar degisikliginin calisan ajana uygulanma sonucu.
type liveResult int

const (
	liveNoAgent     liveResult = iota // calisan ajan yok: bir sonraki baslatmada gecerli
	liveApplied                       // ajana canli uygulandi
	liveNeedRestart                   // ajan var ama canli uygulayamiyor (eski surum) ya da kapsam eslesmiyor
	liveFailed                        // ajan var, islem hata verdi
	liveLater                         // ajan var; bu ayar canli uygulanamaz, yeniden baslatinca gecerli olur
)

// configLive, her ayar anahtarinin calisan ajana CANLI uygulanip uygulanmadigi.
// Ajanin "reload" islemi (agent.Reload/applyReload) yalniz terminal/ekran izin
// tavanini ve sunucu adresi ile token'i canli degistirir. insecure ve ca_cert
// baglanti kurulurken, enable_deploy (Zorven Apps izni) ve language ajan
// baslarken bir kez okunur; bunlarda degisiklik ancak yeniden baslatinca gecerli
// olur. Yeni bir ayar anahtari eklenirse buraya da bilincli bir karar yazilmali
// (TestConfigLiveCoversAllKeys).
var configLive = map[string]bool{
	"server":          true,
	"token":           true,
	"enable_terminal": true,
	"enable_screen":   true,
	"insecure":        false,
	"ca_cert":         false,
	"enable_deploy":   false,
	"language":        false,
}

// liveApplicable: anahtar calisan ajana canli uygulanabilir mi?
func liveApplicable(key string) bool { return configLive[key] }

// applyConfigLive, kaydedilen ayari calisan ajana yansitir. Canli uygulanabilen
// anahtarlarda ajana "reload" gider; digerlerinde ajana dokunulmaz ve (ajan
// varsa) "yeniden baslatinca gecerli olur" sonucu doner.
func applyConfigLive(system bool, key string) (liveResult, error) {
	if liveApplicable(key) {
		return reloadAgent(system)
	}
	ep, _, _ := findAgent()
	if ep == nil {
		return liveNoAgent, nil
	}
	if !scopeMatches(ep, system) {
		return liveNeedRestart, nil
	}
	return liveLater, nil
}

// scopeMatches: duzenlenen ayar dosyasinin kapsami (sistem/kullanici) bulunan
// ajanin turuyle ayni mi? Servis sistem dosyasini, on plan kullanici dosyasini
// okur; eslesmiyorsa canli uygulamanin anlami yoktur.
func scopeMatches(ep *localctl.Endpoint, system bool) bool {
	return ep != nil && ep.Service == system
}

// reloadAgent, calisan ajana "reload" yollar (ayar dosyasini yeniden oku).
func reloadAgent(system bool) (liveResult, error) {
	ep, _, _ := findAgent()
	if ep == nil {
		return liveNoAgent, nil
	}
	if !scopeMatches(ep, system) {
		return liveNeedRestart, nil
	}
	_, err := callAgent(*ep, localctl.Request{Op: "reload"})
	if err != nil {
		if localctl.IsUnsupported(err) {
			return liveNeedRestart, nil
		}
		return liveFailed, err
	}
	return liveApplied, nil
}

// setCapsLive, terminal/ekran tavanini calisan ajana uygular (`zorven
// enable/disable` ile ayni "set" islemi); eski ajanlarda da calisir.
func setCapsLive(t, s *bool, system bool) (liveResult, error) {
	ep, _, _ := findAgent()
	if ep == nil {
		return liveNoAgent, nil
	}
	if !scopeMatches(ep, system) {
		return liveNeedRestart, nil
	}
	if _, err := callAgent(*ep, localctl.Request{Op: "set", Terminal: t, Screen: s}); err != nil {
		return liveFailed, err
	}
	return liveApplied, nil
}
