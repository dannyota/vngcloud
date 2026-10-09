package cli

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/storage"
)

const policyFileScheme = "file://"

// guardPutBucketPolicy resolves a file:// --policy to the file's text, then
// requires --yes when the document names an anonymous principal. A document
// that does not parse is left for the SDK to refuse, which exits 2 before
// any request.
func guardPutBucketPolicy(cmd *cobra.Command, in any) error {
	input := in.(*storage.PutBucketPolicyInput)
	if path, ok := strings.CutPrefix(input.Policy, policyFileScheme); ok {
		text, err := readInputFile("policy", path)
		if err != nil {
			return err
		}
		input.Policy = text
	}
	if !policyIsPublic(input.Policy) {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError("storage put-bucket-policy: the policy has a statement for every principal (\"*\"), " +
		"which lets anyone on the internet use the bucket; pass --yes to confirm")
}

// policyIsPublic reports whether any statement's Principal is "*", {"AWS":
// "*"}, or a list holding "*". Member names match without regard to case,
// like Go's decoder, so a spelling the server might accept is not missed.
func policyIsPublic(policy string) bool {
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(policy), &doc) != nil {
		return false
	}
	var statements []map[string]json.RawMessage
	if json.Unmarshal(memberFold(doc, "Statement"), &statements) != nil {
		return false
	}
	for _, statement := range statements {
		if principalIsAnyone(memberFold(statement, "Principal"), true) {
			return true
		}
	}
	return false
}

// principalIsAnyone checks a Principal value: a string or list holding "*",
// or, at the top level only, an object whose AWS member is one.
func principalIsAnyone(raw json.RawMessage, top bool) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == "*"
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, s := range many {
			if s == "*" {
				return true
			}
		}
		return false
	}
	var object map[string]json.RawMessage
	if top && json.Unmarshal(raw, &object) == nil {
		return principalIsAnyone(memberFold(object, "AWS"), false)
	}
	return false
}

func memberFold(object map[string]json.RawMessage, name string) json.RawMessage {
	for k, v := range object {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}
