//go:build !windows

package agent

import (
	"errors"

	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// Windows disinda ekran yardimcisi yoktur.

// RunScreenHelper, Windows disinda desteklenmez.
func RunScreenHelper(_, _, _ string) error {
	return errors.New(i18n.T("scr.err.unsupported"))
}

func screenUnsupportedErr() error { return errScreen2Unsupported }

func captureInit(string) {}
func captureShutdown()   {}
