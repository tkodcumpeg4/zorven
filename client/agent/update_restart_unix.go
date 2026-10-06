//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// restartSelf, guncellenen ikiliyle yeniden baslar.
//
// exe yolu APPLY'DAN ONCE yakalanmis olmalidir (bkz. maybeUpdate): Linux'ta
// selfupdate eski inode'u unlink ettigi icin sonradan os.Executable() "(deleted)"
// dondurur ve exec ENOENT verir.
//
// Unix'te process imajini yerinde degistiririz (syscall.Exec): PID KORUNUR,
// dolayisiyla systemd MainPID'yi izlemeye devam eder. Etkilesimli calismada da
// ayni sekilde calisir.
func (a *Agent) restartSelf(exe string) {
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		a.Log.Error("restart: exec basarisiz", "hata", err, "yol", exe)
		os.Exit(0)
	}
}
