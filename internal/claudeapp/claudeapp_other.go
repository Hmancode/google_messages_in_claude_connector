//go:build !windows

package claudeapp

import "errors"

// Supported reports whether this system can restart Claude Desktop.
const Supported = false

// Running lists Claude Desktop processes (Windows only).
func Running() ([]Proc, error) { return nil, nil }

// Quit is only supported on Windows.
func Quit([]Proc) error { return errors.New("not supported on this system") }

// Start is only supported on Windows.
func Start(string) error { return errors.New("not supported on this system") }
