//go:build !linux

package agent

// platSessionBlocker: Linux disinda onceden engel yok.
func platSessionBlocker() error { return nil }
