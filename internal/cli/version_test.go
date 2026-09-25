package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	withCleanEnv(t)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{"version"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "vngcloud ") {
		t.Fatalf("stdout = %q, want it to start with %q", out, "vngcloud ")
	}
	if !strings.Contains(out, "go") {
		t.Fatalf("stdout = %q, want it to name the Go version", out)
	}
}

func TestVersionCommandRejectsArgs(t *testing.T) {
	withCleanEnv(t)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{"version", "extra"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
}
