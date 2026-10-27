package agent

// Z1 — Cihaz durusu (posture) toplama.
//
// Kurallar:
//   - AG CAGRISI YOK; yalniz yerel komut/dosya okuma.
//   - Komutlar exec.CommandContext ile, SABIT argumanlarla (kabuk yorumlamasi
//     yok, girdi komuta karismaz) ve 2 sn zaman asimiyla calisir.
//   - Toplanamayan alan "unknown" olur; hata rapor DEGIL, bilgi eksigidir.
//   - Parserlar saf fonksiyonlardir (ornek ciktilarla test edilir).
//
// Rapor istemci beyanidir; sunucu bunu imzasiz kabul eder (bkz. protocol/posture.go).

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	postureCmdTimeout = 2 * time.Second
	postureInterval   = 5 * time.Minute  // toplama araligi
	postureMaxSilence = 30 * time.Minute // degismese de en az bu surede bir gonder
	postureOutLimit   = 64 << 10
)

// postureEnv, komutlari ve dosya sistemini soyutlar (testler sahte verir).
type postureEnv struct {
	goos      string
	run       func(ctx context.Context, name string, args ...string) (string, error)
	readFile  func(path string) ([]byte, error)
	readDir   func(path string) ([]string, error)
	evalLinks func(path string) (string, error)
	version   string
	isService bool
}

func realPostureEnv(version string, isService bool) postureEnv {
	return postureEnv{
		goos: runtime.GOOS, run: runPostureCmd, readFile: os.ReadFile,
		readDir: func(p string) ([]string, error) {
			es, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(es))
			for _, e := range es {
				out = append(out, e.Name())
			}
			return out, nil
		},
		evalLinks: filepath.EvalSymlinks,
		version:   version, isService: isService,
	}
}

// runPostureCmd, tek komutu zaman asimli ve cikti sinirli calistirir.
// stdout+stderr birlestirilir (sysadminctl gibi araclar stderr'e yazar).
func runPostureCmd(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, postureCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var buf limitedBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

type limitedBuffer struct{ b bytes.Buffer }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := postureOutLimit - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil // fazlasini sessizce at
}
func (l *limitedBuffer) String() string { return l.b.String() }

// collectPosture, tum alanlari toplar.
func collectPosture(ctx context.Context, e postureEnv) protocol.PostureReport {
	r := protocol.PostureReport{
		Type: protocol.TypePostureReport, ClientVersion: e.version, IsService: e.isService,
		DiskEncrypted: protocol.PostureUnknown, FirewallOn: protocol.PostureUnknown, ScreenLock: protocol.PostureUnknown,
	}
	switch e.goos {
	case "windows":
		r.OSName = "windows"
		r.OSVersion, r.OSBuild = windowsVersion(ctx, e)
		r.DiskEncrypted = windowsDisk(ctx, e)
		r.FirewallOn = windowsFirewall(ctx, e)
		r.ScreenLock = windowsScreenLock(ctx, e)
	case "darwin":
		r.OSName = "macos"
		if out, err := e.run(ctx, "sw_vers", "-productVersion"); err == nil {
			r.OSVersion = strings.TrimSpace(out)
		}
		if out, err := e.run(ctx, "sw_vers", "-buildVersion"); err == nil {
			r.OSBuild = strings.TrimSpace(out)
		}
		if out, err := e.run(ctx, "fdesetup", "status"); err == nil || out != "" {
			r.DiskEncrypted = parseFdesetup(out)
		}
		if out, err := e.run(ctx, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate"); err == nil || out != "" {
			r.FirewallOn = parseSocketFilterFW(out)
		}
		if out, err := e.run(ctx, "sysadminctl", "-screenLock", "status"); err == nil || out != "" {
			r.ScreenLock = parseSysadminctlScreenLock(out)
		}
	default:
		r.OSName = "linux"
		if b, err := e.readFile("/etc/os-release"); err == nil {
			r.OSVersion = parseOSReleaseVersion(string(b))
		}
		if b, err := e.readFile("/proc/sys/kernel/osrelease"); err == nil {
			r.OSBuild = strings.TrimSpace(string(b))
		}
		r.DiskEncrypted = linuxDisk(e)
		r.FirewallOn = linuxFirewall(ctx, e)
		if out, err := e.run(ctx, "gsettings", "get", "org.gnome.desktop.screensaver", "lock-enabled"); err == nil {
			r.ScreenLock = parseBoolWord(out)
		}
	}
	return r
}

// --- ortak -------------------------------------------------------------------

