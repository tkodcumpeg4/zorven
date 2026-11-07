package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// maskSecret, token'i gosterim icin kisaltir (asla tam yazilmaz).
func maskSecret(t string) string {
	if len(t) > 16 {
		return t[:12] + "..." + t[len(t)-4:]
	}
	if t == "" {
		return ""
	}
	return strings.Repeat("*", len(t))
}

type cfgEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"` // flag | env | user | system | default | none
}

type cfgView struct {
	UserFile   string     `json:"user_file"`
	SystemFile string     `json:"system_file"`
	Active     string     `json:"active_file"` // user | system
	Entries    []cfgEntry `json:"values"`
	raw        config.Config
}

// effectiveConfig, her anahtarin ETKIN degerini ve kaynagini hesaplar:
// ortam degiskeni > ayar dosyasi (config.Load'un sectigi: kullanici, token
// yoksa sistem) > varsayilan.
func effectiveConfig() cfgView {
	cfg, _ := config.Load()
	up, _ := config.Path()
	sp := config.SystemPath()
	v := cfgView{UserFile: up, SystemFile: sp, raw: cfg, Active: "user"}
	if up != "" {
		if uc, err := config.LoadFile(up); err == nil && uc.Token == "" {
			if sc, err := config.LoadFile(sp); err == nil && sc.Token != "" {
				v.Active = "system"
			}
		}
	}
	file := v.Active
	fromFile := func(isDefault bool) string {
		if isDefault {
			return "default"
		}
		return file
	}
	add := func(k, val, src string) { v.Entries = append(v.Entries, cfgEntry{k, val, src}) }

	if e := strings.TrimSpace(os.Getenv("ZORVEN_SERVER")); e != "" {
		add("server", e, "env")
	} else {
		add("server", resolveServerAddr("", cfg), fromFile(cfg.ServerAddr == "" || cfg.ServerAddr == config.DefaultConfig().ServerAddr))
	}
	switch {
	case strings.TrimSpace(os.Getenv("ZORVEN_TOKEN")) != "":
		add("token", maskSecret(strings.TrimSpace(os.Getenv("ZORVEN_TOKEN"))), "env")
	case cfg.Token != "":
		add("token", maskSecret(cfg.Token), file)
	default:
		add("token", "", "none")
	}
	add("insecure", fmt.Sprint(cfg.Insecure), fromFile(!cfg.Insecure))
	add("ca_cert", cfg.CACertPath, fromFile(cfg.CACertPath == ""))
	add("enable_terminal", fmt.Sprint(cfg.EnableTerminal), fromFile(!cfg.EnableTerminal))
	add("enable_screen", fmt.Sprint(cfg.EnableScreen), fromFile(!cfg.EnableScreen))
	add("enable_deploy", fmt.Sprint(cfg.EnableDeploy), fromFile(!cfg.EnableDeploy))
	if e := strings.TrimSpace(os.Getenv("ZORVEN_LANG")); e != "" {
		add("language", e, "env")
	} else {
		add("language", cfg.Language, fromFile(cfg.Language == ""))
	}
	if config.LoadAPIToken() != "" {
		add("api_token", "(saved)", "user")
	} else if os.Getenv("ZORVEN_API_TOKEN") != "" {
		add("api_token", "(set)", "env")
	} else {
		add("api_token", "", "none")
	}
	return v
}

func (v cfgView) lookup(key string) (cfgEntry, bool) {
	for _, e := range v.Entries {
		if e.Key == key {
			return e, true
		}
	}
	return cfgEntry{}, false
}

// targetFile, yazilacak dosyanin yolunu verir (--system: sistem dosyasi).
func targetFile(system bool) (string, error) {
	if system {
		return config.SystemPath(), nil
	}
	return config.Path()
}

// validateConfigValue, anahtara gore degeri onceden denetler.
func validateConfigValue(key, value string) error {
	switch key {
	case "token":
		if !isAgentToken(strings.TrimSpace(value)) {
			return errors.New(i18n.T("main.err.bad_token_format"))
		}
	case "server":
		if v := strings.TrimSpace(value); v == "" || strings.ContainsAny(v, " /") {
			return errors.New(i18n.T("set.err.serverfmt"))
		}
	case "language":
		if value != "" && i18n.Normalize(value) == "" {
			return errors.New(i18n.T("v3.cfg.err.lang", value))
		}
	case "ca_cert":
		if value != "" {
			if st, err := os.Stat(value); err != nil || st.IsDir() {
				return errors.New(i18n.T("set.err.nofile", value))
			}
		}
	}
	return nil
}

// mutateConfig, YALNIZ hedef dosyayi okuyup degistirip yazar; sonra calisan
// ajana yansitmayi dener. Yalniz key'in canli uygulanabildigi durumlarda
// "canli uygulandi" denir (liveApplicable); digerlerinde "yeniden baslatinca
// uygulanir" notu verilir (ornegin enable_deploy: Apps izni calisan istemcide
// canli degismez).
func mutateConfig(system bool, key string, fn func(*config.Config) error, liveMsg bool) error {
	p, err := targetFile(system)
	if err != nil {
		return err
	}
	cfg, err := config.LoadFile(p)
	if err != nil {
		return err
	}
	if err := fn(&cfg); err != nil {
		return err
	}
	if err := config.SaveFile(p, cfg); err != nil {
		return err
	}
	fmt.Fprintln(outW, i18n.T("v3.cfg.saved", p))
	if liveMsg {
		res, rerr := applyConfigLive(system, key)
		switch res {
		case liveApplied:
			fmt.Fprintln(outW, i18n.T("v3.cfg.live.ok"))
		case liveNeedRestart:
			fmt.Fprintln(outW, i18n.T("v3.cfg.live.restart"))
		case liveLater:
			fmt.Fprintln(outW, i18n.T("v3.cfg.live.later"))
		case liveFailed:
			fmt.Fprintln(os.Stderr, i18n.T("v3.cfg.live.fail", rerr))
		}
	}
	return nil
}

