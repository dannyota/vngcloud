package cli

import (
	"net/netip"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

// refuseWorldOpenIngressWithoutYes is create-security-group-rule's Guard. It
// needs --yes for a rule whose RemoteIPPrefix has prefix length 0
// (0.0.0.0/0 or ::/0, whatever string form parses to that), unless Direction
// is exactly "egress" (matched case-insensitively, since the SDK sends
// Direction to the server as given rather than restricting it to a fixed
// value set), since such a rule opens every port it names to every scanner
// on the internet for as long as it lasts, the same risk --yes already
// guards for a destructive command.
//
// The check fails closed rather than open: only a Direction that reads as
// "egress" is exempt, so a value that is not exactly "ingress" either, such
// as "INGRESS", a value with stray whitespace, or one this guard does not
// recognize at all, still needs --yes for a length-0 prefix. This guard
// does not rely on the SDK to reject an unrecognized Direction; it must
// keep failing closed even if that check is missing or runs after this one.
// A rule with any narrower prefix such as 10.0.0.0/8 needs no --yes,
// whatever its Direction.
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
	if strings.EqualFold(create.Direction, "egress") {
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
		"create-security-group-rule: a rule that is not egress, from %s, is open to the entire internet; pass --yes to confirm",
		create.RemoteIPPrefix)
}

// requireYesToChangeRoutes returns add-route's or remove-route's Guard
// (command names the one registering it, for its message): it refuses the
// command outright unless --yes is given, whatever the merged Input holds.
// Per the design, both need --yes on every call, since either can redirect
// or cut traffic for every server behind the route table and the CLI has no
// cheap way to tell whether that table is in use. Unlike delete-route-table,
// neither op carries Destructive: ADR 0002 rule 6 defines destructive as not
// undoable by one more command, and running the other of this pair (a
// RemoveRoute after an AddRoute, or the reverse) does undo it.
func requireYesToChangeRoutes(command string) func(cmd *cobra.Command, in any) error {
	return func(cmd *cobra.Command, _ any) error {
		if yes, _ := cmd.Flags().GetBool("yes"); yes {
			return nil
		}
		return newUsageError(
			"network %s changes routing for every server behind this table; pass --yes to confirm", command)
	}
}
