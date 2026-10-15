//go:build unix

package cli

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestIAMPasswordUnsafeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("synthetic-input"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, "/dev/null"} {
		if _, err := readUserPassword(context.Background(), name, nil, time.Second); err == nil {
			t.Fatal("unsafe input accepted")
		}
	}
	fifo := path + "-fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readUserPassword(context.Background(), fifo, nil, time.Second); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO stalled")
	}
}
