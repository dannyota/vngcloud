package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

func newVersionCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the vngcloud version",
		Args:  noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(e.stdout, versionString())
			return err
		},
	}
}

// versionString reports the module version from the build info embedded by
// `go build` and `go install` (or "(devel)" when there is none, as for a
// plain `go run` or a binary built from a local checkout), plus the Go
// version and platform it was built for.
func versionString() string {
	version := "(devel)"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		version = info.Main.Version
	}
	return fmt.Sprintf("vngcloud %s go%s %s/%s",
		version, strings.TrimPrefix(runtime.Version(), "go"), runtime.GOOS, runtime.GOARCH)
}
