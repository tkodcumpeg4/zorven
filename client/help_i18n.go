package main

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// cobraUsageTemplate, cobra v1.10 varsayilan sablonunun kopyasidir; basliklar
// localizeCobra'da etkin dile cevrilir (cobra bunlari sabit Ingilizce yazar).
const cobraUsageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

// localizeCobra, yardim ciktisindaki cobra basliklarini, help komutunu ve
// -h/--help aciklamasini etkin dile cevirir. Tum alt komutlar eklendikten
// sonra, Execute'tan once cagrilir.
func localizeCobra(root *cobra.Command) {
	r := strings.NewReplacer(
		"Usage:", i18n.T("help.usage"),
		"Aliases:", i18n.T("help.aliases"),
		"Examples:", i18n.T("help.examples"),
		"Available Commands:", i18n.T("help.commands"),
		"Additional Commands:", i18n.T("help.morecommands"),
		"Global Flags:", i18n.T("help.globalflags"),
		"Flags:", i18n.T("help.flags"),
		"Additional help topics:", i18n.T("help.topics"),
		"[command]", i18n.T("help.command"),
		`Use "{{.CommandPath}} [command] --help" for more information about a command.`,
		i18n.T("help.more", "{{.CommandPath}}"),
	)
	cobra.AddTemplateFunc("zvFlags", func(s string) string {
		return strings.Replace(s, "[flags]", i18n.T("help.flagsword"), 1)
	})
	tmpl := strings.Replace(cobraUsageTemplate, "{{.UseLine}}", "{{zvFlags .UseLine}}", 1)
	root.SetUsageTemplate(r.Replace(tmpl))
	if root.Version != "" && root.Flags().Lookup("version") == nil {
		root.Flags().BoolP("version", "v", false, i18n.T("help.version"))
	}

	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		switch c.Name() {
		case "help":
			c.Short = i18n.T("help.cmd")
			c.Long = i18n.T("help.cmd.long")
		case "completion":
			c.Short = i18n.T("help.completion")
		}
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Flags().Lookup("help") == nil {
			c.Flags().BoolP("help", "h", false, i18n.T("help.flag", c.Name()))
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
