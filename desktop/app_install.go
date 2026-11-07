package main

// Kurulu (Program Files) Windows kipinde masaustu HER ZAMAN arka plan servisi
// uzerinden calisir: gomulu ajan baslatilmaz, "devral / hizmet moduna don" secimi
// yoktur. Kimlik (token) kullanici girisinden sonra tek seferlik yukseltilmis
// yardimciyla servisin sistem ayar dosyasina yazilir (runSvcTokenHelper).
// Tasinabilir calistirmada (ve macOS/Linux'ta) eski gomulu ajan yolu surer.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// InstallInfo, arayuze kurulum durumunu bildirir.
type InstallInfo struct {
	Windows    bool   `json:"windows"`
	Installed  bool   `json:"installed"` // Program Files kurulumu (servis kipi zorunlu)
	Portable   bool   `json:"portable"`  // Windows'ta kurulu olmayan kopya
	Legacy     bool   `json:"legacy"`    // eski LOCALAPPDATA kurulumu izi var
	Dir        string `json:"dir,omitempty"`
	HandoffErr string `json:"handoff_err,omitempty"`
}

// GetInstallInfo, kurulum modunu doner.
func (a *App) GetInstallInfo() InstallInfo {
	if runtime.GOOS != "windows" {
		return InstallInfo{}
	}
	inst := installedMode()
	info := InstallInfo{Windows: true, Installed: inst, Portable: !inst}
	if inst {
		if exe, err := os.Executable(); err == nil {
			info.Dir = exe
		}
		a.mu.Lock()
		info.HandoffErr = a.handoffErr
		a.mu.Unlock()
	} else {
		info.Legacy = legacyFound()
	}
	return info
}

// tokenSig, kimlik+sunucu ciftinin kisa imzasi (token'in kendisi saklanmaz).
func tokenSig(token, server string) string {
	h := sha256.Sum256([]byte(server + "\x00" + token))
	return hex.EncodeToString(h[:8])
}

// syncServiceToken, kayitli kimligi servise aktarir (kurulu kip). Ayni kimlik
// daha once aktarildiysa (config.SvcTokenSig) UAC sorulmaz. Hata metni doner.
func (a *App) syncServiceToken() string {
	if !installedMode() {
		return ""
	}
	cfg := a.cfgCopy()
	if cfg.Token == "" {
		return ""
	}
	server := cfg.ServerAddr
	if server == "" || server == "localhost:8443" {
		server = defaultServer
	}
	sig := tokenSig(cfg.Token, server)
	if cfg.SvcTokenSig == sig {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return tm("svc.handoffFail", err.Error())
	}
	tf, err := writeInstallTokenFile(cfg.Token)
	if err != nil {
		return tm("svc.handoffFail", err.Error())
	}
	defer os.Remove(tf)
	code, err := runElevatedWait(exe, fmt.Sprintf(`%s "%s" %s%s`, svcTokenFlag, tf, server, operatorParam()), 2*time.Minute)
	msg := ""
	switch {
	case err != nil:
		msg = tm("svc.handoffFail", err.Error())
	case code != setupOK:
		msg = tm("svc.handoffFail", setupErrText(code))
	}
	a.mu.Lock()
	a.handoffErr = msg
	if msg == "" {
		a.cfg.SvcTokenSig = sig
	}
	a.mu.Unlock()
	if msg == "" {
		if err := a.persist(); err != nil {
			a.log.Warn("kimlik imzasi kaydedilemedi", "hata", err)
		}
	} else {
		a.log.Warn("kimlik servise aktarilamadi", "hata", msg)
	}
	return msg
}

// connectInstalled, kurulu kipte "baglan": gomulu ajan yok; kimligi servise
// aktarir ve servis durumunu tazeler.
func (a *App) connectInstalled(target string) string {
	if target != "" {
		return tm("svc.active")
	}
	if msg := a.syncServiceToken(); msg != "" {
		return msg
	}
	a.serviceLoopTick()
	if !a.svcSnapshot().Found {
		return tm("svc.starting")
	}
	return ""
}

// RepairService, kurulu kipte servisi yeniden kurar/baslatir (UAC). Kimlik varsa
// birlikte aktarilir.
func (a *App) RepairService() NetTunActionResult {
	if runtime.GOOS != "windows" {
		return NetTunActionResult{Error: tm("net.winOnly")}
	}
	if !installedMode() {
		return NetTunActionResult{Error: tm("svc.notInstalled")}
	}
	exe, err := os.Executable()
	if err != nil {
		return NetTunActionResult{Error: err.Error()}
	}
	cfg := a.cfgCopy()
	server := cfg.ServerAddr
	if !svcServerRe.MatchString(server) {
		server = defaultServer
	}
	params := installRunFlag + " --repair --server " + server + operatorParam()
	tf := ""
	if svcTokenRe.MatchString(cfg.Token) {
		if f, err := writeInstallTokenFile(cfg.Token); err == nil {
			tf = f
			params += ` --token-file "` + f + `"`
		}
	}
	code, err := runElevatedWait(exe, params, 5*time.Minute)
	if tf != "" {
		_ = os.Remove(tf)
	}
	if err != nil {
		return NetTunActionResult{Error: err.Error()}
	}
	if code != setupOK {
		return NetTunActionResult{Error: setupErrText(code)}
	}
	a.mu.Lock()
	a.handoffErr = ""
	a.mu.Unlock()
	return NetTunActionResult{OK: true, Message: tm("svc.repairOK")}
}

// StartSetup, tasinabilir calisan kopyadan kurulum akisini (yeni surec, --install)
// baslatir ve bu uygulamayi kapatir. Yalniz Windows, yalniz kurulu degilken.
func (a *App) StartSetup() string {
	if runtime.GOOS != "windows" {
		return tm("net.winOnly")
	}
	if installedMode() {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return err.Error()
	}
	if err := exec.Command(exe, installFlag).Start(); err != nil {
		return err.Error()
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.QuitApp()
	}()
	return ""
}
