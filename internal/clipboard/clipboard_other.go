//go:build !windows

// Package clipboard reads text from the clipboard (Windows only).
package clipboard

import "errors"

// ReadText is not implemented outside Windows; use --curl-file instead.
func ReadText() (string, error) {
	return "", errors.New("clipboard reading is only supported on Windows; use --curl-file")
}

// Supported reports whether ReadText works on this system.
const Supported = false
