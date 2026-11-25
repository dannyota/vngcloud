//go:build livewrite && !darwin && !linux

package livetest_test

import (
	"errors"
	"os"
)

func natHoldOpenResult(*os.Root, string) (*os.File, error) {
	return nil, errors.New("NAT hold unsupported")
}

func natHoldOwned(os.FileInfo) bool { return false }
