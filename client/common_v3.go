package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"golang.org/x/term"
)

// ---- renk ----------------------------------------------------------------

var (
	colorOnce  sync.Once
	colorOnVal bool
)

// colorEnabled: ANSI renk yalniz stdout bir terminalse ve NO_COLOR tanimli
// degilse kullanilir (boruya/dosyaya yazarken kacis dizisi karismasin).
func colorEnabled() bool {
	colorOnce.Do(func() {
		if _, ok := os.LookupEnv("NO_COLOR"); ok {
			return
		}
		if os.Getenv("TERM") == "dumb" {
			return
		}
		colorOnVal = term.IsTerminal(int(os.Stdout.Fd()))
	})
	return colorOnVal
}

// paint, renk aciksa s'yi verilen SGR koduyla sarar.
func paint(code, s string) string {
	if !colorEnabled() {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// ---- sunucu / kimlik cozumleme -------------------------------------------

const defaultServer = "zorven.app:443"

// resolveServerAddr, sunucu adresini tek kuralla cozer: bayrak > ZORVEN_SERVER
// > ayar dosyasi > varsayilan. (Eskiden her komut kendi kopyasini tutuyordu.)
func resolveServerAddr(flagVal string, cfg config.Config) string {
	srv := strings.TrimSpace(flagVal)
	if srv == "" {
		srv = strings.TrimSpace(os.Getenv("ZORVEN_SERVER"))
	}
	if srv == "" {
		srv = strings.TrimSpace(cfg.ServerAddr)
	}
	if srv == "" || srv == "localhost:8443" {
		srv = defaultServer
	}
	return srv
}

var plaintextWarned sync.Once

// warnPlaintext, --insecure (ws://, TLS yok) kullanildiginda token'in aciktan
// gidecegini stderr'de yuksek sesle soyler (surec basina bir kez).
func warnPlaintext(insecure bool) {
	if !insecure {
		return
	}
	plaintextWarned.Do(func() {
		fmt.Fprintln(os.Stderr, paint("1;33", i18n.T("v3.warn.plaintext")))
	})
}

func isAgentToken(t string) bool {
	return strings.HasPrefix(t, "zrv_live_") || strings.HasPrefix(t, "rpsh_live_")
}

// readTokenFromFile, dosyadan (ya da "-" ile stdin'den) tek satir token okur.
// Dosya SILINMEZ (service install'daki --token-file'in aksine kalicidir).
func readTokenFromFile(path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4096))
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("v3.err.tokenfile.read"), err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" || len(t) > 512 || strings.ContainsAny(t, " \t\r\n") {
		return "", errors.New(i18n.T("svc.err.tokenfile.invalid"))
	}
	return t, nil
}

// apiTokenSources, API token'in nereden cozuldugunu da soyler (config show icin).
func lookupAPIToken(flagTok, fileFlag string) (tok, source string, err error) {
	if t := strings.TrimSpace(flagTok); t != "" {
		return t, "flag", nil
	}
	if fileFlag != "" {
		t, err := readTokenFromFile(fileFlag)
		if err != nil {
			return "", "", err
		}
		return t, "file", nil
	}
	if t := strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN")); t != "" {
		return t, "env", nil
	}
	if t := config.LoadAPIToken(); t != "" {
		return t, "saved", nil
	}
	return "", "", nil
}

// resolveAPIToken: --api-token > --api-token-file > ZORVEN_API_TOKEN > kayitli
// (zorven login). Bos donebilir; cagiran uygun hatayi verir.
func resolveAPIToken(flagTok, fileFlag string) (string, error) {
	t, _, err := lookupAPIToken(flagTok, fileFlag)
	return t, err
}

// explicitAPIToken: kayitli token HARIC (access komutu: ajan varsa o tercih edilir).
func explicitAPIToken(flagTok, fileFlag string) (string, error) {
	if t := strings.TrimSpace(flagTok); t != "" {
		return t, nil
	}
	if fileFlag != "" {
		return readTokenFromFile(fileFlag)
	}
	return strings.TrimSpace(os.Getenv("ZORVEN_API_TOKEN")), nil
}

// readSecret, terminalde ise yankisiz (x/term.ReadPassword), degilse stdin'den
// satir okur.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func stdinIsTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// confirm, e/y onayi ister; terminal degilse (ve force degilse) false doner.
func confirm(prompt string) bool {
	if !stdinIsTTY() {
		return false
	}
	fmt.Fprint(os.Stderr, prompt+" [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "e", "evet":
		return true
	}
	return false
}
