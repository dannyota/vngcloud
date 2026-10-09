package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/storage"
)

const policyFileScheme = "file://"

// guardPutBucketPolicy resolves a file:// --policy to the file's text, then
// requires --yes when an Allow statement's principal contains "*". A document
// the SDK refuses exits 2 with the SDK's message before any request.
func guardPutBucketPolicy(cmd *cobra.Command, in any) error {
	input := in.(*storage.PutBucketPolicyInput)
	if path, ok := strings.CutPrefix(input.Policy, policyFileScheme); ok {
		text, err := readInputFile("policy", path)
		if err != nil {
			return err
		}
		input.Policy = text
	}
	public, err := storage.PolicyHasPublicPrincipal(input.Policy)
	if err != nil {
		return err
	}
	if !public {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError("storage put-bucket-policy: an Allow statement has a principal with \"*\", " +
		"which can let anyone on the internet use the bucket; pass --yes to confirm")
}
