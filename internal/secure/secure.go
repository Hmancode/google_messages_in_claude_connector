// Package secure encrypts the pairing session at rest.
//
// On Windows it uses DPAPI with user scope: only the same Windows user on the
// same computer can decrypt the file. On other systems (used only for
// development and tests today) the data is stored as-is with owner-only file
// permissions, and Encrypted reports false so pairing_status can warn.
package secure

import (
	"bytes"
	"errors"
)

var magic = []byte("GMR1")

const (
	modePlain byte = 0
	modeDPAPI byte = 1
)

// ErrWrongMode is returned when a file was written in a mode this build cannot read.
var ErrWrongMode = errors.New("session file was encrypted on a different kind of system; pair again")

// Seal returns the bytes to write to disk.
func Seal(plain []byte) ([]byte, error) {
	body, mode, err := protect(plain)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+1+len(body))
	out = append(out, magic...)
	out = append(out, mode)
	out = append(out, body...)
	return out, nil
}

// Open reverses Seal.
func Open(sealed []byte) ([]byte, error) {
	if len(sealed) < len(magic)+1 || !bytes.Equal(sealed[:len(magic)], magic) {
		return nil, errors.New("session file is not in gm-reader format")
	}
	mode := sealed[len(magic)]
	return unprotect(sealed[len(magic)+1:], mode)
}
