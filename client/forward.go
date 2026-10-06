package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/config"
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
		Short: "SNI-modu bir TCP tuneline yerel port acar (ziyaretci tarafi)",
		Long: "SNI-modu bir ham TCP tunelini yerel bir porta baglar. Ornek:\n" +
			"  zorven forward mc.ornek.com --port 25565\n" +
			"Ardindan uygulamanizi 127.0.0.1:25565 adresine baglayin.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := strings.TrimSpace(args[0])
			if host == "" {
				return fmt.Errorf("host gerekli (or. mc.ornek.com)")
			}
			if udp {
				return fmt.Errorf("UDP forward henuz desteklenmiyor; UDP icin rezerve-port modunu kullanin")
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
				return fmt.Errorf("yerel port acilamadi (%s): %w", laddr, err)
			}
			defer ln.Close()

			fmt.Printf("Zorven forward etkin:\n")
			fmt.Printf("  Yerel  : %s\n", laddr)
			fmt.Printf("  Tunel  : %s (TLS-SNI -> %s)\n", host, srv)
			fmt.Printf("  Uygulamanizi %s adresine baglayin. Durdurmak icin Ctrl+C.\n\n", laddr)

			for {
				c, aerr := ln.Accept()
				if aerr != nil {
					return aerr
				}
				go forwardOne(c, srv, host, insecureFlag)
			}
		},
	}
	cmd.Flags().StringVar(&fwServer, "server", "", "tunel sunucusu (host:port, varsayilan zorven.app:443)")
	cmd.Flags().IntVar(&port, "port", 25565, "dinlenecek yerel port")
	cmd.Flags().StringVar(&listen, "listen", "", "dinlenecek yerel adres (or. 0.0.0.0:25565); bos ise 127.0.0.1:<port>")
	cmd.Flags().BoolVar(&udp, "udp", false, "UDP (henuz desteklenmiyor)")
	cmd.Flags().BoolVar(&insecureFlag, "insecure", false, "sunucu TLS sertifikasini dogrulama (yalnizca gelistirme)")
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
