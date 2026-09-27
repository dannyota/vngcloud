//go:build unix

package cli

import (
	"context"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestIAMCreatePolicyDocumentFileFIFODoesNotHang checks that a
// --document-file naming a FIFO is refused quickly, with exit 2 and no
// request sent, rather than blocking on the open the way a plain os.Open
// would: readPolicyDocumentFile opens without O_NONBLOCK's usual meaning
// turned into a hang, then refuses the FIFO once Stat shows it is not a
// regular file. The command runs on a goroutine with a bounded wait, so a
// regression that does block fails this test instead of hanging the whole
// run.
func TestIAMCreatePolicyDocumentFileFIFODoesNotHang(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", path})

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for a FIFO document file")
		}
		if got := exitCode(err); got != 2 {
			t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
		}
		if n := fixture.requestCount(); n != 0 {
			t.Fatalf("requestCount = %d, want 0", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("create-policy hung reading a FIFO --document-file")
	}
}
