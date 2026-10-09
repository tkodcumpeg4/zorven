package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func fakeDeps(goos string, files map[string]string, onPath map[string]string, env map[string]string, uid int) discoverDeps {
	exists := map[string]bool{}
	for _, p := range onPath {
		exists[p] = true
	}
	return discoverDeps{
		GOOS:     goos,
		UID:      uid,
		Username: "root",
		Getenv:   func(k string) string { return env[k] },
		LookPath: func(n string) (string, error) {
			if p, ok := onPath[n]; ok {
				return p, nil
			}
			return "", errors.New("yok")
		},
		ReadFile: func(p string) ([]byte, error) {
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}
			return nil, errors.New("yok")
		},
		Exists: func(p string) bool {
			if exists[p] {
				return true
			}
			_, ok := files[p]
			return ok
		},
	}
}

func ids(s shellSet) string {
	var o []string
	for _, sh := range s.List {
		o = append(o, sh.ID)
	}
	return strings.Join(o, ",")
}

func TestParsePasswdByUID(t *testing.T) {
	data := "root:x:0:0:root:/root:/bin/zsh\nbob:x:1000:1000::/home/bob:/bin/fish\n# yorum\n"
	e, ok := parsePasswd([]byte(data), 1000, "")
	if !ok || e.Shell != "/bin/fish" || e.Home != "/home/bob" {
		t.Fatalf("beklenmeyen kayit: %+v ok=%v", e, ok)
	}
	if _, ok := parsePasswd([]byte(data), 55, ""); ok {
		t.Fatal("olmayan uid bulunmamali")
	}
	if e, ok := parsePasswd([]byte(data), 55, "bob"); !ok || e.UID != 1000 {
		t.Fatal("ad ile bulunmali")
	}
}

func TestDiscoverUnixPrefersZshThenBash(t *testing.T) {
	d := fakeDeps("linux",
		map[string]string{
			"/etc/passwd": "root:x:0:0:root:/root:/bin/fish\n",
			"/etc/shells": "# x\n/bin/sh\n/bin/bash\n/usr/bin/fish\n",
			"/bin/fish":   "", "/usr/bin/fish": "",
		},
		map[string]string{"zsh": "/usr/bin/zsh", "bash": "/bin/bash", "sh": "/bin/sh"},
		map[string]string{}, 0)
	s := discoverShellsWith(d)
	if s.Default != "zsh" {
		t.Fatalf("varsayilan zsh olmali: %q (%s)", s.Default, ids(s))
	}
	if got := ids(s); got != "zsh,bash,fish,sh" {
		t.Fatalf("sira: %s", got)
	}
	// Giris kabugunun yolu PATH sonucundan onceliklidir.
	for _, sh := range s.List {
		if sh.ID == "fish" && sh.Path != "/bin/fish" {
			t.Fatalf("fish yolu giris kabugundan gelmeli: %s", sh.Path)
		}
	}
}

func TestDiscoverUnixLoginShellBeforeSh(t *testing.T) {
	// zsh/bash yok: giris kabugu (fish) sh'ten once varsayilan.
	d := fakeDeps("linux",
		map[string]string{
			"/etc/passwd": "svc:x:5:5::/home/svc:/usr/bin/fish\n",
			"/usr/bin/fish": "",
		},
		map[string]string{"sh": "/bin/sh"},
		map[string]string{}, 5)
	s := discoverShellsWith(d)
	if s.Default != "fish" {
		t.Fatalf("varsayilan fish olmali: %q", s.Default)
	}
}

func TestDiscoverUnixIgnoresNologin(t *testing.T) {
	d := fakeDeps("linux",
		map[string]string{
			"/etc/passwd":      "svc:x:5:5::/nonexistent:/usr/sbin/nologin\n",
			"/usr/sbin/nologin": "",
		},
		map[string]string{"sh": "/bin/sh"},
		map[string]string{"SHELL": ""}, 5)
	s := discoverShellsWith(d)
	if got := ids(s); got != "sh" || s.Default != "sh" {
		t.Fatalf("yalnizca sh beklenirdi: %s / %s", got, s.Default)
	}
}

func TestDiscoverUnixRootWithoutShellEnv(t *testing.T) {
	// systemd altinda root: SHELL yok, passwd'den bash gelir.
	d := fakeDeps("linux",
		map[string]string{"/etc/passwd": "root:x:0:0:root:/root:/bin/bash\n"},
		map[string]string{"bash": "/bin/bash", "sh": "/bin/sh"},
		map[string]string{}, 0)
	s := discoverShellsWith(d)
	if s.Default != "bash" {
		t.Fatalf("varsayilan bash: %q", s.Default)
	}
}

func TestDiscoverWindowsOrder(t *testing.T) {
	d := fakeDeps("windows", nil,
		map[string]string{
			"pwsh.exe": `C:\Program Files\PowerShell\7\pwsh.exe`, "powershell.exe": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
			"cmd.exe": `C:\Windows\System32\cmd.exe`, "wsl.exe": `C:\Windows\System32\wsl.exe`,
		},
		map[string]string{"COMSPEC": `C:\Windows\System32\cmd.exe`}, 0)
	s := discoverShellsWith(d)
	if got := ids(s); got != "pwsh,powershell,cmd,wsl" {
		t.Fatalf("sira: %s", got)
	}
	if s.Default != "pwsh" {
		t.Fatalf("varsayilan pwsh: %q", s.Default)
	}

	// pwsh yoksa powershell, o da yoksa cmd; wsl asla varsayilan degil.
	d2 := fakeDeps("windows", nil,
		map[string]string{"cmd.exe": `C:\Windows\System32\cmd.exe`, "wsl.exe": `C:\Windows\System32\wsl.exe`},
		map[string]string{}, 0)
	s2 := discoverShellsWith(d2)
	if s2.Default != "cmd" {
		t.Fatalf("varsayilan cmd: %q (%s)", s2.Default, ids(s2))
	}
}

func TestShellSetAllowlist(t *testing.T) {
	s := shellSet{
		List:    []protocol.ShellInfo{{ID: "zsh", Path: "/bin/zsh"}, {ID: "sh", Path: "/bin/sh"}},
		Default: "zsh",
	}
	if sh, ok := s.find(""); !ok || sh.ID != "zsh" {
		t.Fatal("bos ID varsayilani vermeli")
	}
	if sh, ok := s.find("sh"); !ok || sh.Path != "/bin/sh" {
		t.Fatal("sh bulunmali")
	}
	for _, bad := range []string{"/bin/evil", "../../bin/sh", "bash", "ZSH", "zsh ", "powershell"} {
		if _, ok := s.find(bad); ok {
			t.Fatalf("listede olmayan kabuk kabul edildi: %q", bad)
		}
	}
}
