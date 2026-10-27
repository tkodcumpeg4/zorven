//go:build linux

package main

import "github.com/kardianos/service"

// W3 / Mod B: systemd unit'i TUN icin CAP_NET_ADMIN'i ACIKCA tasir (servis root
// calissa da, ileride User= ile dusurulurse ayricalik korunur). Sablon, kardianos/service
// v1.3.0'in varsayilan systemd sablonunun AmbientCapabilities satiri eklenmis halidir.
// Mevcut kurulumlar `zorven service uninstall && zorven service install` (veya
// `zorven up`) ile yeni unit'i alir.
const systemdUnitWithNetAdmin = `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
{{range Dependencies}}{{.}}
{{end}}
[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{Path | cmdEscape}}{{range Arguments}} {{. | cmd}}{{end}}
{{if ChRoot}}RootDirectory={{ChRoot | cmd}}
{{end}}{{if WorkingDirectory}}WorkingDirectory={{WorkingDirectory | cmdEscape}}
{{end}}{{if UserName}}User={{UserName}}
{{end}}{{if ReloadSignal}}ExecReload=/bin/kill -{{ReloadSignal}} "$MAINPID"
{{end}}{{if PIDFile}}PIDFile={{PIDFile | cmd}}
{{end}}{{if OutputFileSupport}}StandardOutput=file:{{LogDirectory}}/{{Name}}.out
StandardError=file:{{LogDirectory}}/{{Name}}.err
{{end}}{{if LimitNOFILE}}LimitNOFILE={{LimitNOFILE}}
{{end}}{{if Restart}}Restart={{Restart}}
{{end}}{{if SuccessExitStatus}}SuccessExitStatus={{SuccessExitStatus}}
{{end}}AmbientCapabilities=CAP_NET_ADMIN
RestartSec=120
EnvironmentFile=-/etc/sysconfig/{{Name}}

{{range EnvVars}}{{.}}
{{end}}[Install]
WantedBy=multi-user.target
`

func platformServiceOptions() service.KeyValue {
	return service.KeyValue{"SystemdScript": systemdUnitWithNetAdmin}
}
