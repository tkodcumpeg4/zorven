//go:build !windows

package main

// systemPrefersLight: Windows disinda acilis rengi koyu tema (arayuz sonra kendi uygular).
func systemPrefersLight() bool { return false }
