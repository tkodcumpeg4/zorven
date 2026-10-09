package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func envGet(env []string, k string) (string, bool) {
	for _, kv := range env {
		if strings.HasPrefix(kv, k+"=") {
			return kv[len(k)+1:], true
		}
	}
	return "", false
}

func TestBuildEnvServiceMinimal(t *testing.T) {
	// systemd servisi: neredeyse bos ortam.
	env := buildEnv(launchOpts{
		GOOS:    "linux",
		Shell:   protocol.ShellInfo{ID: "bash", Path: "/bin/bash"},
		BaseEnv: []string{"PATH=/usr/bin"},
		Home:    "/root",
		User:    "root",
	})
	want := map[string]string{
		"TERM": "xterm-256color", "COLORTERM": "truecolor", "HOME": "/root",
		"USER": "root", "LOGNAME": "root", "SHELL": "/bin/bash",
		"LANG": "C.UTF-8", "LC_ALL": "C.UTF-8",
	}
	for k, v := range want {
		if got, _ := envGet(env, k); got != v {
			t.Errorf("%s=%q, beklenen %q", k, got, v)
		}
	}
	path, _ := envGet(env, "PATH")
	if !strings.HasPrefix(path, "/usr/bin:") || !strings.Contains(path, "/usr/local/bin") || !strings.Contains(path, "/sbin") {
		t.Errorf("PATH tamamlanmadi: %s", path)
	}
	if strings.Contains(path, "/usr/bin:/usr/bin") {
		t.Errorf("PATH'te tekrar var: %s", path)
	}
}

func TestBuildEnvKeepsUTF8AndUser(t *testing.T) {
	env := buildEnv(launchOpts{
		GOOS:    "linux",
		Shell:   protocol.ShellInfo{ID: "sh", Path: "/bin/sh"},
		BaseEnv: []string{"LANG=tr_TR.UTF-8", "USER=ali", "PATH=/a:/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/usr/local/sbin"},
		Home:    "/home/ali", User: "ali",
	})
	if v, _ := envGet(env, "LANG"); v != "tr_TR.UTF-8" {
		t.Errorf("UTF-8 LANG korunmali: %q", v)
	}
	if _, ok := envGet(env, "LC_ALL"); ok {
		t.Error("LC_ALL eklenmemeli")
	}
	if v, _ := envGet(env, "PATH"); v != "/a:/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/usr/local/sbin" {
		t.Errorf("PATH degismemeli: %s", v)
	}
}

func TestBuildEnvForcesUTF8OverC(t *testing.T) {
	env := buildEnv(launchOpts{
		GOOS: "linux", Shell: protocol.ShellInfo{ID: "sh", Path: "/bin/sh"},
		BaseEnv: []string{"LANG=C", "LC_ALL=POSIX"}, Home: "/root", User: "root",
	})
	if v, _ := envGet(env, "LC_ALL"); v != "C.UTF-8" {
		t.Errorf("LC_ALL=%q", v)
	}
}

func TestBuildEnvDarwinLocale(t *testing.T) {
	env := buildEnv(launchOpts{GOOS: "darwin", Shell: protocol.ShellInfo{ID: "zsh", Path: "/bin/zsh"}, Home: "/Users/x", User: "x"})
	if v, _ := envGet(env, "LANG"); v != "en_US.UTF-8" {
		t.Errorf("LANG=%q", v)
	}
	if p, _ := envGet(env, "PATH"); !strings.Contains(p, "/opt/homebrew/bin") {
		t.Errorf("homebrew PATH eksik: %s", p)
	}
}

func TestBuildEnvZshProfile(t *testing.T) {
	env := buildEnv(launchOpts{
		GOOS: "linux", Shell: protocol.ShellInfo{ID: "zsh", Path: "/bin/zsh"},
		BaseEnv: []string{"ZDOTDIR=/home/ali/.config/zsh"}, Home: "/home/ali", User: "ali",
		ZshDir: "/home/ali/.config/zorven/zsh",
	})
	checks := map[string]string{
		"ZDOTDIR": "/home/ali/.config/zorven/zsh", "ZORVEN_ZDOTDIR": "/home/ali/.config/zorven/zsh",
		"ZORVEN_USER_ZDOTDIR": "/home/ali/.config/zsh", "ZORVEN_USER_ZDOTDIR_SET": "1",
	}
	for k, v := range checks {
		if got, _ := envGet(env, k); got != v {
			t.Errorf("%s=%q, beklenen %q", k, got, v)
		}
	}

	// Kullanici ZDOTDIR'i yoksa HOME kullanilir.
	env = buildEnv(launchOpts{
		GOOS: "linux", Shell: protocol.ShellInfo{ID: "zsh", Path: "/bin/zsh"},
		Home: "/root", User: "root", ZshDir: "/root/.config/zorven/zsh",
	})
	if v, _ := envGet(env, "ZORVEN_USER_ZDOTDIR"); v != "/root" {
		t.Errorf("ZORVEN_USER_ZDOTDIR=%q", v)
	}
	if _, ok := envGet(env, "ZORVEN_USER_ZDOTDIR_SET"); ok {
		t.Error("SET isareti olmamali")
	}

	// bash icin ZDOTDIR kurulmaz.
	env = buildEnv(launchOpts{GOOS: "linux", Shell: protocol.ShellInfo{ID: "bash", Path: "/bin/bash"},
		Home: "/root", User: "root", ZshDir: "/x"})
	if _, ok := envGet(env, "ZDOTDIR"); ok {
		t.Error("bash icin ZDOTDIR olmamali")
	}
}

