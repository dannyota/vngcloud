package cli

import (
	"context"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
)

// userDataFileFlagName is create-server's own file flag for cloud-init user
// data. UserData is vngcloud.Secret, so flagSpecsFor already gives it no
// flag of its own and applyCLIInputJSON already refuses it from
// --cli-input-json entirely; --user-data-file is the only way to set it.
const userDataFileFlagName = "user-data-file"

func registerUserDataFileFlag(cmd *cobra.Command) {
	cmd.Flags().String(userDataFileFlagName, "", "path to a file holding cloud-init user data, at most 64 KiB")
}

// populateCreateServerUserData is create-server's Guard: it runs on the
// merged Input, after --cli-input-json and every flag are applied but
// before checkRequiredFlags and before any request, and reads
// --user-data-file, only when the operator actually gave it, into UserData.
// Reading the file never depends on read-only: it is a local file, not a
// request, and runOp checks read-only right after this guard returns.
func populateCreateServerUserData(cmd *cobra.Command, in any) error {
	input, ok := in.(*compute.CreateServerInput)
	if !ok {
		return nil
	}
	if !cmd.Flags().Changed(userDataFileFlagName) {
		return nil
	}
	path, _ := cmd.Flags().GetString(userDataFileFlagName)
	content, err := readInputFile(userDataFileFlagName, path)
	if err != nil {
		return err
	}
	input.UserData = vngcloud.Secret(content)
	return nil
}

// createServerOp builds create-server's Op directly, rather than through
// Write, since UserData's value comes from populateCreateServerUserData's
// file flag instead of flags.go's own reflection, the same reason
// loadbalancer's importCertificateOp is hand-built for PrivateKey and
// Passphrase. methodName is set by hand to "CreateServer" so checkOpName's
// name-matches-the-SDK-method rule still holds. create-server is Write but
// not Destructive: per the paid writes design, --max-price is its own
// consent, and the default of 0 already orders nothing since the quote
// guard refuses any priced order above it.
func createServerOp() Op[compute.Client] {
	return Op[compute.Client]{
		name:       kebab("CreateServer"),
		methodName: "CreateServer",
		kind:       kindWrite,
		guard:      populateCreateServerUserData,
		extraFlags: registerUserDataFileFlag,
		newInput:   func() any { return new(compute.CreateServerInput) },
		newOutput:  func() any { return new(compute.CreateServerOutput) },
		call: func(_ *cobra.Command, client *compute.Client, ctx context.Context, in any) (any, error) {
			return client.CreateServer(ctx, in.(*compute.CreateServerInput))
		},
	}
}
