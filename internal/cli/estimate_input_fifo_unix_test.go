//go:build unix

package cli

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestEstimateFIFOWithoutWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSvcFixture(nil)
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"pricing", "estimate", "--cli-input-json", "file://" + path})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(context.Background()) }()
	select {
	case err := <-done:
		if exitCode(err) != 2 || stdout.Len() != 0 || fixture.requestCount() != 0 {
			t.Fatalf("FIFO: exit=%d requests=%d stdout=%s err=%v", exitCode(err), fixture.requestCount(), stdout, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FIFO open blocked without a writer")
	}
}
