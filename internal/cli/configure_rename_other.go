//go:build !windows

package cli

import (
	"context"
	"os"
)

func renameConfigFile(ctx context.Context, old, new string, before func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := before(); err != nil {
		return err
	}
	return os.Rename(old, new)
}
