package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT, Windows konsolunda ANSI kacis dizilerinin islenmesini acar
// (eski conhost varsayilan olarak kapali tutar). Girdi tarafindaki VT kipini
// term.MakeRaw zaten acar.
func enableVT(out *os.File) (func(), error) {
	h := windows.Handle(out.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {}, nil
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return func() {}, nil
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }, nil
}
