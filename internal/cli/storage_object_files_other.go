//go:build !linux && !darwin

package cli

import (
	"errors"
	"os"
)

func objectFilesSupported() bool { return false }
func openObjectFile(string, bool) (*os.File, error) {
	return nil, errors.New("object file transfers are unsupported on this platform")
}
func privateObjectCredentials(*os.File) bool { return false }
func syncObjectDirectory(string) error {
	return errors.New("object file transfers are unsupported on this platform")
}
