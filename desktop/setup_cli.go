package main

// Kurulum ve guncelleme icin CLI (zorven.exe) ikilisini imzali manifestten
// indirme ve kimligi (token) Windows servisine aktarma yardimcisi.

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"

	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// cliManifestKey, CLI ikilisinin sunucu manifestindeki anahtari. windows/amd64
// yalniz "+cli" ile yayinlanir (duz anahtar masaustu icindir, bkz. build-clients.sh).
func cliManifestKey(goos, goarch string) string {
	if goos == "windows" && goarch == "amd64" {
		return "windows/amd64+cli"
	}
	return goos + "/" + goarch
}

// updateBaseFor, "host[:443]" sunucu adresinden https taban adresini uretir.
func updateBaseFor(addr string) string {
	if addr == "" || addr == "localhost:8443" {
		addr = defaultServer
	}
	return "https://" + strings.TrimSuffix(addr, ":443")
}

// fetchSignedManifest, base/bin/<name> ve .sig dosyalarini indirir; imza
// dogrulanmadan ayristirilmaz (desktop.json ile ayni yol).
func fetchSignedManifest(ctx context.Context, base, name string) (desktopManifest, error) {
	body, err := fetchUpdateFile(ctx, base+"/bin/"+name)
	if err != nil {
		return desktopManifest{}, err
	}
	sig, err := fetchUpdateFile(ctx, base+"/bin/"+name+".sig")
	if err != nil {
		return desktopManifest{}, errors.New(tm("upd.sigFail"))
	}
	return parseSignedManifest(body, sig, nil)
}

// provisionCLI, CLI ikilisini imzali manifestten indirir, sha256 + boyut + MZ
// dogrular ve gecici dosya yolunu (ve manifest surumunu) doner. Cevrimdisiysa
// acik hata verir; hicbir sey kurulmaz.
func provisionCLI(ctx context.Context, server string) (path, cliVersion string, err error) {
	base := updateBaseFor(server)
	m, err := fetchSignedManifest(ctx, base, "manifest.json")
	if err != nil {
		return "", "", err
	}
	f, ok := m.Files[cliManifestKey(runtime.GOOS, runtime.GOARCH)]
	if !ok || f.Name == "" || f.SHA256 == "" || !validBinName(f.Name) {
		return "", "", errors.New(tm("setup.errNoCLI"))
	}
	path, err = downloadVerified(ctx, base+"/bin/"+f.Name, f.SHA256, f.Size, nil)
	if err != nil {
		return "", "", err
	}
	return path, m.Version, nil
}

// validBinName, manifest dosya adinin yol ayirici icermedigini denetler.
func validBinName(n string) bool {
	return n != "" && !strings.ContainsAny(n, `/\`) && !strings.Contains(n, "..")
}

// runSvcTokenHelper, yukseltilmis yardimci: "--zv-svc-token <token-dosyasi>
// <sunucu>". Token'i sistem ayar dosyasina (ProgramData\zorven, yalniz yonetici
// yazabilir) yazar ve calisan servise canli "reload" gonderir. Kimliksiz bekleyen
// servis dosyayi zaten yoklar (client/service.go waitForToken).
func runSvcTokenHelper(args []string) int {
	if len(args) != 2 && len(args) != 4 {
		return setupUsage
	}
	tf, server := args[0], args[1]
	op := ""
	if len(args) == 4 {
		if args[2] != operatorSIDFlag {
			return setupUsage
		}
		if op = validOperatorSID(args[3]); op == "" {
			return setupUsage
		}
	}
	raw, err := os.ReadFile(tf)
	_ = os.Remove(tf)
	token := strings.TrimSpace(string(raw))
	if err != nil || !svcTokenRe.MatchString(token) || !svcServerRe.MatchString(server) {
		return setupUsage
	}
	cfg, _ := clientcfg.LoadFile(clientcfg.SystemPath())
	cfg.Token, cfg.ServerAddr = token, server
	cfg.AddOperator(op)
	if err := clientcfg.SaveSystem(cfg); err != nil {
		return setupErrSystem
	}
	// Canli servis varsa yeni kimligi hemen alsin (yoksa sessizce gecilir).
	_, _ = localctl.Call(localctl.ServiceEndpoint(), localctl.Request{Op: "reload"})
	return setupOK
}
