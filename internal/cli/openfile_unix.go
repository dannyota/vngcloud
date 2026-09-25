//go:build unix

package cli

import (
	"os"
	"syscall"
)

// openConfigureFile opens path for reading without O_NONBLOCK's usual
// meaning turned into a hang: a FIFO named at path would otherwise make a
// plain os.Open block until a writer connects, turning a read of an existing
// config or credentials file into a denial of service if the path was
// swapped for a FIFO between an earlier check and this open. O_NONBLOCK
// makes the open return immediately regardless, and has no effect on a
// regular file's own reads. This mirrors internal/core's own
// openConfigFile, reimplemented here because internal/cli imports only the
// public SDK, never another internal package.
func openConfigureFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // path is the resolved config or credentials path, not attacker-controlled input
}
