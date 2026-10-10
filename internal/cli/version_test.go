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

func TestVersionFlagsPrintTheVersionCommandLine(t *testing.T) {
	withCleanEnv(t)
	var want, stderr bytes.Buffer
	if code := Main(context.Background(), []string{"version"}, strings.NewReader(""), &want, &stderr); code != 0 {
		t.Fatalf("version exit code = %d; stderr=%q", code, stderr.String())
	}
	for _, flag := range []string{"--version", "-v"} {
		var stdout, errOut bytes.Buffer
		code := Main(context.Background(), []string{flag}, strings.NewReader(""), &stdout, &errOut)
		if code != 0 {
			t.Fatalf("%s: exit code = %d, want 0; stderr=%q", flag, code, errOut.String())
		}
		if errOut.Len() != 0 {
			t.Fatalf("%s: stderr = %q, want empty", flag, errOut.String())
		}
		if stdout.String() != want.String() {
			t.Fatalf("%s: stdout = %q, want %q", flag, stdout.String(), want.String())
		}
	}
}