func knownKey(k string) bool {
	for _, x := range config.Keys {
		if x == k {
			return true
		}
	}
	return false
}

func newConfigCmd(authtokenCmd *cobra.Command) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: i18n.T("main.cmd.config"),
	}

	var showJSON bool
	showCmd := &cobra.Command{
		Use:   "show",
		Short: i18n.T("main.cmd.config_show"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			v := effectiveConfig()
			if showJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(v)
			}
			row := func(label, value string) { fmt.Println(padLabel(label, 18) + value) }
			row(i18n.T("main.lbl.file"), v.UserFile)
			row(i18n.T("v3.cfg.sysfile"), v.SystemFile)
			row(i18n.T("v3.cfg.active"), v.Active)
			fmt.Println()
			for _, e := range v.Entries {
				val := e.Value
				if val == "" {
					val = "-"
				}
				fmt.Println(padLabel(e.Key, 18) + padLabel(val, 34) + "(" + e.Source + ")")
			}
			return nil
		},
	}
	showCmd.Flags().BoolVar(&showJSON, "json", false, i18n.T("v3.flag.json"))

	getCmd := &cobra.Command{
		Use:   "get <key>",
		Short: i18n.T("v3.cfg.get.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			if !knownKey(key) {
				return errors.New(i18n.T("v3.cfg.err.key", key, strings.Join(config.Keys, ", ")))
			}
			cfg, _ := config.Load()
			val, _ := cfg.Get(key)
			switch key {
			case "server":
				val = resolveServerAddr("", cfg)
			case "token":
				if e := strings.TrimSpace(os.Getenv("ZORVEN_TOKEN")); e != "" {
					val = e
				}
			}
			fmt.Println(val)
			return nil
		},
	}

	var setSystem bool
	setCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: i18n.T("v3.cfg.set.short"),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], strings.TrimSpace(args[1])
			if !knownKey(key) {
				return errors.New(i18n.T("v3.cfg.err.key", key, strings.Join(config.Keys, ", ")))
			}
			if err := validateConfigValue(key, value); err != nil {
				return err
			}
			if key == "insecure" && (strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "on")) {
				warnPlaintext(true)
			}
			return mutateConfig(setSystem, key, func(c *config.Config) error { return c.Set(key, value) }, true)
		},
	}
	setCmd.Flags().BoolVar(&setSystem, "system", false, i18n.T("main.flag.system"))

	var unsetSystem bool
	unsetCmd := &cobra.Command{
		Use:   "unset <key>",
		Short: i18n.T("v3.cfg.unset.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !knownKey(args[0]) {
				return errors.New(i18n.T("v3.cfg.err.key", args[0], strings.Join(config.Keys, ", ")))
			}
			return mutateConfig(unsetSystem, args[0], func(c *config.Config) error { return c.Unset(args[0]) }, true)
		},
	}
	unsetCmd.Flags().BoolVar(&unsetSystem, "system", false, i18n.T("main.flag.system"))

	// Eski komutlar (geriye uyumluluk).
	var isSystemServer bool
	configSetServerCmd := &cobra.Command{
		Use:   "set-server <server_addr>",
		Short: i18n.T("main.cmd.set_server"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := strings.TrimSpace(args[0])
			if err := validateConfigValue("server", s); err != nil {
				return err
			}
			if isSystemServer {
				if err := config.SetServerSystem(s); err != nil {
					return err
				}
				fmt.Print(i18n.T("main.msg.sys_server", config.SystemPath(), s))
				return nil
			}
			if err := config.SetServer(s); err != nil {
				return err
			}
			fmt.Print(i18n.T("main.msg.def_server", s))
			return nil
		},
	}
	configSetServerCmd.Flags().BoolVar(&isSystemServer, "system", false, i18n.T("main.flag.system"))

	var isSystemToken bool
	configSetTokenCmd := &cobra.Command{
		Use:   "set-token <token>",
		Short: i18n.T("main.cmd.set_token"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t := strings.TrimSpace(args[0])
			if !isAgentToken(t) {
				return errors.New(i18n.T("main.err.bad_token_format"))
			}
			if isSystemToken {
				if err := config.SetTokenSystem(t); err != nil {
					return err
				}
				fmt.Print(i18n.T("main.msg.sys_token", config.SystemPath()))
				return nil
			}
			return authtokenCmd.RunE(cmd, args)
		},
	}
	configSetTokenCmd.Flags().BoolVar(&isSystemToken, "system", false, i18n.T("main.flag.system"))

	configCmd.AddCommand(showCmd, getCmd, setCmd, unsetCmd, configSetServerCmd, configSetTokenCmd)
	return configCmd
}
