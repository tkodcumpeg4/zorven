//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestSystemdUnitRestartSettings(t *testing.T) {
	u := systemdUnitWithNetAdmin
	unit := u[:strings.Index(u, "[Service]")]
	if !strings.Contains(unit, "StartLimitIntervalSec=") || !strings.Contains(unit, "StartLimitBurst=") {
		t.Fatal("StartLimit* [Unit] bolumunde olmali")
	}
	svc := u[strings.Index(u, "[Service]"):]
	if strings.Contains(svc, "StartLimit") {
		t.Fatal("StartLimit* [Service] bolumunde olmamali")
	}
	if !strings.Contains(svc, "RestartSec=5\n") || strings.Contains(svc, "RestartSec=120") {
		t.Fatal("RestartSec=5 olmali")
	}
	if !strings.Contains(svc, "RestartPreventExitStatus=78") {
		t.Fatal("token yok cikis kodu yeniden baslatmayi engellemeli")
	}
}
