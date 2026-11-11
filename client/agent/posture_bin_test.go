package agent

import "testing"

// A5 #17: durus komutlari once bilinen sistem dizinlerinde aranir, PATH son care.
func TestPostureBinPrefersSystemDirs(t *testing.T) {
	have := map[string]bool{
		"/usr/bin/systemctl":        true,
		"/bin/gsettings":            true,
		"/usr/sbin/sysadminctl":     true,
		`C:\Win\System32\netsh.exe`: true,
		`C:\Win\System32\WindowsPowerShell\v1.0\powershell.exe`: true,
	}
	exists := func(p string) bool { return have[p] }
	cases := []struct{ name, goos, want string }{
		{"systemctl", "linux", "/usr/bin/systemctl"},
		{"gsettings", "linux", "/bin/gsettings"},
		{"sysadminctl", "darwin", "/usr/sbin/sysadminctl"},
		{"nft", "linux", "nft"}, // bulunamadi: PATH
		{"/usr/libexec/ApplicationFirewall/socketfilterfw", "darwin", "/usr/libexec/ApplicationFirewall/socketfilterfw"},
		{"netsh", "windows", `C:\Win\System32\netsh.exe`},
		{"powershell.exe", "windows", `C:\Win\System32\WindowsPowerShell\v1.0\powershell.exe`},
		{"manage-bde", "windows", "manage-bde.exe"},
	}
	for _, c := range cases {
		if got := postureBin(c.name, c.goos, `C:\Win`, exists); got != c.want {
			t.Errorf("%s/%s: %q, beklenen %q", c.goos, c.name, got, c.want)
		}
	}
}
