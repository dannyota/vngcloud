//go:build unix

package cli

import (
	"os"
	"syscall"
)

// openRegularFileNonBlocking opens path for reading without O_NONBLOCK's
// usual meaning turned into a hang: a FIFO named at path would otherwise make
// a plain os.Open block until a writer connects, turning a read of an
// existing file into a denial of service if the path was swapped for a FIFO
// between an earlier check and this open. O_NONBLOCK makes the open return
// immediately regardless, and has no effect on a regular file's own reads.
// This mirrors internal/core's own openConfigFile, reimplemented here
// because internal/cli imports only the public SDK, never another internal
// package. Every caller stats the returned file and refuses anything that
// is not a regular file, since O_NONBLOCK alone still opens a FIFO
// successfully; it only stops the open from blocking. Callers include the
// config and credentials files (configfile.go), a write command's own file
// flag, such as loadbalancer import-certificate's --certificate-file
// (inputfile.go), and a document file, such as iam create-policy's
// --document-file (documentfile.go).
func openRegularFileNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // path is a resolved config, credentials, input-file, or document-file path, not attacker-controlled input
}
