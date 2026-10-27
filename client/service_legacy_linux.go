//go:build linux

package main

import (
	"os"
	"os/exec"
	"strings"
)

// systemdUnitPath, kardianos/service'in sistem servisi icin yazdigi birim.
const systemdUnitPath = "/etc/systemd/system/zorven.service"

// detectLegacyUnit, kurulu birim eski bicimdeyse (service run yok) bilgisini doner.
func detectLegacyUnit() (legacyUnit, bool) {
	b, err := os.ReadFile(systemdUnitPath)
	if err != nil {
		return legacyUnit{}, false
	}
	return parseLegacyUnit(string(b))
}

// autostartEnabled, birimin acilista baslamak uzere etkin olup olmadigini soyler.
func autostartEnabled() bool {
	out, _ := exec.Command("systemctl", "is-enabled", "zorven").Output()
	return strings.TrimSpace(string(out)) == "enabled"
}
