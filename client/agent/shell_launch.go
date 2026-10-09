package agent

// Kabuk baslatma: ortam, arguman ve Zorven zsh/bash profilleri.
//
// Ajan servis olarak (root / SYSTEM) calisirken SHELL, HOME, LANG, PATH genellikle
// bos ya da cok sinirlidir; burada makul degerlere tamamlanir. Kullanicinin
// dotfile'larina ASLA dokunulmaz: Zorven profilleri ayri bir dizinde uretilir ve
// kullanicinin dosyalarini yukleyip ustune yalnizca eksik varsayilanlari ekler.

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

//go:embed all:zshassets
var shellAssets embed.FS

// launchOpts, bir kabuk baslatmanin saf (yan etkisiz) girdileri.
type launchOpts struct {
	GOOS      string
	Shell     protocol.ShellInfo
	BaseEnv   []string
	Home      string
	User      string
	ZshDir    string // Zorven ZDOTDIR'i; bos ise zsh duz baslar
	BashRC    string // Zorven bash rcfile'i; bos ise bash duz baslar
	NoPlugins bool
}

type launchPlan struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

const unixDefaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func envKey(goos, k string) string {
	if goos == "windows" {
		return strings.ToUpper(k)
	}
	return k
}

type envMap struct {
	goos  string
	order []string
	vals  map[string]string // anahtar (normallestirilmis) -> "ad=deger"
	names map[string]string
}

func newEnvMap(goos string, base []string) *envMap {
	m := &envMap{goos: goos, vals: map[string]string{}, names: map[string]string{}}
	for _, kv := range base {
		i := strings.IndexByte(kv, '=')
		if i <= 0 { // "=C:=..." gibi Windows ic girdileri ve bozuk satirlar
			continue
		}
		m.set(kv[:i], kv[i+1:])
	}
	return m
}

