// Command vngcloud is the AWS-CLI-style command line for VNG Cloud
// (GreenNode). All behavior lives in internal/cli; this file only wires
// process signals and the exit code.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"danny.vn/vngcloud/internal/cli"
)

func main() {
	os.Exit(run())
}

// run holds everything main would otherwise defer past an os.Exit, so
// cancel and the signal handling below actually run before the process
// exits.
func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A first SIGINT or SIGTERM cancels ctx, then the signal is reset to its
	// default disposition, so a second one terminates the process at once
	// even while a command is blocked on a password read that ctx cannot
	// interrupt (term.ReadPassword has no context support).
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		if _, ok := <-sig; ok {
			cancel()
			signal.Stop(sig)
			signal.Reset(os.Interrupt, syscall.SIGTERM)
		}
	}()

	code := cli.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	signal.Stop(sig)
	close(sig)
	return code
}
