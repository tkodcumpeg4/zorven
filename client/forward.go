package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// newForwardCmd, ZIYARETCI tarafi komutu: SNI-modu bir ham TCP tuneline yerel
// bir port acar. Her yerel baglantı icin sunucuya (zorven.app:443) SNI=<host>
// ile bir TLS baglantisi kurulur; sunucu bunu ilgili tunelin ajanina koprüler.
//
// Kullanim:  zorven forward mc.ornek.com --port 25565
// Sonra uygulamanizi (or. Minecraft) 127.0.0.1:25565 adresine baglarsiniz.
func newForwardCmd() *cobra.Command {
	var fwServer, listen string
	var port int
	var udp, insecureFlag bool

	cmd := &cobra.Command{
		Use:   "forward <host>",
		Short: i18n.T("conn2.fw.short"),
		Long:  i18n.T("conn2.fw.long"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := strings.TrimSpace(args[0])
			if host == "" {
				return errors.New(i18n.T("conn2.fw.err.host"))
			}
			if udp {
				return errors.New(i18n.T("conn2.fw.err.udp"))
			}
			cfg, _ := config.Load()
			srv := strings.TrimSpace(fwServer)
			if srv == "" {
				srv = strings.TrimSpace(cfg.ServerAddr)
			}
			if srv == "" || srv == "localhost:8443" {
				srv = "zorven.app:443"
			}
			laddr := listen
			if laddr == "" {
				laddr = fmt.Sprintf("127.0.0.1:%d", port)
			}
			ln, err := net.Listen("tcp", laddr)
			if err != nil {
				return fmt.Errorf("%s: %w", i18n.T("conn2.fw.err.listen", laddr), err)
			}
			defer ln.Close()

			fmt.Println(i18n.T("conn2.fw.active"))
			fmt.Println(i18n.T("conn2.fw.local", laddr))
			fmt.Println(i18n.T("conn2.fw.tunnel", host, srv))
			fmt.Println(i18n.T("conn2.fw.connect", laddr))
			fmt.Println()

			for {
				c, aerr := ln.Accept()
				if aerr != nil {
					return aerr
				}
				go forwardOne(c, srv, host, insecureFlag)
			}
		},
	}
	cmd.Flags().StringVar(&fwServer, "server", "", i18n.T("conn2.fw.flag.server"))
	cmd.Flags().IntVar(&port, "port", 25565, i18n.T("conn2.fw.flag.port"))
	cmd.Flags().StringVar(&listen, "listen", "", i18n.T("conn2.fw.flag.listen"))
	cmd.Flags().BoolVar(&udp, "udp", false, i18n.T("conn2.fw.flag.udp"))
	cmd.Flags().BoolVar(&insecureFlag, "insecure", false, i18n.T("conn2.fw.flag.insecure"))
	return cmd
}

// forwardOne, tek bir yerel baglantiyi sunucuya SNI=host ile TLS uzerinden koprüler.
func forwardOne(local net.Conn, serverAddr, sniHost string, insecure bool) {
	defer local.Close()
	tconn, err := tls.Dial("tcp", serverAddr, &tls.Config{
		ServerName:         sniHost,
		InsecureSkipVerify: insecure, //nolint:gosec // yalnizca --insecure ile
	})
	if err != nil {
		return
	}
	defer tconn.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(tconn, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, tconn); done <- struct{}{} }()
	<-done
}
