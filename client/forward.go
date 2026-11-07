package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

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
	var fwServer, listen, caCert string
	var port int
	var udp, skipVerify, insecureOld bool

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
			srv := resolveServerAddr(fwServer, cfg)
			if insecureOld {
				fmt.Fprintln(os.Stderr, i18n.T("v3.fw.insecure_deprecated"))
				skipVerify = true
			}
			// Ayar dosyasindaki insecure (yerel gelistirme sunucusu, kendi imzali
			// sertifika) forward icin de sertifika atlamak anlamina gelir.
			if cfg.Insecure && !skipVerify {
				fmt.Fprintln(os.Stderr, i18n.T("v3.fw.cfg_insecure"))
				skipVerify = true
			}
			if caCert == "" {
				caCert = cfg.CACertPath
			}
			tc, err := forwardTLSConfig(host, caCert, skipVerify)
			if err != nil {
				return err
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
			laddr = ln.Addr().String() // port 0 ise gercek port

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
				go func() {
					if ferr := forwardOne(cmd.Context(), c, srv, tc); ferr != nil {
						fmt.Fprintln(os.Stderr, i18n.T("v3.fw.err.dial", srv, ferr))
					}
				}()
			}
		},
	}
	cmd.Flags().StringVar(&fwServer, "server", "", i18n.T("conn2.fw.flag.server"))
	cmd.Flags().IntVar(&port, "port", 0, i18n.T("conn2.fw.flag.port"))
	cmd.Flags().StringVar(&listen, "listen", "", i18n.T("conn2.fw.flag.listen"))
	cmd.Flags().BoolVar(&udp, "udp", false, i18n.T("conn2.fw.flag.udp"))
	_ = cmd.Flags().MarkHidden("udp") // henuz desteklenmiyor
	cmd.Flags().BoolVar(&skipVerify, "skip-verify", false, i18n.T("v3.fw.flag.skipverify"))
	cmd.Flags().StringVar(&caCert, "ca-cert", "", i18n.T("net.flag.cacert"))
	cmd.Flags().BoolVar(&insecureOld, "insecure", false, i18n.T("conn2.fw.flag.insecure"))
	_ = cmd.Flags().MarkHidden("insecure") // --skip-verify takma adi (eski)
	return cmd
}

// forwardTLSConfig, SNI=host ile istemci TLS ayari kurar.
func forwardTLSConfig(host, caCert string, skipVerify bool) (*tls.Config, error) {
	tc := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if skipVerify {
		tc.InsecureSkipVerify = true //nolint:gosec // yalniz --skip-verify ile
		return tc, nil
	}
	if caCert != "" {
		pem, err := os.ReadFile(caCert)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New(i18n.T("v3.fw.err.badca", caCert))
		}
		tc.RootCAs = pool
	}
	return tc, nil
}

type closeWriter interface{ CloseWrite() error }

// bridge, iki baglantiyi cift yonlu kopyalar. Bir yon EOF olunca karsi
// tarafin yazma ucu kapatilir (yari-kapama); iki kopya da bitince doner.
func bridge(a, b net.Conn) {
	var wg sync.WaitGroup
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()
}

// forwardOne, tek bir yerel baglantiyi sunucuya SNI=host ile TLS uzerinden
// koprüler. Baglanti kurulamazsa hata doner (cagiran yazdirir).
func forwardOne(ctx context.Context, local net.Conn, serverAddr string, tc *tls.Config) error {
	defer local.Close()
	if ctx == nil {
		ctx = context.Background()
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}, Config: tc}
	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tconn, err := d.DialContext(hctx, "tcp", serverAddr)
	if err != nil {
		return err
	}
	defer tconn.Close()
	bridge(local, tconn)
	return nil
}
