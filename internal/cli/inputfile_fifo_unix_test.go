//go:build unix

package cli

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadInputFileFIFORefused checks that readInputFile refuses a FIFO
// named at a write command's own file flag path (such as
// --certificate-file) instead of blocking until a writer connects, the same
// rule configure_fifo_unix_test.go checks for the config file. The read
// runs in a goroutine with its own timeout so a regression that reintroduces
// the blocking open fails this test instead of hanging it forever.
func TestReadInputFileFIFORefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	type result struct {
		content string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		content, err := readInputFile("certificate-file", path)
		done <- result{content, err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("expected an error for a FIFO, got content %q", r.content)
		}
		if !strings.Contains(r.err.Error(), "not a regular file") {
			t.Fatalf("err = %v, want a not-a-regular-file refusal", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readInputFile blocked on a FIFO instead of refusing it")
	}
}

func TestCLIInputJSONFIFORefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { var input struct{ Name string }; done <- applyCLIInputJSON("file://"+path, &input) }()
	select {
	case err := <-done:
		if err == nil || exitCode(err) != 2 {
			t.Fatal("FIFO JSON accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO JSON stalled")
	}
}

func TestCLIInputJSONFIFOUnrelatedCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"iam", "list-actions", "--cli-input-json", "file://" + path})
	done := make(chan error, 1)
	go func() { done <- root.Execute() }()
	select {
	case err := <-done:
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
			t.Fatal("FIFO input not refused")
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated command stalled on JSON FIFO")
	}
}
