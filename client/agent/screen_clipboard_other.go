//go:build !windows

package agent

// Pano Windows disinda desteklenmiyor: Linux X11 secim protokolu (CLIPBOARD /
// INCR / hedef donusumu) saf Go'da buyuk bir is; macOS icin cgo gerekir. Bu
// platformlarda FeatureScreenClipboard ilan edilmez, panel pano arayuzunu gizler.

func clipboardSupported() bool { return false }

func newClipboardBackend() clipboardBackend { return nil }
