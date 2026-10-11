//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package tokencache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestServiceAccountCacheFIFORefusal(t *testing.T) {
	for _, lock := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "cache")
		tokenPath, lockPath := serviceAccountPaths(dir, serviceAccountTestKey())
		if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
			t.Fatal(err)
		}
		path := tokenPath
		if lock {
			path = lockPath
		}
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		c := New(dir, time.Now)
		done := make(chan error, 1)
		go func() {
			_, err := c.GetServiceAccount(context.Background(), serviceAccountTestKey(), "", func(context.Context) (Token, error) { t.Error("FIFO triggered login"); return Token{}, nil })
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("FIFO blocked cache")
		}
	}
}

func TestServiceAccountLockReplacementRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	file, err := openServiceAccountFile(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := validateServiceAccountFile(path, file); err == nil {
		_ = file.Close()
		t.Fatal("replaced lock inode accepted")
	}
	_ = file.Close()
}
