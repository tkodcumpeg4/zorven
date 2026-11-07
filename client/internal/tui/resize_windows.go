package tui

import (
	"os"
	"time"

	"golang.org/x/term"
)

// watchResize, Windows'ta SIGWINCH yoktur: konsol boyutu periyodik yoklanir ve
// degisince ch'ye sinyal yollanir. stop kapaninca izleme biter.
func watchResize(ch chan<- struct{}, stop <-chan struct{}) {
	go func() {
		w, h, _ := term.GetSize(int(os.Stdout.Fd()))
		t := time.NewTicker(400 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				nw, nh, err := term.GetSize(int(os.Stdout.Fd()))
				if err == nil && (nw != w || nh != h) {
					w, h = nw, nh
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
}
