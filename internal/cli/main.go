// Package cli implements the vngcloud command: a cobra root command built
// fresh per invocation, one operation table per SDK service, output
// formatting, and configure and gen-docs. It calls GreenNode only through
// the public danny.vn/vngcloud package.
package cli

import (
	"context"
	"io"
)

// Main builds a fresh command tree from args, runs it, and returns the
// process exit code. Results go to stdout; a failed command's error and any
// --debug logging go to stderr. The caller owns calling os.Exit.
func Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCmd(stdin, stdout, stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil {
		printError(stderr, err)
	}
	return exitCode(err)
}