func (m *envMap) get(k string) string { return m.vals[envKey(m.goos, k)] }
func (m *envMap) has(k string) bool {
	_, ok := m.vals[envKey(m.goos, k)]
	return ok
}
func (m *envMap) set(k, v string) {
	nk := envKey(m.goos, k)
	if _, ok := m.vals[nk]; !ok {
		m.order = append(m.order, nk)
		m.names[nk] = k
	}
	m.vals[nk] = v
}
func (m *envMap) unset(k string) {
	nk := envKey(m.goos, k)
	if _, ok := m.vals[nk]; !ok {
		return
	}
	delete(m.vals, nk)
	for i, o := range m.order {
		if o == nk {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}
func (m *envMap) list() []string {
	out := make([]string, 0, len(m.order))
	for _, nk := range m.order {
		out = append(out, m.names[nk]+"="+m.vals[nk])
	}
	return out
}

func hasUTF8(vals ...string) bool {
	for _, v := range vals {
		l := strings.ToLower(v)
		if strings.Contains(l, "utf-8") || strings.Contains(l, "utf8") {
			return true
		}
	}
	return false
}

// ensurePathDirs, PATH'e eksik standart dizinleri sona ekler.
func ensurePathDirs(cur string, dirs []string) string {
	have := map[string]bool{}
	for _, p := range strings.Split(cur, ":") {
		if p != "" {
			have[p] = true
		}
	}
	out := cur
	for _, d := range dirs {
		if !have[d] {
			if out != "" {
				out += ":"
			}
			out += d
		}
	}
	return out
}

// buildEnv, kabuk ortamini kurar.
func buildEnv(o launchOpts) []string {
	m := newEnvMap(o.GOOS, o.BaseEnv)

	m.set("TERM", "xterm-256color")
	m.set("COLORTERM", "truecolor")

	if o.GOOS == "windows" {
		return m.list()
	}

	// Konum / kullanici kimligi: servis olarak calisirken genellikle eksik.
	if o.Home != "" {
		m.set("HOME", o.Home)
	}
	if o.User != "" {
		if m.get("USER") == "" {
			m.set("USER", o.User)
		}
		if m.get("LOGNAME") == "" {
			m.set("LOGNAME", o.User)
		}
	}
	if o.Shell.Path != "" && o.Shell.ID != "wsl" {
		m.set("SHELL", o.Shell.Path)
	}

	// PATH: standart dizinler eksikse tamamla (systemd'nin dar PATH'i).
	dirs := strings.Split(unixDefaultPath, ":")
	if o.GOOS == "darwin" {
		dirs = append([]string{"/opt/homebrew/bin", "/opt/homebrew/sbin"}, dirs...)
	}
	m.set("PATH", ensurePathDirs(m.get("PATH"), dirs))

	// UTF-8 yerel ayari: hicbiri UTF-8 degilse (bos veya "C"/"POSIX") yukselt.
	if !hasUTF8(m.get("LC_ALL"), m.get("LC_CTYPE"), m.get("LANG")) {
		loc := "C.UTF-8"
		if o.GOOS == "darwin" {
			loc = "en_US.UTF-8"
		}
		m.set("LANG", loc)
		m.set("LC_ALL", loc)
	}

	// zsh: Zorven profili. Kullanicinin ZDOTDIR'i saklanir, o dizin yuklenir.
	if o.Shell.ID == "zsh" && o.ZshDir != "" {
		userZ := m.get("ZDOTDIR")
		if userZ == o.ZshDir {
			// Zorven terminalinin icinden baslatilmis ajan: asil kullanici
			// ZDOTDIR'ini onceki oturumdan al.
			userZ = ""
			if m.get("ZORVEN_USER_ZDOTDIR_SET") != "" {
				userZ = m.get("ZORVEN_USER_ZDOTDIR")
			}
		}
		if userZ != "" {
			m.set("ZORVEN_USER_ZDOTDIR", userZ)
			m.set("ZORVEN_USER_ZDOTDIR_SET", "1")
		} else {
			m.set("ZORVEN_USER_ZDOTDIR", o.Home)
			m.unset("ZORVEN_USER_ZDOTDIR_SET")
		}
		m.set("ZORVEN_ZDOTDIR", o.ZshDir)
		m.set("ZDOTDIR", o.ZshDir)
		if o.NoPlugins {
			m.set("ZORVEN_NO_PLUGINS", "1")
		}
	}
	return m.list()
}

// buildLaunch, secilen kabuk icin calistirilacak komutu hazirlar.
func buildLaunch(o launchOpts) launchPlan {
	p := launchPlan{Path: o.Shell.Path, Env: buildEnv(o), Dir: o.Home}

	switch o.GOOS {
	case "windows":
		p.Args = []string{o.Shell.Path}
		switch o.Shell.ID {
		case "pwsh", "powershell":
			// UTF-8 girdi/cikti; pwsh'te PSReadLine varsayilan olarak etkindir.
			p.Args = append(p.Args, "-NoLogo", "-NoExit", "-Command",
				"$e=New-Object System.Text.UTF8Encoding $false;"+
					"[Console]::OutputEncoding=$e;[Console]::InputEncoding=$e;$OutputEncoding=$e")
		case "cmd":
			p.Args = append(p.Args, "/K", "chcp 65001 >nul")
		case "wsl":
			p.Args = append(p.Args, "--cd", "~")
		}
	default:
		// go-pty unix'te Args[0]'i yok sayar (argv0 "-zsh" mumkun degil); giris +
		// etkilesimli kabuk bayrakla istenir.
		p.Args = []string{o.Shell.Path}
		switch o.Shell.ID {
		case "bash":
			if o.BashRC != "" {
				// --rcfile giris kabuguyla birlikte okunmaz; rc dosyasi
				// /etc/profile + kullanici profilini kendisi yukler.
				p.Args = append(p.Args, "--rcfile", o.BashRC, "-i")
			} else {
				p.Args = append(p.Args, "-l", "-i")
			}
		default: // zsh, fish, sh, dash, ksh...
			p.Args = append(p.Args, "-l", "-i")
		}
	}
	return p
}

// --- Profil dosyalari ------------------------------------------------------

// writeIfChanged, icerik farkliysa yazar (gereksiz disk yazimini onler).
func writeIfChanged(dst string, data []byte, mode os.FileMode) error {
	// Kabuk betikleri Unix'te calisir: Windows'ta derlenen ikiliye CRLF ile
	// gomulmus olsalar bile LF'e cevir (zsh/bash CRLF'li betigi calistiramaz).
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if old, err := os.ReadFile(dst); err == nil && string(old) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

// extractAssets, gomulu bir alt agaci (src) hedef dizine (dst) yazar.
func extractAssets(src, dst string) error {
	return fs.WalkDir(shellAssets, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, src), "/")
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == ".gitattributes" {
			return nil
		}
		data, err := shellAssets.ReadFile(p)
		if err != nil {
			return err
		}
		return writeIfChanged(target, data, 0o644)
	})
}

