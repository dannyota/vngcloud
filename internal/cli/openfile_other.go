//go:build !unix

package cli

import "os"

// openRegularFileNonBlocking opens path for reading. Non-Unix platforms have
// no equivalent to a FIFO's open-blocks-until-a-writer behavior, so a plain
// open is enough. This mirrors internal/core's own openConfigFile,
// reimplemented here because internal/cli imports only the public SDK,
// never another internal package. See openfile_unix.go for the full list of
// callers.
func openRegularFileNonBlocking(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // path is a resolved config, credentials, input-file, or document-file path, not attacker-controlled input
}
