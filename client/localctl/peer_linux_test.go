//go:build linux

package localctl

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Ayni uid'li istemci checkPeer'den gecer.
func TestCheckPeerSameUID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		done <- checkPeer(c, Endpoint{Path: p})
	}()
	c, err := net.Dial("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := <-done; err != nil {
		t.Errorf("ayni uid reddedildi (euid %d): %v", os.Geteuid(), err)
	}
}
