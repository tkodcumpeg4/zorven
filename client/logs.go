package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/localctl"
)

// tailLines, ReaderAt'in son n satirini doner (en fazla son 1 MiB okunur).
func tailLines(r io.ReaderAt, size int64, n int) ([]string, int64, error) {
	if n <= 0 || size == 0 {
		return nil, size, nil
	}
	const maxRead = 1 << 20
	start := size - maxRead
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := r.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, size, err
	}
	if start > 0 {
		// Ilk satir yarim olabilir: at.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	text := strings.TrimRight(string(buf), "\r\n")
	if text == "" {
		return nil, size, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, size, nil
}

func agentLogPath() string { return filepath.Join(localctl.LogDir(), "agent.log") }

func newLogsCmd() *cobra.Command {
	var follow bool
	var n int
	cmd := &cobra.Command{
		Use: "logs", SilenceErrors: true, SilenceUsage: true,
		Short: i18n.T("v3.logs.short"),
		Long:  i18n.T("v3.logs.long"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := agentLogPath()
			f, err := os.Open(p)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(os.Stderr, i18n.T("v3.logs.none", p))
					if runtime.GOOS == "linux" {
						fmt.Fprintln(os.Stderr, i18n.T("v3.logs.journal"))
					}
					return &exitError{1}
				}
				if errors.Is(err, os.ErrPermission) {
					return errors.New(i18n.T("v3.logs.perm", p))
				}
				return err
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil {
				return err
			}
			lines, off, err := tailLines(f, fi.Size(), n)
			if err != nil {
				return err
			}
			for _, l := range lines {
				fmt.Println(l)
			}
			if !follow {
				if runtime.GOOS == "linux" {
					fmt.Fprintln(os.Stderr, i18n.T("v3.logs.journal"))
				}
				return nil
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return followFile(ctx, p, f, off)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, i18n.T("v3.logs.flag.follow"))
	cmd.Flags().IntVarP(&n, "lines", "n", 50, i18n.T("v3.logs.flag.lines"))
	return cmd
}

// followFile, dosyaya eklenenleri yazar; dosya kisalirsa (donme/truncate)
// bastan okur.
func followFile(ctx context.Context, path string, f *os.File, off int64) error {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	buf := make([]byte, 32<<10)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.Size() < off {
			// donduruldu: yeni dosyayi bastan oku
			nf, oerr := os.Open(path)
			if oerr != nil {
				continue
			}
			f.Close()
			f, off = nf, 0
		}
		for {
			nr, rerr := f.ReadAt(buf, off)
			if nr > 0 {
				_, _ = os.Stdout.Write(buf[:nr])
				off += int64(nr)
			}
			if rerr != nil || nr == 0 {
				break
			}
		}
	}
}
