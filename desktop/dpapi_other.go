//go:build !windows

package main

// platformTokenProtector: Windows disinda token korumasi yoktur (acik metin,
// 0600 dosya izniyle).
func platformTokenProtector() tokenProtect { return nil }
