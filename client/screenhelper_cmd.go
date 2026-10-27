package main

import (
	"github.com/spf13/cobra"

	"github.com/tkodcumpeg4/zorven/client/agent"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// newScreenHelperCmd, gizli "zorven screen-helper" komutu: Windows servisi
// (oturum 0) ekran paylasimi icin bunu kullanici oturumunda baslatir. Kullaniciya
// donuk bir komut degildir; gecerli --pipe/--token olmadan calismayi reddeder.
func newScreenHelperCmd() *cobra.Command {
	var pipe, token string
	c := &cobra.Command{
		Use:           "screen-helper",
		Short:         i18n.T("scr.cmd.short"),
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return agent.RunScreenHelper(pipe, token, version)
		},
	}
	c.Flags().StringVar(&pipe, "pipe", "", "")
	c.Flags().StringVar(&token, "token", "", "")
	return c
}
