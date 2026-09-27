//go:build !unix

package cli

import "os"

// openSecretFileExclusive creates path fresh. Non-Unix platforms have no
// O_NOFOLLOW; O_EXCL alone still rejects any path something now occupies,
// which is the same protection openfile_other.go accepts for configure's
// own file writes on these platforms.
func openSecretFileExclusive(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) //nolint:gosec // path is refused unless nothing exists there yet; see checkSecretFilePath
}
