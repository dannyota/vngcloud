package cli

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseVersionEmbedding(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "vngcloud")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-ldflags=-X danny.vn/vngcloud/internal/cli.releaseVersion=v1.2.3", "-o", binary, "../../cmd/vngcloud") //nolint:gosec // The output path is a test-owned temporary directory.
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	for _, arg := range []string{"version", "--version"} {
		output, err := exec.CommandContext(t.Context(), binary, arg).CombinedOutput() //nolint:gosec // The binary and arguments are created by this test.
		if err != nil {
			t.Fatalf("%s: %v: %s", arg, err, output)
		}
		want := "vngcloud v1.2.3 " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
		if strings.TrimSpace(string(output)) != want {
			t.Fatalf("%s: got %q, want %q", arg, output, want)
		}
	}
}
