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

func TestIAMCacheFIFORefusal(t *testing.T) {
	for _, suffix := range []string{".json", ".lock"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			chmodDirForTest(t, dir, 0o700)
			path := filepath.Join(dir, testKey().Hash()+suffix)
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := New(dir, time.Now).Get(context.Background(), testKey(), "", func(context.Context) (Token, error) { return Token{}, errors.New("unexpected login") })
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err == nil {
					_ = file.Close()
				}
				t.Fatal("FIFO blocked cache")
			}
		})
	}
}
