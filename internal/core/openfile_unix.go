//go:build unix

package core

import (
	"os"
	"syscall"
)

// openConfigFile opens path for reading without O_NONBLOCK's usual meaning
// turned into a hang: a FIFO named at path would otherwise make a plain
// os.Open block until a writer connects, turning a config read into a denial
// of service if the path is swapped for a FIFO between an earlier check and
// this open. O_NONBLOCK makes the open return immediately regardless, and
// has no effect on a regular file's own reads.
func openConfigFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // path is an explicit option/env value or the resolved home directory, not attacker-controlled input
}
