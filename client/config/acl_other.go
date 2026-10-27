//go:build !windows

package config

// restrictFile, Unix'te 0600 izni yeterlidir (SaveFile chmod eder).
func restrictFile(string, bool) error { return nil }

func isSystemPath(path string) bool { return path == SystemPath() }
