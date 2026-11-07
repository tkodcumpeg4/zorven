//go:build !windows

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// watchResize, terminal boyutu degistiginde (SIGWINCH) ch'ye sinyal yollar.
// stop kapaninca izleme biter.
func watchResize(ch chan<- struct{}, stop <-chan struct{}) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-sig:
				select {
				case ch <- struct{}{}:
				default:
				}
			case <-stop:
				return
			}
		}
	}()
}
