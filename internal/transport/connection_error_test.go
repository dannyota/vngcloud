package transport

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestNetworkFailureNamesConnectionReset(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{syscall.ECONNRESET, "connection reset"},
		{&net.OpError{Op: "read", Err: fmt.Errorf("private details: %w", syscall.ECONNRESET)}, "read: connection reset"},
	} {
		if got := NetworkFailureCause(tc.cause); got != tc.want {
			t.Errorf("cause = %q, want %q", got, tc.want)
		}
		safe := sanitizeNetworkError(tc.cause, nil)
		if safe.Error() != tc.want {
			t.Errorf("safe cause = %q", safe.Error())
		}
		if !errors.Is(safe, syscall.ECONNRESET) {
			t.Error("reset sentinel lost")
		}
	}
}
