//go:build !linux

package main

// Eski systemd birimi yalniz Linux'ta vardir.
func detectLegacyUnit() (legacyUnit, bool) { return legacyUnit{}, false }

func autostartEnabled() bool { return true }
