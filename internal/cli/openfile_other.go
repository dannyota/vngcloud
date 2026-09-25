//go:build !unix

package cli

import "os"

// openConfigureFile opens path for reading. Non-Unix platforms have no
// equivalent to a FIFO's open-blocks-until-a-writer behavior, so a plain
// open is enough. This mirrors internal/core's own openConfigFile,
// reimplemented here because internal/cli imports only the public SDK,
// never another internal package.
func openConfigureFile(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // path is the resolved config or credentials path, not attacker-controlled input
}
