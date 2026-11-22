//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package cli

import (
	"context"
	"errors"
	"os"
)

func openConfigLock(context.Context, string) (*os.File, error) {
	return nil, errors.New("configure locking unsupported on this platform")
}

func acquireConfigLock(context.Context, string) (func(), error) {
	return nil, errors.New("configure locking unsupported on this platform")
}
