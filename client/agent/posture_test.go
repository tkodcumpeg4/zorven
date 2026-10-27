package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestParseFdesetup(t *testing.T) {
	for in, want := range map[string]string{
		"FileVault is On.\n":  "yes",
		"FileVault is Off.\n": "no",
		"Error: not root":     "unknown",
		"":                    "unknown",
	} {
		if got := parseFdesetup(in); got != want {
			t.Errorf("%q -> %s, beklenen %s", in, got, want)
		}
	}
}

func TestParseSocketFilterFW(t *testing.T) {
	for in, want := range map[string]string{
		"Firewall is enabled. (State = 1)\n":  "yes",
		"Firewall is enabled. (State = 2)\n":  "yes",
		"Firewall is disabled. (State = 0)\n": "no",
		"garbage":                             "unknown",
	} {
		if got := parseSocketFilterFW(in); got != want {
			t.Errorf("%q -> %s, beklenen %s", in, got, want)
		}
	}
}

func TestParseSysadminctlScreenLock(t *testing.T) {
	for in, want := range map[string]string{
		"2025-01-01 10:00:00 sysadminctl[123:456] screenLock delay is immediate\n": "yes",
		"2025-01-01 10:00:00 sysadminctl[123:456] screenLock is off\n":             "no",
		"": "unknown",
	} {
		if got := parseSysadminctlScreenLock(in); got != want {
			t.Errorf("%q -> %s, beklenen %s", in, got, want)
		}
	}
}

func TestParseWindowsVer(t *testing.T) {
	v, b := parseWindowsVer("\r\nMicrosoft Windows [Version 10.0.22631.3593]\r\n")
	if v != "10.0.22631" || b != "10.0.22631.3593" {
		t.Errorf("got %q %q", v, b)
	}
	// Turkce on ek
	v, _ = parseWindowsVer("Microsoft Windows [Sürüm 10.0.19045.4170]")
	if v != "10.0.19045" {
		t.Errorf("yerellesmis: %q", v)
	}
	if v, _ := parseWindowsVer("hata"); v != "" {
		t.Errorf("bos beklenirdi: %q", v)
	}
}

func TestParseBitLocker(t *testing.T) {
	cases := map[string]string{
		"1\r\n": "yes", "0\r\n": "no", "2": "unknown", "": "unknown",
		"Volume C: [OS]\n    Conversion Status:    Fully Encrypted\n    Protection Status:    Protection On\n": "yes",
		"    Protection Status:    Protection Off\n":                                                           "no",
		"ERROR: access denied": "unknown",
	}
	for in, want := range cases {
		if got := parseBitLockerStatus(in); got != want {
			t.Errorf("%q -> %s, beklenen %s", in, got, want)
		}
	}
}

func TestParseWindowsFirewall(t *testing.T) {
	if got := parseNetFirewallProfiles("True\r\nTrue\r\nTrue\r\n"); got != "yes" {
		t.Errorf("hepsi acik: %s", got)
	}
	if got := parseNetFirewallProfiles("True\r\nFalse\r\nTrue\r\n"); got != "no" {
		t.Errorf("biri kapali: %s", got)
	}
	if got := parseNetFirewallProfiles("Get-NetFirewallProfile : not recognized"); got != "unknown" {
		t.Errorf("hata cikti: %s", got)
	}
	en := "Domain Profile Settings: \n----------------------------------------------------------------------\nState                                 ON\n\nPrivate Profile Settings: \n------\nState                                 ON\n\nPublic Profile Settings: \n------\nState                                 OFF\n"
	if got := parseNetshFirewall(en); got != "no" {
		t.Errorf("netsh en: %s", got)
	}
	tr := "Etki Alanı Profili Ayarları:\n------\nDurum                                 AÇIK\n\nÖzel Profil Ayarları:\n------\nDurum                                 AÇIK\n"
	if got := parseNetshFirewall(tr); got != "yes" {
		t.Errorf("netsh tr: %s", got)
	}
}