func parseBoolWord(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return protocol.PostureYes
	case "false", "0", "no", "off":
		return protocol.PostureNo
	}
	return protocol.PostureUnknown
}

// --- macOS -------------------------------------------------------------------

// parseFdesetup: "FileVault is On." / "FileVault is Off."
func parseFdesetup(out string) string {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "filevault is on"):
		return protocol.PostureYes
	case strings.Contains(l, "filevault is off"):
		return protocol.PostureNo
	}
	return protocol.PostureUnknown
}

// parseSocketFilterFW: "Firewall is enabled. (State = 1)" / "disabled. (State = 0)"
func parseSocketFilterFW(out string) string {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "state = 0"), strings.Contains(l, "is disabled"):
		return protocol.PostureNo
	case strings.Contains(l, "state = 1"), strings.Contains(l, "state = 2"), strings.Contains(l, "is enabled"):
		return protocol.PostureYes
	}
	return protocol.PostureUnknown
}

// parseSysadminctlScreenLock: "screenLock delay is immediate" / "screenLock is off"
func parseSysadminctlScreenLock(out string) string {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "screenlock is off"), strings.Contains(l, "screenlock is disabled"):
		return protocol.PostureNo
	case strings.Contains(l, "screenlock delay is"):
		return protocol.PostureYes
	}
	return protocol.PostureUnknown
}

// --- Windows -----------------------------------------------------------------

var winVerRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)(?:\.(\d+))?`)

// parseWindowsVer: "Microsoft Windows [Version 10.0.22631.3593]" (onek yerellesebilir).
// os_version = 10.0.22631, os_build = tam surum.
func parseWindowsVer(out string) (version, build string) {
	m := winVerRe.FindStringSubmatch(out)
	if m == nil {
		return "", ""
	}
	return m[1] + "." + m[2] + "." + m[3], m[0]
}

func windowsVersion(ctx context.Context, e postureEnv) (string, string) {
	out, err := e.run(ctx, "cmd", "/c", "ver")
	if err != nil && out == "" {
		return "", ""
	}
	return parseWindowsVer(out)
}

// Sabit PowerShell betikleri (kullanici girdisi yok). Cikti dilden bagimsizdir.
const (
	psBitLocker = `(Get-CimInstance -Namespace root/CIMV2/Security/MicrosoftVolumeEncryption -ClassName Win32_EncryptableVolume -Filter "DriveLetter='C:'").ProtectionStatus`
	psFirewall  = `Get-NetFirewallProfile -All | ForEach-Object { $_.Enabled }`
)

// parseBitLockerStatus: Win32_EncryptableVolume.ProtectionStatus (0 kapali, 1 acik,
// 2 bilinmiyor) YA DA "manage-bde -status C:" (Ingilizce) ciktisi.
func parseBitLockerStatus(out string) string {
	t := strings.TrimSpace(out)
	switch t {
	case "1":
		return protocol.PostureYes
	case "0":
		return protocol.PostureNo
	case "2", "":
		return protocol.PostureUnknown
	}
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "protection on"):
		return protocol.PostureYes
	case strings.Contains(l, "protection off"):
		// "Fully Decrypted" ise gercekten kapali; askiya alinmis koruma da "off" gorunur
		// ve korumasizdir — her ikisi de "no".
		return protocol.PostureNo
	}
	return protocol.PostureUnknown
}

func windowsDisk(ctx context.Context, e postureEnv) string {
	if out, err := e.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psBitLocker); err == nil {
		if v := parseBitLockerStatus(out); v != protocol.PostureUnknown {
			return v
		}
	}
	if out, err := e.run(ctx, "manage-bde", "-status", "C:"); err == nil {
		return parseBitLockerStatus(out)
	}
	return protocol.PostureUnknown
}

// parseNetFirewallProfiles: satir basina True/False (Get-NetFirewallProfile).
// Hepsi True -> yes; herhangi False -> no.
func parseNetFirewallProfiles(out string) string {
	yes, no := 0, 0
	for _, ln := range strings.Split(out, "\n") {
		switch strings.ToLower(strings.TrimSpace(ln)) {
		case "true", "1":
			yes++
		case "false", "0":
			no++
		}
	}
	switch {
	case no > 0:
		return protocol.PostureNo
	case yes > 0:
		return protocol.PostureYes
	}
	return protocol.PostureUnknown
}

// parseNetshFirewall: "netsh advfirewall show allprofiles state" — her profil
// icin "State   ON|OFF" (Turkce: AÇIK|KAPALI). Herhangi profil kapaliysa "no".
func parseNetshFirewall(out string) string {
	on, off := 0, 0
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(strings.ToLower(strings.TrimSpace(ln)))
		if len(f) < 2 || strings.HasSuffix(f[0], ":") || strings.Contains(ln, "---") {
			continue
		}
		switch f[len(f)-1] {
		case "on", "açık", "açik", "acik", "ein":
			on++
		case "off", "kapalı", "kapali", "aus":
			off++
		}
	}
	switch {
	case off > 0:
		return protocol.PostureNo
	case on > 0:
		return protocol.PostureYes
	}
	return protocol.PostureUnknown
}

func windowsFirewall(ctx context.Context, e postureEnv) string {
	if out, err := e.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psFirewall); err == nil {
		if v := parseNetFirewallProfiles(out); v != protocol.PostureUnknown {
			return v
		}
	}
	if out, err := e.run(ctx, "netsh", "advfirewall", "show", "allprofiles", "state"); err == nil {
		return parseNetshFirewall(out)
	}
	return protocol.PostureUnknown
}

// parseRegScreenLock: `reg query "HKCU\Control Panel\Desktop"` ciktisinda
// ScreenSaveActive ve ScreenSaverIsSecure. Yalnizca "yes" ya da "unknown" doner:
// grup ilkesiyle kilit baska yerde de kurulabilir, "no" iddiasi yanlis olabilir.
func parseRegScreenLock(out string) string {
	val := func(name string) string {
		for _, ln := range strings.Split(out, "\n") {
			f := strings.Fields(ln)
			if len(f) >= 3 && strings.EqualFold(f[0], name) {
				return strings.TrimSpace(f[len(f)-1])
			}
		}
		return ""
	}
	if val("ScreenSaveActive") == "1" && val("ScreenSaverIsSecure") == "1" {
		return protocol.PostureYes
	}
	return protocol.PostureUnknown
}

func windowsScreenLock(ctx context.Context, e postureEnv) string {
	if e.isService {
		return protocol.PostureUnknown // SYSTEM hesabinin HKCU'su kullaniciyi yansitmaz
	}
	out, err := e.run(ctx, "reg", "query", `HKCU\Control Panel\Desktop`)
	if err != nil && out == "" {
		return protocol.PostureUnknown
	}
	return parseRegScreenLock(out)
}

// --- Linux -------------------------------------------------------------------

// parseOSReleaseVersion: VERSION_ID ("22.04"); yoksa bos (rolling dagitimlar).
func parseOSReleaseVersion(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "VERSION_ID="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// parseRootDevice: /proc/mounts icinden "/" baglama noktasinin aygiti.
func parseRootDevice(mounts string) string {
	dev := ""
	for _, ln := range strings.Split(mounts, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && f[1] == "/" {
			dev = f[0] // sonuncu kazanir (ustune baglamalar)
		}
	}
	return dev
}

// linuxDisk: kok dosya sistemi dm-crypt/LUKS uzerinde mi. Zincir
// /sys/block/<dm-N>/slaves boyunca (LVM-on-LUKS dahil) uuid "CRYPT-" aranir.
func linuxDisk(e postureEnv) string {
	b, err := e.readFile("/proc/mounts")
	if err != nil {
		return protocol.PostureUnknown
	}
	dev := parseRootDevice(string(b))
	if !strings.HasPrefix(dev, "/dev/") {
		return protocol.PostureUnknown // overlay/tmpfs/konteyner: bilinmiyor
	}
	if real, err := e.evalLinks(dev); err == nil {
		dev = real
	}
	name := filepath.Base(dev)
	switch cryptChain(e, name, 0) {
	case 1:
		return protocol.PostureYes
	case 0:
		return protocol.PostureNo
	}
	return protocol.PostureUnknown
}

// cryptChain: 1 = zincirde CRYPT var, 0 = yok (sysfs okundu), -1 = okunamadi.
func cryptChain(e postureEnv, blk string, depth int) int {
	if depth > 6 {
		return -1
	}
	// Bolum ise (sda2, nvme0n1p3) ust diskin sysfs yolu farkli; dm-* dogrudan /sys/block'ta.
	base := "/sys/block/" + blk
	if _, err := e.readDir(base); err != nil {
		// bolum: /sys/class/block/<blk> uzerinden dene
		base = "/sys/class/block/" + blk
		if _, err := e.readDir(base); err != nil {
			return -1
		}
	}
	if u, err := e.readFile(base + "/dm/uuid"); err == nil && strings.HasPrefix(strings.TrimSpace(string(u)), "CRYPT-") {
		return 1
	}
	slaves, err := e.readDir(base + "/slaves")
	if err != nil {
		return 0 // dm degil / alt aygit yok: sifreleme zinciri yok
	}
	res := 0
	for _, s := range slaves {
		switch cryptChain(e, s, depth+1) {
		case 1:
			return 1
		case -1:
			res = -1
		}
	}
	return res
}

// linuxFirewall: en iyi caba. Servis aktifse yes; kurallar okunabildiyse
// kural sayisina gore; hicbiri okunamadiysa unknown.
func linuxFirewall(ctx context.Context, e postureEnv) string {
	for _, unit := range []string{"firewalld", "ufw"} {
		if out, err := e.run(ctx, "systemctl", "is-active", unit); err == nil && strings.TrimSpace(out) == "active" {
			return protocol.PostureYes
		}
	}
	read := false
	if out, err := e.run(ctx, "nft", "list", "ruleset"); err == nil {
		read = true
		if nftHasRules(out) {
			return protocol.PostureYes
		}
	}
	if out, err := e.run(ctx, "iptables", "-S"); err == nil {
		read = true
		if iptablesHasRules(out) {
			return protocol.PostureYes
		}
	}
	if read {
		return protocol.PostureNo
	}
	return protocol.PostureUnknown
}

// nftHasRules: bir kuralin (accept/drop/reject/counter...) varligi; yalniz bos tablolar sayilmaz.
func nftHasRules(out string) bool {
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "table ") || strings.HasPrefix(t, "chain ") ||
			strings.HasPrefix(t, "type ") || t == "}" || strings.HasPrefix(t, "policy ") {
			continue
		}
		for _, kw := range []string{"accept", "drop", "reject", "jump", "goto", "masquerade", "dnat", "redirect"} {
			if strings.Contains(t, kw) {
				return true
			}
		}
	}
	return false
}

// iptablesHasRules: "-A" ile baslayan kural satiri VEYA DROP/REJECT varsayilan politikasi.
func iptablesHasRules(out string) bool {
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "-A ") {
			return true
		}
		if strings.HasPrefix(t, "-P ") && (strings.HasSuffix(t, " DROP") || strings.HasSuffix(t, " REJECT")) {
			return true
		}
	}
	return false
}

// --- rapor dongusu -------------------------------------------------------------

// postureKey, degisiklik tespiti icin (Type haric) alanlarin ozeti.
func postureKey(r protocol.PostureReport) protocol.PostureReport {
	r.Type = ""
	return r
}

// runPosture, rapor dongusu: hemen bir rapor, sonra postureInterval'de bir
// toplama; degismediyse en fazla postureMaxSilence'ta bir gonderim.
func (cs *clientSession) runPosture(ctx context.Context, env postureEnv) {
	var last protocol.PostureReport
	var lastSent time.Time
	step := func(force bool) {
		rep := collectPosture(ctx, env)
		if !force && !lastSent.IsZero() && postureKey(rep) == postureKey(last) && time.Since(lastSent) < postureMaxSilence {
			return
		}
		// Z4: gecerli cihaz sertifikasi varsa rapor imzalanir (attested).
		if cs.devCert != nil {
			rep.Attest = cs.devCert.attest(rep, cs.deviceChallenge)
		}
		if err := cs.sendControl(ctx, rep, protocol.TypePostureReport); err != nil {
			return // baglanti sorunu: sonraki turda tekrar
		}
		last, lastSent = rep, time.Now()
	}
	step(false)
	tk := time.NewTicker(postureInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			step(false)
		case <-cs.postureKick:
			step(true) // yeni sertifika: imzali rapor hemen
		}
	}
}

// kickPosture, durus raporunun hemen yeniden gonderilmesini ister.
func (cs *clientSession) kickPosture() {
	select {
	case cs.postureKick <- struct{}{}:
	default:
	}
}

// requestDeviceCert, CSR'yi sunucuya gonderir ve device_cert yanitini bekler.
func (cs *clientSession) requestDeviceCert(ctx context.Context, csrPEM string, renew bool) (protocol.DeviceCert, error) {
	select { // eski yaniti at
	case <-cs.certReply:
	default:
	}
	if err := cs.sendControl(ctx, protocol.DeviceCertRequest{
		Type: protocol.TypeDeviceCertRequest, CSRPEM: csrPEM, Renew: renew,
	}, protocol.TypeDeviceCertRequest); err != nil {
		return protocol.DeviceCert{}, err
	}
	select {
	case r := <-cs.certReply:
		return r, nil
	case <-time.After(20 * time.Second):
		return protocol.DeviceCert{}, errors.New("device_cert yaniti zaman asimi")
	case <-ctx.Done():
		return protocol.DeviceCert{}, ctx.Err()
	}
}
