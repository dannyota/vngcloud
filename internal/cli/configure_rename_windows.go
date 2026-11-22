//go:build windows

package cli

import (
	"context"
	"errors"
	"os"
	"syscall"
)

func renameConfigFile(ctx context.Context, old, new string, before func() error) error {
	return renameWithRetry(ctx, old, new, func(a, b string) error {
		if err := before(); err != nil {
			return err
		}
		return os.Rename(a, b)
	}, func(err error) bool { return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(5)) })
}