func TestParseRegScreenLock(t *testing.T) {
	yes := "HKEY_CURRENT_USER\\Control Panel\\Desktop\n    ScreenSaveActive    REG_SZ    1\n    ScreenSaverIsSecure    REG_SZ    1\n"
	if got := parseRegScreenLock(yes); got != "yes" {
		t.Errorf("got %s", got)
	}
	if got := parseRegScreenLock("    ScreenSaveActive    REG_SZ    0\n"); got != "unknown" {
		t.Errorf("kapali ekran koruyucu 'no' dememeli: %s", got)
	}
}

func TestParseOSRelease(t *testing.T) {
	s := "NAME=\"Ubuntu\"\nVERSION_ID=\"22.04\"\nID=ubuntu\n"
	if v := parseOSReleaseVersion(s); v != "22.04" {
		t.Errorf("got %q", v)
	}
	if v := parseOSReleaseVersion("NAME=Arch\n"); v != "" {
		t.Errorf("rolling dagitimda bos beklenirdi: %q", v)
	}
}

func TestParseRootDevice(t *testing.T) {
	m := "sysfs /sys sysfs rw 0 0\n/dev/mapper/vg-root / ext4 rw 0 0\n/dev/sda1 /boot ext4 rw 0 0\n"
	if d := parseRootDevice(m); d != "/dev/mapper/vg-root" {
		t.Errorf("got %q", d)
	}
}

// fakeFS, /sys ve /proc icin sahte dosya sistemi.
func fakeEnv(files map[string]string, dirs map[string][]string, links map[string]string) postureEnv {
	return postureEnv{
		goos: "linux",
		run:  func(context.Context, string, ...string) (string, error) { return "", errors.New("yok") },
		readFile: func(p string) ([]byte, error) {
			if v, ok := files[p]; ok {
				return []byte(v), nil
			}
			return nil, errors.New("yok")
		},
		readDir: func(p string) ([]string, error) {
			if v, ok := dirs[p]; ok {
				return v, nil
			}
			return nil, errors.New("yok")
		},
		evalLinks: func(p string) (string, error) {
			if v, ok := links[p]; ok {
				return v, nil
			}
			return p, nil
		},
	}
}

func TestLinuxDiskLUKSDirect(t *testing.T) {
	e := fakeEnv(
		map[string]string{
			"/proc/mounts":            "/dev/mapper/cryptroot / ext4 rw 0 0\n",
			"/sys/block/dm-0/dm/uuid": "CRYPT-LUKS2-abcd-cryptroot\n",
		},
		map[string][]string{"/sys/block/dm-0": {"dm"}},
		map[string]string{"/dev/mapper/cryptroot": "/dev/dm-0"},
	)
	if got := linuxDisk(e); got != "yes" {
		t.Errorf("LUKS dogrudan: %s", got)
	}
}

func TestLinuxDiskLVMOnLUKS(t *testing.T) {
	e := fakeEnv(
		map[string]string{
			"/proc/mounts":            "/dev/mapper/vg-root / ext4 rw 0 0\n",
			"/sys/block/dm-1/dm/uuid": "LVM-xyz\n",
			"/sys/block/dm-0/dm/uuid": "CRYPT-LUKS2-abcd-luks\n",
		},
		map[string][]string{
			"/sys/block/dm-1":            {"dm", "slaves"},
			"/sys/block/dm-1/slaves":     {"dm-0"},
			"/sys/block/dm-0":            {"dm"},
			"/sys/block/dm-0/slaves":     {"nvme0n1p3"},
			"/sys/class/block/nvme0n1p3": {},
		},
		map[string]string{"/dev/mapper/vg-root": "/dev/dm-1"},
	)
	if got := linuxDisk(e); got != "yes" {
		t.Errorf("LVM-on-LUKS: %s", got)
	}
}

