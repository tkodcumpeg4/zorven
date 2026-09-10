//go:build !windows

package agent

import "github.com/tkodcumpeg4/zorven/shared/protocol"

// Windows disi platformlarda girdi enjeksiyonu HENUZ YOK.
//
// Gerekce: cross-platform fare/klavye enjeksiyonu (robotgo) cgo + C derleyici
// gerektirir ve projenin "tek statik binary" ozelligini bozar. Windows'ta saf
// syscall (user32.SendInput) ile cgo'suz cozuldu; Linux (uinput/XTest) ve macOS
// (CGEvent) sonraki bir asamada build tag'iyle eklenebilir.
//
// Bu platformlarda ekran GORUNTULEME calisir, KONTROL sessizce yok sayilir.
func applyInput(_ protocol.ScreenInput) {}

func keyToRune(_ string) rune { return 0 }
