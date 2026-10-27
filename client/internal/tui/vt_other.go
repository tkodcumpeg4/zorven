//go:build !windows

package tui

import "os"

func enableVT(*os.File) (func(), error) { return func() {}, nil }
