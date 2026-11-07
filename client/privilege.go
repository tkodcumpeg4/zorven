package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

const (
	flagNoElevate = "no-elevate"
	flagElevated  = "zv-elevated" // dahili: yukseltilmis alt surec
	flagLogFile   = "zv-log-file" // dahili: yukseltilmis surecin ciktisini yazacagi dosya
)

var (
	noElevate    bool // --no-elevate: otomatik yukseltme yok (betikler icin)
	elevatedProc bool // --zv-elevated: bu surec UAC ile yeniden baslatildi
)

// addPrivilegeFlags, kok komuta yetki bayraklarini ekler.
func addPrivilegeFlags(root *cobra.Command) {
	root.PersistentFlags().BoolVar(&noElevate, flagNoElevate, false, i18n.T("svc.priv.flag.noelev"))
	root.PersistentFlags().BoolVar(&elevatedProc, flagElevated, false, "")
	_ = root.PersistentFlags().MarkHidden(flagElevated)
	root.PersistentFlags().StringVar(&elevatedLog, flagLogFile, "", "")
	_ = root.PersistentFlags().MarkHidden(flagLogFile)
}

var elevatedLog string // --zv-log-file

// logFileFromArgs, cobra ayrisitirmadan ONCE argumanlardan --zv-log-file degerini
// okur: cikti kopyalama erken (herhangi bir komut yazmadan once) kurulmali.
func logFileFromArgs(args []string) string {
	for i, a := range args {
		if a == "--"+flagLogFile && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--"+flagLogFile+"="); ok {
			return v
		}
	}
	return ""
}

// teeOutputToFile, os.Stdout ve os.Stderr'i hem asil hedefe hem path dosyasina
// yazar (yukseltilmis alt surec: ust surec dosyayi okuyup kendi terminaline
// basar; yeni UAC penceresi kapaninca cikti kaybolmasin). Donen fonksiyon
// tamponu bosaltir ve eski hedefleri geri koyar; os.Exit'ten once cagrilmali.
func teeOutputToFile(path string) func() {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return func() {}
	}
	origOut, origErr := os.Stdout, os.Stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		f.Close()
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, rerr := pr.Read(buf)
			if n > 0 {
				_, _ = origOut.Write(buf[:n])
				_, _ = f.Write(buf[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()
	os.Stdout, os.Stderr = pw, pw
	return func() {
		_ = pw.Close()
		<-done
		_ = f.Sync()
		_ = f.Close()
		os.Stdout, os.Stderr = origOut, origErr
	}
}

// requireAdmin, yonetici/root yetkisi isteyen komutlarin PreRunE'sidir.
// Linux/macOS'ta yetki yoksa tam `sudo zorven ...` komutunu veren cevrilmis bir
// hata doner. Windows'ta komutu UAC ile yukseltilmis olarak yeniden baslatir,
// bitmesini bekler ve bu sureci ayni cikis koduyla sonlandirir.
func requireAdmin(cmd *cobra.Command, _ []string) error {
	if isAdmin() {
		return nil
	}
	if runtime.GOOS != "windows" {
		return errors.New(i18n.T("svc.priv.root", sudoCommand(os.Args)))
	}
	if noElevate || elevatedProc || os.Getenv("ZORVEN_NO_ELEVATE") == "1" {
		return errors.New(i18n.T("svc.priv.win.noelev"))
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("svc.priv.win.err"), err)
	}
	fmt.Println(i18n.T("svc.priv.win.relaunch"))
	// Yukseltilmis alt surec ayri bir pencerede calisir; ciktisi bu dosyaya da
	// yazilir ve bitince buraya basilir.
	logPath := ""
	if lf, lerr := os.CreateTemp("", "zorven-elevated-*.log"); lerr == nil {
		logPath = lf.Name()
		lf.Close()
		defer os.Remove(logPath)
	}
	args := elevatedArgs(os.Args[1:])
	if logPath != "" {
		args = append([]string{"--" + flagLogFile, logPath}, args...)
	}
	code, err := runElevatedWait(exe, windowsCmdLine(args))
	if logPath != "" {
		if b, rerr := os.ReadFile(logPath); rerr == nil && len(b) > 0 {
			fmt.Print(string(b))
		}
	}
	if err != nil {
		if isUACDeclined(err) {
			return errors.New(i18n.T("svc.priv.win.declined"))
		}
		return fmt.Errorf("%s: %w", i18n.T("svc.priv.win.err"), err)
	}
	fmt.Println(i18n.T("svc.priv.win.done", code))
	os.Exit(int(code))
	return nil
}

// pauseIfElevatedFailed, yukseltilmis (yeni pencerede calisan) surec hata ile
// bitiyorsa pencere kapanmadan once Enter bekler; hata metni okunabilsin.
func pauseIfElevatedFailed(err error) {
	if err == nil || !elevatedProc {
		return
	}
	fmt.Fprint(os.Stderr, "\n"+i18n.T("svc.priv.pause"))
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// elevatedArgs, yukseltilmis alt surece verilecek argumanlari kurar: --no-elevate
// atilir, dahili --zv-elevated bayragi en basa eklenir.
func elevatedArgs(args []string) []string {
	out := []string{"--" + flagElevated}
	for _, a := range args {
		if a == "--"+flagNoElevate {
			continue
		}
		out = append(out, a)
	}
	return out
}

// sudoCommand, calisan komutun `sudo ...` karsiligini (kabuk icin tirnaklanmis)
// uretir. args os.Args bicimindedir (args[0] program).
func sudoCommand(args []string) string {
	parts := []string{"sudo"}
	for i, a := range args {
		if i > 0 && (a == "--"+flagNoElevate || a == "--"+flagElevated) {
			continue
		}
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote, POSIX kabugu icin guvenli tirnaklama yapar.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("@%+=:,./-_", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// windowsCmdLine, argumanlari CommandLineToArgvW kurallarina gore tek komut
// satirina birlestirir.
func windowsCmdLine(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = escapeWinArg(a)
	}
	return strings.Join(q, " ")
}

// escapeWinArg, syscall.EscapeArg ile ayni kurallari uygular (o yalniz Windows
// derlemesinde bulundugu icin burada, testlenebilir biciminde durur).
func escapeWinArg(s string) string {
	if s == "" {
		return `""`
	}
	needs := false
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '"' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var b []byte
	b = append(b, '"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b = append(b, '\\')
			}
			b = append(b, '\\')
		default:
			slashes = 0
		}
		b = append(b, c)
	}
	for ; slashes > 0; slashes-- {
		b = append(b, '\\')
	}
	b = append(b, '"')
	return string(b)
}
