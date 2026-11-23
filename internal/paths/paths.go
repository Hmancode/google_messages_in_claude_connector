// Package paths decides where gm-reader keeps its files.
//
// On Windows everything lives under %LOCALAPPDATA%\gm-reader\.
// Elsewhere it uses $XDG_DATA_HOME/gm-reader or ~/.local/share/gm-reader.
// GM_READER_HOME overrides both (used by tests and CI).
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

const appDir = "gm-reader"

// Dir returns the data directory, creating it if needed.
func Dir() (string, error) {
	var base string
	if v := os.Getenv("GM_READER_HOME"); v != "" {
		base = v
	} else if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			var err error
			local, err = os.UserCacheDir()
			if err != nil {
				return "", err
			}
		}
		base = filepath.Join(local, appDir)
	} else {
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			data = filepath.Join(home, ".local", "share")
		}
		base = filepath.Join(data, appDir)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return base, nil
}

func file(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// SessionFile is the encrypted pairing credential.
func SessionFile() (string, error) { return file("session.bin") }

// DatabaseFile is the local SQLite message cache.
func DatabaseFile() (string, error) { return file("cache.db") }

// LogFile is the log file. Message bodies are never written to it.
func LogFile() (string, error) { return file("gm-reader.log") }
