package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
)

// globalFlags holds the flags every command shares. cobra writes directly
// into these fields as it parses, so a subcommand's RunE reads them without
// any lookup by name.
type globalFlags struct {
	profile   string
	region    string
	projectID string
	output    string
	query     string
	yes       bool
	debug     bool
	readOnly  bool
}

// env bundles the I/O streams and parsed global flags a command needs. One
// env is built per Main call and passed to every command constructor, so no
// command reads a package-level variable for these.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	flags  *globalFlags
}

// testOptions, when set by a _test.go file in this package, are appended to
// every vngcloud.Config the CLI builds (see loadConfig in config.go), so
// tests can route every endpoint at an httptest server and install a
// transport that refuses any non-loopback host. It is always nil in the
// built binary, since no non-test file in this package ever assigns it.
var testOptions []vngcloud.LoadOption

// newRootCmd builds the command tree. It never keeps state across calls, so
// Main (and every test) gets an independent root each time.
func newRootCmd(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	flags := &globalFlags{}
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr, flags: flags}

	root := &cobra.Command{
		Use:           "vngcloud",
		Short:         "Command-line access to GreenNode",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          parentArgs,
		RunE:          unknownCommandRunE,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(stdin)
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(flagErrorFunc)

	root.PersistentFlags().StringVar(&flags.profile, "profile", "", "profile to use")
	root.PersistentFlags().StringVar(&flags.region, "region", "", "region to use")
	root.PersistentFlags().StringVar(&flags.projectID, "project-id", "", "project id to use")
	root.PersistentFlags().StringVar(&flags.output, "output", "", "output format: json, table, or text (default from the profile, else json)")
	root.PersistentFlags().StringVar(&flags.query, "query", "", "JMESPath query to filter output")
	root.PersistentFlags().BoolVar(&flags.yes, "yes", false, "confirm a destructive operation")
	root.PersistentFlags().BoolVar(&flags.debug, "debug", false, "log requests to stderr")
	root.PersistentFlags().BoolVar(&flags.readOnly, "read-only", false, "refuse every write command")

	root.AddCommand(newVersionCmd(e))
	root.AddCommand(newConfigureCmd(e))
	root.AddCommand(newGenDocsCmd(e))
	root.AddCommand(newBillingCmd(e))
	root.AddCommand(newPricingCmd(e))
	root.AddCommand(newComputeCmd(e))
	root.AddCommand(newNetworkCmd(e))
	root.AddCommand(newDNSCmd(e))
	root.AddCommand(newCDNCmd(e))
	root.AddCommand(newMonitorCmd(e))
	root.AddCommand(newProjectCmd(e))
	root.AddCommand(newPortalCmd(e))
	root.AddCommand(newLoadBalancerCmd(e))
	root.AddCommand(newVolumeCmd(e))

	return root
}

// flagErrorFunc turns cobra's own flag-parsing errors into usageError, so
// they classify and exit exactly like every other usage mistake.
func flagErrorFunc(_ *cobra.Command, err error) error {
	return usageError{msg: err.Error()}
}

// noArgs is cobra.NoArgs wrapped so its error is a usageError. Every leaf
// operation command uses this as its Args validator.
func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return usageError{msg: err.Error()}
	}
	return nil
}

// unknownCommandRunE is the RunE for the root and every parent (service)
// command: with no further arguments it prints help. parentArgs below
// already turns a leftover argument into a usage error before RunE runs, so
// this is only ever reached with args empty.
func unknownCommandRunE(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

// parentArgs is the Args validator for the root and every parent (service)
// command. Cobra's own default (a nil Args field) reports an unrecognized
// subcommand name as a plain error from Find, before RunE or any Args
// validator would run, so it can never be turned into a usageError; setting
// Args explicitly, to this function, routes that case through
// ValidateArgs instead, where a usageError can be returned.
func parentArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return usageError{msg: fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())}
}
