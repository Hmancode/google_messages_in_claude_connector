//go:build !windows

package confirm

import (
	"errors"
	"time"
)

// showYesNo is not available outside Windows. Write actions in dialog mode are
// therefore refused on other systems, which is the safe default.
func showYesNo(string, string, time.Duration) (bool, error) {
	return false, errors.New("confirmation dialogs are only available on Windows")
}
