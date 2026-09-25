//go:build !unix

package core

import "os"

// openConfigFile opens path for reading. Non-Unix platforms have no
// equivalent to a FIFO's open-blocks-until-a-writer behavior, so a plain
// open is enough.
func openConfigFile(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // path is an explicit option/env value or the resolved home directory, not attacker-controlled input
}