func TestLinuxDiskPlainAndUnknown(t *testing.T) {
	plain := fakeEnv(
		map[string]string{"/proc/mounts": "/dev/sda2 / ext4 rw 0 0\n"},
		map[string][]string{"/sys/class/block/sda2": {}},
		nil,
	)
	if got := linuxDisk(plain); got != "no" {
		t.Errorf("duz bolum: %s", got)
	}
	over := fakeEnv(map[string]string{"/proc/mounts": "overlay / overlay rw 0 0\n"}, nil, nil)
	if got := linuxDisk(over); got != "unknown" {
		t.Errorf("konteyner: %s", got)
	}
	none := fakeEnv(nil, nil, nil)
	if got := linuxDisk(none); got != "unknown" {
		t.Errorf("mounts okunamadi: %s", got)
	}
}

func TestNftIptablesRules(t *testing.T) {
	empty := "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy accept;\n\t}\n}\n"
	if nftHasRules(empty) {
		t.Error("bos tablo kural sayilmamali")
	}
	full := "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t\ttcp dport 22 accept\n\t}\n}\n"
	if !nftHasRules(full) {
		t.Error("kural var")
	}
	if iptablesHasRules("-P INPUT ACCEPT\n-P FORWARD ACCEPT\n-P OUTPUT ACCEPT\n") {
		t.Error("kuralsiz iptables")
	}
	if !iptablesHasRules("-P INPUT DROP\n") || !iptablesHasRules("-P INPUT ACCEPT\n-A INPUT -p tcp --dport 22 -j ACCEPT\n") {
		t.Error("iptables kurali")
	}
}

func TestLinuxFirewallServices(t *testing.T) {
	e := fakeEnv(nil, nil, nil)
	e.run = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && len(args) == 2 && args[1] == "ufw" {
			return "active\n", nil
		}
		return "inactive\n", errors.New("exit 3")
	}
	if got := linuxFirewall(context.Background(), e); got != "yes" {
		t.Errorf("ufw aktif: %s", got)
	}
	// Hicbir sey okunamiyor -> unknown (yanlis "no" iddia edilmez)
	e.run = func(context.Context, string, ...string) (string, error) { return "", errors.New("izin yok") }
	if got := linuxFirewall(context.Background(), e); got != "unknown" {
		t.Errorf("okunamadi: %s", got)
	}
	// Kurallar okundu ama bos -> no
	e.run = func(_ context.Context, name string, _ ...string) (string, error) {
		if name == "nft" {
			return "", nil
		}
		return "", errors.New("yok")
	}
	if got := linuxFirewall(context.Background(), e); got != "no" {
		t.Errorf("kural yok: %s", got)
	}
}

func TestCollectPostureMacAndUnknown(t *testing.T) {
	e := postureEnv{goos: "darwin", version: "0.2.18", isService: true,
		run: func(_ context.Context, name string, args ...string) (string, error) {
			switch name {
			case "sw_vers":
				if args[0] == "-productVersion" {
					return "14.5\n", nil
				}
				return "23F79\n", nil
			case "fdesetup":
				return "FileVault is On.\n", nil
			}
			return "", errors.New("yok")
		}}
	r := collectPosture(context.Background(), e)
	if r.OSName != "macos" || r.OSVersion != "14.5" || r.OSBuild != "23F79" || r.DiskEncrypted != "yes" ||
		r.FirewallOn != "unknown" || r.ScreenLock != "unknown" || r.ClientVersion != "0.2.18" || !r.IsService {
		t.Errorf("rapor: %+v", r)
	}
	if r.Type != protocol.TypePostureReport {
		t.Errorf("tip: %s", r.Type)
	}
}

func TestLimitedBuffer(t *testing.T) {
	var b limitedBuffer
	big := strings.Repeat("x", postureOutLimit+5000)
	if n, _ := b.Write([]byte(big)); n != len(big) {
		t.Errorf("Write n=%d", n)
	}
	if len(b.String()) != postureOutLimit {
		t.Errorf("sinir asildi: %d", len(b.String()))
	}
}
