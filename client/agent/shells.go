package agent

// Uzak terminal icin kabuk kesfi.
//
// GUVENLIK: kabuk yolu ASLA sunucudan gelmez. Ajan acilista kendi makinesindeki
// kabuklari bulur ve sunucuya yalnizca ID listesi olarak bildirir; terminal_open
// icindeki "shell" alani bu listede aranir, bulunamazsa istek reddedilir.

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// shellSet, kesfedilen kabuklar ve varsayilan ID.
type shellSet struct {
	List    []protocol.ShellInfo
	Default string
}

// find, ID'ye gore kabugu bulur. Bos ID varsayilani verir. Listede olmayan
// ID icin ok=false doner (sunucudan gelen rastgele deger kabul edilmez).
func (s shellSet) find(id string) (protocol.ShellInfo, bool) {
	if id == "" {
		id = s.Default
	}
	for _, sh := range s.List {
		if sh.ID == id {
			return sh, true
		}
	}
	return protocol.ShellInfo{}, false
}

var errUnknownShell = errors.New("bilinmeyen kabuk: istemcinin kesfettigi listede yok")

// passwdEntry, /etc/passwd satirinin ilgili alanlari.
type passwdEntry struct {
	Name, Home, Shell string
	UID               int
}

// parsePasswd, uid (veya uid eslesmezse ad) icin passwd kaydini bulur.
func parsePasswd(data []byte, uid int, name string) (passwdEntry, bool) {
	var byName *passwdEntry
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		u, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		e := passwdEntry{Name: f[0], UID: u, Home: f[5], Shell: f[6]}
		if u == uid {
			return e, true
		}
		if name != "" && f[0] == name && byName == nil {
			c := e
			byName = &c
		}
	}
	if byName != nil {
		return *byName, true
	}
	return passwdEntry{}, false
}

// parseEtcShells, /etc/shells'ten mutlak yollari doner.
func parseEtcShells(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "/") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// discoverDeps, kesfin dis dunyaya bagimliliklari (test icin degistirilebilir).
type discoverDeps struct {
	GOOS     string
	UID      int
	Username string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	ReadFile func(string) ([]byte, error)
	Exists   func(string) bool // dosya var ve dizin degil
}

func realDiscoverDeps() discoverDeps {
	d := discoverDeps{
		GOOS:     runtime.GOOS,
		UID:      os.Getuid(),
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		ReadFile: os.ReadFile,
		Exists: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && !st.IsDir()
		},
	}
	if u, err := user.Current(); err == nil {
		d.Username = u.Username
	}
	return d
}

// discoverShells, bu makinedeki kabuklari ve varsayilani bulur.
func discoverShells() shellSet { return discoverShellsWith(realDiscoverDeps()) }

func discoverShellsWith(d discoverDeps) shellSet {
	if d.GOOS == "windows" {
		return discoverWindows(d)
	}
	return discoverUnix(d)
}

func shellDisplayName(id string) string {
	switch id {
	case "zsh":
		return "Zsh"
	case "bash":
		return "Bash"
	case "fish":
		return "Fish"
	case "sh":
		return "sh (POSIX)"
	case "pwsh":
		return "PowerShell 7"
	case "powershell":
		return "Windows PowerShell"
	case "cmd":
		return "Komut Istemi (cmd)"
	case "wsl":
		return "WSL"
	}
	return id
}

// loginShellOf, calisan kullanicinin giris kabugunu doner (bos: bilinmiyor).
func loginShellOf(d discoverDeps) (passwdEntry, string) {
	var pe passwdEntry
	if data, err := d.ReadFile("/etc/passwd"); err == nil {
		if e, ok := parsePasswd(data, d.UID, d.Username); ok {
			pe = e
		}
	}
	sh := pe.Shell
	if sh == "" {
		// macOS gibi passwd'de kullanicinin olmadigi sistemler ve SHELL tanimli ortamlar.
		sh = d.Getenv("SHELL")
	}
	if sh == "" && d.GOOS == "darwin" {
		sh = "/bin/zsh"
	}
	base := filepath.Base(sh)
	if sh == "" || base == "nologin" || base == "false" || base == "git-shell" || !d.Exists(sh) {
		return pe, ""
	}
	return pe, sh
}

func discoverUnix(d discoverDeps) shellSet {
	_, login := loginShellOf(d)

	var etc []string
	if data, err := d.ReadFile("/etc/shells"); err == nil {
		etc = parseEtcShells(data)
	}

	byID := map[string]string{}
	pathFor := func(id string) string {
		if p, err := d.LookPath(id); err == nil && p != "" {
			return p
		}
		for _, p := range etc {
			if filepath.Base(p) == id && d.Exists(p) {
				return p
			}
		}
		return ""
	}
	for _, id := range []string{"zsh", "bash", "fish", "sh"} {
		if p := pathFor(id); p != "" {
			byID[id] = p
		}
	}
	loginID := ""
	if login != "" {
		loginID = filepath.Base(login)
		// Giris kabugunun yolu, ayni adli PATH sonucundan onceliklidir.
		byID[loginID] = login
	}

	// Liste sirasi = varsayilan tercih sirasi: zsh > bash > giris kabugu > sh, sonra fish.
	order := []string{"zsh", "bash"}
	if loginID != "" {
		order = append(order, loginID)
	}
	order = append(order, "sh", "fish")

	var set shellSet
	seen := map[string]bool{}
	for _, id := range order {
		p, ok := byID[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		set.List = append(set.List, protocol.ShellInfo{ID: id, Name: shellDisplayName(id), Path: p})
	}
	for _, id := range []string{"zsh", "bash", loginID, "sh"} {
		if id == "" {
			continue
		}
		if seen[id] {
			set.Default = id
			break
		}
	}
	if set.Default == "" && len(set.List) > 0 {
		set.Default = set.List[0].ID
	}
	return set
}

func discoverWindows(d discoverDeps) shellSet {
	sysRoot := d.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	progFiles := d.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	find := func(exe string, extra ...string) string {
		if p, err := d.LookPath(exe); err == nil && p != "" {
			return p
		}
		for _, p := range extra {
			if d.Exists(p) {
				return p
			}
		}
		return ""
	}

	cands := []struct{ id, path string }{
		{"pwsh", find("pwsh.exe", filepath.Join(progFiles, "PowerShell", "7", "pwsh.exe"))},
		{"powershell", find("powershell.exe", filepath.Join(sysRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))},
	}
	cmdPath := d.Getenv("COMSPEC")
	if cmdPath == "" || !d.Exists(cmdPath) {
		cmdPath = find("cmd.exe", filepath.Join(sysRoot, "System32", "cmd.exe"))
	}
	cands = append(cands,
		struct{ id, path string }{"cmd", cmdPath},
		struct{ id, path string }{"wsl", find("wsl.exe", filepath.Join(sysRoot, "System32", "wsl.exe"))},
	)

	var set shellSet
	for _, c := range cands {
		if c.path == "" {
			continue
		}
		set.List = append(set.List, protocol.ShellInfo{ID: c.id, Name: shellDisplayName(c.id), Path: c.path})
	}
	// Varsayilan: pwsh > powershell > cmd (wsl hicbir zaman varsayilan degil).
	for _, id := range []string{"pwsh", "powershell", "cmd"} {
		for _, sh := range set.List {
			if sh.ID == id {
				set.Default = id
				return set
			}
		}
	}
	if len(set.List) > 0 {
		set.Default = set.List[0].ID
	}
	return set
}
