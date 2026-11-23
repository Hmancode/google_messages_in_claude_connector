//go:build windows

package secure

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Encrypted is true when the session file is protected by the operating system.
const Encrypted = true

// Extra entropy so a different program calling DPAPI with the same user
// cannot decrypt the blob without also knowing this value.
var entropy = []byte("gm-reader session v1")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func protect(plain []byte) ([]byte, byte, error) {
	var out windows.DataBlob
	name, _ := windows.UTF16PtrFromString("gm-reader session")
	err := windows.CryptProtectData(blob(plain), name, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, 0, fmt.Errorf("DPAPI encrypt failed: %w", err)
	}
	return takeBlob(&out), modeDPAPI, nil
}

func unprotect(body []byte, mode byte) ([]byte, error) {
	if mode != modeDPAPI {
		return nil, ErrWrongMode
	}
	var out windows.DataBlob
	err := windows.CryptUnprotectData(blob(body), nil, blob(entropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, fmt.Errorf("DPAPI decrypt failed (was this file made by a different Windows user?): %w", err)
	}
	return takeBlob(&out), nil
}
