//go:build unix

package cli

import (
	"os"
	"syscall"
)

// openSecretFileExclusive creates path fresh, refusing a symlink even one
// created between checkSecretFilePath's check and this call: O_EXCL rejects
// any path something now occupies, and O_NOFOLLOW additionally refuses to
// follow a symlink there, so a symlink swapped in during that window is
// caught here rather than followed.
func openSecretFileExclusive(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode) //nolint:gosec // path is refused unless nothing exists there yet; see checkSecretFilePath
}
