//go:build !windows

package secure

// Encrypted is false: on this system the session file relies on file
// permissions only. gm-reader targets Windows; this path exists for
// development and tests.
const Encrypted = false

func protect(plain []byte) ([]byte, byte, error) {
	return append([]byte(nil), plain...), modePlain, nil
}

func unprotect(body []byte, mode byte) ([]byte, error) {
	if mode != modePlain {
		return nil, ErrWrongMode
	}
	return append([]byte(nil), body...), nil
}