// ensureZshProfile, <base>/zsh altinda ZDOTDIR dizinini uretir ve yolunu doner.
// Kullanicinin hicbir dosyasina yazilmaz.
func ensureZshProfile(base string) (string, error) {
	dir := filepath.Join(base, "zsh")
	if err := extractAssets("zshassets/zdotdir", dir); err != nil {
		return "", fmt.Errorf("zsh profili yazilamadi: %w", err)
	}
	for _, plug := range []string{"zsh-autosuggestions", "zsh-syntax-highlighting"} {
		if err := extractAssets(path.Join("zshassets", plug), filepath.Join(dir, "plugins", plug)); err != nil {
			return "", fmt.Errorf("zsh eklentisi yazilamadi: %w", err)
		}
	}
	// Lisans bildirimi eklentilerin yanina da konur.
	if data, err := shellAssets.ReadFile("zshassets/NOTICE"); err == nil {
		_ = writeIfChanged(filepath.Join(dir, "plugins", "NOTICE"), data, 0o644)
	}
	return dir, nil
}

// ensureBashRC, <base>/bash/zorven.bashrc dosyasini uretir ve yolunu doner.
func ensureBashRC(base string) (string, error) {
	data, err := shellAssets.ReadFile("zshassets/zorven.bashrc")
	if err != nil {
		return "", err
	}
	dst := filepath.Join(base, "bash", "zorven.bashrc")
	if err := writeIfChanged(dst, data, 0o644); err != nil {
		return "", fmt.Errorf("bash rcfile yazilamadi: %w", err)
	}
	return dst, nil
}

// profileBase, Zorven profil dosyalari icin yazilabilir bir taban dizin bulur.
func profileBase(home string) []string {
	var cands []string
	if cd, err := os.UserConfigDir(); err == nil && cd != "" {
		cands = append(cands, filepath.Join(cd, "zorven"))
	}
	if home != "" && home != "/" {
		cands = append(cands, filepath.Join(home, ".config", "zorven"))
	}
	cands = append(cands, filepath.Join(os.TempDir(), fmt.Sprintf("zorven-%d", os.Getuid())))
	return cands
}

// resolveUser, Unix'te HOME ve kullanici adini (servis olarak bos olabilir)
// ortamdan, yoksa /etc/passwd'den cozer.
func resolveUser(d discoverDeps, getenv func(string) string) (home, usr string) {
	pe, _ := loginShellOf(d)
	home, usr = getenv("HOME"), getenv("USER")
	if usr == "" {
		usr = getenv("LOGNAME")
	}
	if usr == "" {
		usr = pe.Name
	}
	if usr == "" {
		usr = d.Username
	}
	if home == "" {
		home = pe.Home
	}
	if home == "" {
		if d.GOOS == "windows" {
			home = getenv("USERPROFILE")
		} else {
			home = "/"
		}
	}
	return home, usr
}

// prepareLaunch, secilen kabuk icin gercek makinede komut hazirlar ve Zorven
// profil dosyalarini (acik degilse) yazar. Profil yazilamazsa duz kabuga duser.
func prepareLaunch(sh protocol.ShellInfo, plainZsh, plainBash bool) launchPlan {
	d := realDiscoverDeps()
	home, usr := resolveUser(d, os.Getenv)
	o := launchOpts{
		GOOS:    runtime.GOOS,
		Shell:   sh,
		BaseEnv: os.Environ(),
		Home:    home,
		User:    usr,
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(home); err != nil || !st.IsDir() {
			o.Home = "/"
		}
		wantZsh := sh.ID == "zsh" && !plainZsh
		wantBash := sh.ID == "bash" && !plainBash
		if wantZsh || wantBash {
			for _, base := range profileBase(o.Home) {
				if wantZsh {
					if dir, err := ensureZshProfile(base); err == nil {
						o.ZshDir = dir
						break
					}
				} else if rc, err := ensureBashRC(base); err == nil {
					o.BashRC = rc
					break
				}
			}
		}
	} else if o.Home == "" {
		o.Home = os.Getenv("SystemDrive") + `\`
	}
	return buildLaunch(o)
}
