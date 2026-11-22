package tokencache

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

//nolint:revive // This method is an internal package boundary.
func (c *Cache) ReadTOTPStep(key Key, current int64) (int64, bool) {
	path := filepath.Join(c.dir, key.Hash()+".totp")
	f, err := openServiceAccountFile(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			return 0, false
		}
		return current, true
	}
	if err != nil {
		return current, true
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 32))
	if err != nil || validateServiceAccountFile(path, f) != nil {
		return current, true
	}
	step, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil || step < 0 {
		return current, true
	}
	return step, true
}

//nolint:revive // This method is an internal package boundary.
func (c *Cache) WriteTOTPStep(ctx context.Context, key Key, step int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(c.dir, ".totp-*")
	if err != nil {
		return ErrUnavailable
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	_, writeErr := f.WriteString(strconv.FormatInt(step, 10))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(c.dir, key.Hash()+".totp")); err != nil {
		return ErrUnavailable
	}
	return nil
}
