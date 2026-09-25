//go:build !windows

package core

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

// TestLoadConfigFIFOPathErrors confirms a FIFO named as the config file is
// refused, same as a directory. FIFOs are a Unix concept, so this test does
// not run on Windows.
func TestLoadConfigFIFOPathErrors(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	fifo := filepath.Join(home, "afifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO in this environment: %v", err)
	}

	_, err := LoadConfig(context.Background(), WithSharedCredentialsFile(fifo), WithStaticToken("tok"))
	if !errors.Is(err, ErrCredentialsFile) {
		t.Fatalf("err = %v, want ErrCredentialsFile for a FIFO credentials path", err)
	}
}
