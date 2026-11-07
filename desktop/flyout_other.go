//go:build !windows

package main

import (
	"context"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

func platWindowBounds() (winBounds, bool) { return winBounds{}, false }

func platSetBounds(ctx context.Context, _ winBounds) {
	wailsrt.WindowSetSize(ctx, 1040, 680)
	wailsrt.WindowCenter(ctx)
}

func platFocus() {}

func platPlaceFlyout(ctx context.Context, w, h int) {
	wailsrt.WindowSetSize(ctx, w, h)
	wailsrt.WindowSetAlwaysOnTop(ctx, true)
}
