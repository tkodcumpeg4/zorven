package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// newLoginCmd: API token'i (zrv_api_...) 0600 izinli bir dosyaya kaydeder; sonraki
// connect/mcp/net/apps/access komutlari ZORVEN_API_TOKEN gerektirmez.
func newLoginCmd() *cobra.Command {
	var tokFile string
	var stdin bool
	cmd := &cobra.Command{
		Use:     "login",
		Short:   i18n.T("v3.login.short"),
		Long:    i18n.T("v3.login.long"),
		Example: "  zorven login --api-token-file ./token.txt\n  echo $TOKEN | zorven login --api-token-stdin\n  zorven login",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var tok string
			var err error
			switch {
			case tokFile != "":
				tok, err = readTokenFromFile(tokFile)
			case stdin:
				tok, err = readTokenFromFile("-")
			default:
				if !stdinIsTTY() {
					return errors.New(i18n.T("v3.login.err.notty"))
				}
				tok, err = readSecret(i18n.T("v3.login.prompt"))
			}
			if err != nil {
				return err
			}
			tok = strings.TrimSpace(tok)
			if tok == "" {
				return errors.New(i18n.T("net.err.token"))
			}
			if isAgentToken(tok) {
				return errors.New(i18n.T("net.err.agenttoken"))
			}
			if !strings.HasPrefix(tok, "zrv_api_") {
				return errors.New(i18n.T("v3.login.err.format"))
			}
			if err := config.SaveAPIToken(tok); err != nil {
				return fmt.Errorf("%s: %w", i18n.T("main.err.token_save"), err)
			}
			p, _ := config.APITokenPath()
			fmt.Println(i18n.T("v3.login.saved", p))
			return nil
		},
	}
	cmd.Flags().StringVar(&tokFile, "api-token-file", "", i18n.T("v3.flag.apitokenfile"))
	cmd.Flags().BoolVar(&stdin, "api-token-stdin", false, i18n.T("v3.flag.apitokenstdin"))
	return cmd
}

// newLogoutCmd: kayitli cihaz token'ini (kullanici ayar dosyasi) ve API token'i siler.
func newLogoutCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: i18n.T("v3.logout.short"),
		Long:  i18n.T("v3.logout.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			cfg, lerr := config.LoadFile(p)
			if lerr != nil {
				return lerr
			}
			hasDev := cfg.Token != ""
			hasAPI := config.LoadAPIToken() != ""
			if !hasDev && !hasAPI {
				fmt.Println(i18n.T("v3.logout.nothing"))
				return nil
			}
			if !yes {
				if !stdinIsTTY() {
					return errors.New(i18n.T("v3.logout.err.confirm"))
				}
				if !confirm(i18n.T("v3.logout.confirm")) {
					fmt.Println(i18n.T("set.cancelled"))
					return nil
				}
			}
			if hasDev {
				cfg.Token = ""
				if err := config.SaveFile(p, cfg); err != nil {
					return err
				}
				fmt.Println(i18n.T("v3.logout.device"))
			}
			if hasAPI {
				if _, err := config.DeleteAPIToken(); err != nil {
					return err
				}
				fmt.Println(i18n.T("v3.logout.api"))
			}
			// Sistem servisinin token'i ayri bir kimliktir; dokunulmaz, haber ver.
			if sc, err := config.LoadFile(config.SystemPath()); err == nil && sc.Token != "" {
				fmt.Fprintln(os.Stderr, i18n.T("v3.logout.sys", config.SystemPath()))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, i18n.T("v3.flag.yes"))
	return cmd
}
