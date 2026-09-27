package cli

import (
	"io"
	"os"
	"strings"
)

// maxInputFileSize caps how much a write command's own file flag (such as
// loadbalancer import-certificate's --certificate-file) reads: enough for
// any real PEM certificate, chain, or key, small enough that a mistaken
// path cannot exhaust memory. It is unrelated to maxCLIInputJSONSize
// (input.go), which bounds a --cli-input-json file:// value instead.
const maxInputFileSize = 64 * 1024

// readInputFile reads path, named by flagName, for a write command's own
// Input field: at most maxInputFileSize bytes, following symlinks like any
// other file argument, unlike --secret-file's own write path
// (secretfile.go), which refuses one since it is about to create a file
// rather than read an existing one. An empty file is refused. Every error
// names flagName and path, never the file's content.
func readInputFile(flagName, path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from a flag the operator typed, exactly like a shell command reading its own argument
	if err != nil {
		return "", newUsageError("--%s: %s", flagName, err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxInputFileSize+1))
	if err != nil {
		return "", newUsageError("--%s: %s: %s", flagName, path, err)
	}
	if len(data) > maxInputFileSize {
		return "", newUsageError("--%s: %s is larger than %d bytes", flagName, path, maxInputFileSize)
	}
	if len(data) == 0 {
		return "", newUsageError("--%s: %s is empty", flagName, path)
	}
	return string(data), nil
}

// dropTrailingNewline removes one trailing "\r\n" or "\n" from s, the same
// rule configure.go's readStdinValue applies to a value configure set <key>
// - reads from stdin: import-certificate's --passphrase-file applies it
// too, so a passphrase saved with a text editor's own trailing newline is
// not sent with one extra byte the server never expected.
func dropTrailingNewline(s string) string {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "\n"):
		return s[:len(s)-1]
	default:
		return s
	}
}
