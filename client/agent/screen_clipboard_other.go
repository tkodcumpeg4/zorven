//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"runtime"
)

// Windows disinda pano alt surec araclariyla yapilir (screen_clipboard_cmd.go):
// macOS pbcopy/pbpaste, Wayland wl-clipboard, X11 xclip/xsel. Arac ya da ekran
// ortami (DISPLAY/WAYLAND_DISPLAY) yoksa FeatureScreenClipboard ilan edilmez.
// Gorsel panosu bu platformlarda YOK.

func platClipTool() *clipTool { return detectClipTool(runtime.GOOS, os.Getenv, exec.LookPath) }

func clipboardSupported() bool { return platClipTool() != nil }

func clipboardImageSupported() bool { return false }

func newClipboardBackend() clipboardBackend {
	t := platClipTool()
	if t == nil {
		return nil
	}
	return newCmdClipboard(execRunner{}, *t)
}
