//go:build !unix

package tokencache

import "os"

// Non-Unix platforms have no Unix FIFO open behavior. Check the named and
// opened files, retaining Windows' existing mode-bit exception.
func openServiceAccountFile(path string, flags int) (*os.File, error) {
	if err := checkServiceAccountPath(path, flags&os.O_CREATE != 0); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	if err := validateServiceAccountFile(path, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
