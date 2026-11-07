//go:build !windows

package wgnet

import "os/exec"

// hideConsole, Windows disinda bir sey yapmaz (bkz. hidecmd_windows.go).
func hideConsole(c *exec.Cmd) *exec.Cmd { return c }
