// Package util provides shared string utility functions used across packages.
package util

// TruncateRunes truncates s to at most maxRunes Unicode code points,
// appending "..." if truncation occurred.
// If maxRunes <= 0, s is returned unchanged.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
