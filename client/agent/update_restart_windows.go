//go:build windows

package agent

import (
	"os"
	"os/exec"
	"time"
)

// restartSelf, guncellenen ikiliyle yeniden baslar. exe yolu APPLY'DAN ONCE
// yakalanmis olmalidir (bkz. maybeUpdate).
//
// Windows'ta calisan bir .exe kendini exec ile yerinde degistiremez; selfupdate
// zaten diske YENI ikiliyi yazdi. Iki mod:
//   - Servis: ayri bir process'ten "zorven service restart" cagirilir; SCM bu
//     (eski) process'i durdurup guncellenmis ikiliyle yeniden baslatir.
//   - Etkilesimli: yeni ikili ayni argumanlarla baslatilir, mevcut process cikar.
func (a *Agent) restartSelf(exe string) {
	if a.IsService {
		cmd := exec.Command(exe, "service", "restart")
		if err := cmd.Start(); err != nil {
			a.Log.Error("restart: servis yeniden baslatma cagrilamadi", "hata", err)
		}
		// SCM bizi durduracak; olmazsa fallback olarak cikiyoruz.
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		a.Log.Error("restart: yeni process baslatilamadi", "hata", err, "yol", exe)
		os.Exit(0)
	}
	os.Exit(0)
}
