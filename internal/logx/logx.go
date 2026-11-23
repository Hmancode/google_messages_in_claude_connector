// Package logx sets up logging to %LOCALAPPDATA%\gm-reader\gm-reader.log.
//
// gm-reader's own code never logs message text, names or cookies. libgm's
// logs pass through a filter that removes raw data fields (see internal/gm).
// Nothing is written to stdout, which carries the MCP protocol.
package logx

import (
	"io"
	"os"

	"github.com/rs/zerolog"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/gm"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/paths"
)

const maxLogSize = 5 << 20 // rotate at 5 MB

// Open returns the app logger, the libgm logger, and a closer.
func Open(debug bool) (zerolog.Logger, zerolog.Logger, io.Closer, error) {
	p, err := paths.LogFile()
	if err != nil {
		return zerolog.Nop(), zerolog.Nop(), nopCloser{}, err
	}
	if st, err := os.Stat(p); err == nil && st.Size() > maxLogSize {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return zerolog.Nop(), zerolog.Nop(), nopCloser{}, err
	}
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}
	app := zerolog.New(f).Level(level).With().Timestamp().Str("component", "gm-reader").Logger()
	return app, gm.LibgmLogger(f, debug), f, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
