//go:build !windows

package agent

// verifyAuthenticode yalniz Windows'ta anlamlidir.
func verifyAuthenticode(string) error { return nil }
