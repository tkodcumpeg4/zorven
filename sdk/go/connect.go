package zorven

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

var urlLine = regexp.MustCompile(`^zorven-url:\s*(\S+)`)

// ConnectOptions Node/Python SDK'larindaki connect() ile ayni kapsamdadir:
// "zorven" CLI'sini surer ve yerel bir hedefi tunelle.
type ConnectOptions struct {
	Token   string // istemci token'i (zrv_live_...), zorunlu
	Port    int    // yerel port; Port veya Target zorunlu
	Target  string // tam hedef, ornek "http://localhost:8080"
	Server  string // sunucu adresi (varsayilan CLI ayari)
	Bin     string // CLI yolu (varsayilan "zorven")
	Timeout time.Duration
}

// TunnelSession canli bir CLI tunel oturumudur.
type TunnelSession struct {
	URL  string // ilk herkese acik URL
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

// Close tuneli kapatir (CLI surecini sonlandirir).
func (t *TunnelSession) Close() error {
	var err error
	t.once.Do(func() {
		if t.cmd.Process != nil {
			err = t.cmd.Process.Kill()
		}
		<-t.done
	})
	return err
}

// Wait CLI sureci bitene kadar bekler.
func (t *TunnelSession) Wait() { <-t.done }

// Connect "zorven <hedef> --token ... --print-url" calistirir ve
// "zorven-url: https://..." satirini bekler. ctx iptal edilirse surec oldurulur.
func Connect(ctx context.Context, o ConnectOptions) (*TunnelSession, error) {
	if o.Token == "" {
		return nil, errors.New("zorven: token zorunlu")
	}
	tgt := o.Target
	if tgt == "" && o.Port > 0 {
		tgt = fmt.Sprint(o.Port)
	}
	if tgt == "" {
		return nil, errors.New("zorven: port veya target zorunlu")
	}
	bin := o.Bin
	if bin == "" {
		bin = "zorven"
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	args := []string{tgt, "--token", o.Token, "--print-url", "--no-auto-update"}
	if o.Server != "" {
		args = append(args, "--server", o.Server)
	}
	cmd := exec.Command(bin, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("zorven: CLI baslatilamadi (%s): %w", bin, err)
	}
	t := &TunnelSession{cmd: cmd, done: make(chan struct{})}
	first := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sent := false
		for sc.Scan() {
			if m := urlLine.FindStringSubmatch(sc.Text()); m != nil {
				if !sent {
					sent = true
					first <- m[1]
				}
			}
		}
	}()
	go func() {
		cmd.Wait()
		pw.Close()
		close(t.done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case u := <-first:
		t.URL = u
		return t, nil
	case <-t.done:
		return nil, errors.New("zorven: CLI URL vermeden cikti")
	case <-timer.C:
		t.Close()
		return nil, errors.New("zorven: baglanti zaman asimina ugradi")
	case <-ctx.Done():
		t.Close()
		return nil, ctx.Err()
	}
}
