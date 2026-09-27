//go:build unix

package cli

import (
	"context"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"
)

// TestComputeCreateServerUserDataFileFIFORefused checks that a FIFO named at
// --user-data-file is refused before any request instead of blocking the
// command, the same rule readInputFile's own FIFO test
// (TestReadInputFileFIFORefused) checks directly. The read runs through the
// real command, in-process, so a regression that reintroduces a blocking
// open would hang this test rather than fail it quickly; readInputFile's own
// test already bounds that risk with a goroutine and a timeout.
func TestComputeCreateServerUserDataFileFIFORefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user-data.sh")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateServerArgs, "--user-data-file", path)...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a refusal for a FIFO --user-data-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}