func TestBuildLaunchUnixArgs(t *testing.T) {
	zsh := buildLaunch(launchOpts{GOOS: "linux", Shell: protocol.ShellInfo{ID: "zsh", Path: "/bin/zsh"}, Home: "/root"})
	if strings.Join(zsh.Args, " ") != "/bin/zsh -l -i" || zsh.Dir != "/root" {
		t.Errorf("zsh: %v dir=%s", zsh.Args, zsh.Dir)
	}
	bash := buildLaunch(launchOpts{GOOS: "linux", Shell: protocol.ShellInfo{ID: "bash", Path: "/bin/bash"}, Home: "/root", BashRC: "/rc"})
	if strings.Join(bash.Args, " ") != "/bin/bash --rcfile /rc -i" {
		t.Errorf("bash rc: %v", bash.Args)
	}
	bash = buildLaunch(launchOpts{GOOS: "linux", Shell: protocol.ShellInfo{ID: "bash", Path: "/bin/bash"}, Home: "/root"})
	if strings.Join(bash.Args, " ") != "/bin/bash -l -i" {
		t.Errorf("bash duz: %v", bash.Args)
	}
}

func TestBuildLaunchWindows(t *testing.T) {
	ps := buildLaunch(launchOpts{GOOS: "windows", Shell: protocol.ShellInfo{ID: "pwsh", Path: `C:\pwsh.exe`}, Home: `C:\Users\a`})
	j := strings.Join(ps.Args, " ")
	if !strings.Contains(j, "-NoLogo") || !strings.Contains(j, "UTF8Encoding") || !strings.Contains(j, "OutputEncoding") {
		t.Errorf("pwsh args: %s", j)
	}
	cmd := buildLaunch(launchOpts{GOOS: "windows", Shell: protocol.ShellInfo{ID: "cmd", Path: `C:\cmd.exe`}})
	if !strings.Contains(strings.Join(cmd.Args, " "), "chcp 65001") {
		t.Errorf("cmd args: %v", cmd.Args)
	}
	if v, _ := envGet(ps.Env, "TERM"); v != "xterm-256color" {
		t.Error("TERM kurulmadi")
	}
}

func TestEnsureZshProfile(t *testing.T) {
	base := t.TempDir()
	dir, err := ensureZshProfile(base)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(base, "zsh") {
		t.Fatalf("dizin: %s", dir)
	}
	rc, err := os.ReadFile(filepath.Join(dir, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(rc)
	iUser := strings.Index(s, `source "$ZDOTDIR/.zshrc"`)
	iDef := strings.Index(s, "HISTSIZE=100000")
	iHL := strings.Index(s, "zsh-syntax-highlighting.zsh")
	if iUser < 0 || iDef < 0 || iUser > iDef {
		t.Fatalf("kullanici .zshrc'si Zorven varsayilanlarindan ONCE yuklenmeli (%d,%d)", iUser, iDef)
	}
	if iHL < iDef {
		t.Fatal("sozdizimi vurgulama en sonda yuklenmeli")
	}
	for _, f := range []string{".zshenv", ".zprofile", ".zlogin",
		"plugins/zsh-autosuggestions/zsh-autosuggestions.zsh",
		"plugins/zsh-autosuggestions/LICENSE",
		"plugins/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh",
		"plugins/zsh-syntax-highlighting/COPYING.md",
		"plugins/zsh-syntax-highlighting/.version",
		"plugins/zsh-syntax-highlighting/highlighters/main/main-highlighter.zsh",
		"plugins/NOTICE"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("eksik dosya %s: %v", f, err)
		}
	}
	// Ikinci cagri idempotent.
	if _, err := ensureZshProfile(base); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureBashRC(t *testing.T) {
	p, err := ensureBashRC(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if i, j := strings.Index(s, `. "$HOME/.bashrc"`), strings.Index(s, "HISTSIZE=100000"); i < 0 || j < 0 || i > j {
		t.Fatal("bashrc varsayilanlardan once yuklenmeli")
	}
}

// Kullanicinin dotfile'larina yazilmadigi: profil yalnizca verilen taban dizine yazilir.
func TestProfileNeverTouchesHome(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(t.TempDir(), "zorven")
	if _, err := ensureZshProfile(base); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Fatalf("HOME'a yazildi: %v", ents)
	}
}
