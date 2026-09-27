package cli

import (
	"net/netip"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

// refuseWorldOpenIngressWithoutYes is create-security-group-rule's Guard. It
// needs --yes for an ingress rule whose RemoteIPPrefix has prefix length 0
// (0.0.0.0/0 or ::/0, whatever string form parses to that), since such a
// rule opens every port it names to every scanner on the internet for as
// long as it lasts, the same risk --yes already guards for a destructive
// command. Direction is matched case-insensitively, since the SDK sends it
// to the server as given rather than restricting it to a fixed value set.
// An egress rule, and an ingress rule with any narrower prefix such as
// 10.0.0.0/8, need no --yes.
//
// It runs on the merged Input, after --cli-input-json and every flag are
// applied, so a prefix or direction set through --cli-input-json is checked
// exactly the same as one set by flag. RemoteIPPrefix's own shape (it must
// parse as a CIDR prefix) is left to the SDK's own check, which runs after
// this guard: a value that fails to parse here is let through so that later,
// more specific error is the one the caller sees.
func refuseWorldOpenIngressWithoutYes(cmd *cobra.Command, in any) error {
	create, ok := in.(*network.CreateSecurityGroupRuleInput)
	if !ok {
		return nil
	}
	if !strings.EqualFold(create.Direction, "ingress") {
		return nil
	}
	prefix, err := netip.ParsePrefix(create.RemoteIPPrefix)
	if err != nil {
		return nil //nolint:nilerr // a bad prefix is this guard's business to ignore; the SDK's own shape check reports it
	}
	if prefix.Bits() != 0 {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"create-security-group-rule: an ingress rule from %s is open to the entire internet; pass --yes to confirm",
		create.RemoteIPPrefix)
}
