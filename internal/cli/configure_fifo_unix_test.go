//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestConfigureFIFORefused lives in its own unix-only file because
// syscall.Mkfifo does not exist on windows at all (not just at runtime):
// keeping it here, rather than behind a runtime skip, lets
// GOOS=windows go vet ./... type-check every other test in this package.
func TestConfigureFIFORefused(t *testing.T) {
	home := withCleanEnv(t)
	dir := filepath.Join(home, ".vngcloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := configPath(home)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	_, err := runConfigure(t, "", []string{"configure", "set", "region", "x"})
	if err == nil {
		t.Fatalf("expected an error for a FIFO")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}
