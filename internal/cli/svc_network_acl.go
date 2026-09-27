package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// requireYesForACLChange returns the Guard for add-network-acl-rule,
// associate-network-acl-subnet, and disassociate-network-acl-subnet
// (command names the one registering it, for its message): it refuses the
// command outright unless --yes is given, whatever the merged Input holds,
// the same shape requireYesToChangeRoutes (svc_network_write.go) uses for
// add-route and remove-route. Per the design, all three need --yes on every
// call: adding a rule can pass or drop traffic for every subnet the ACL
// covers, and moving a subnet's association applies a different ACL's rules
// to its traffic at once; the CLI has no cheap way to tell whether the ACL
// is in active use. remove-network-acl-rule needs the same --yes check, plus
// a --priority requirement; see requireYesAndPriorityToRemoveACLRule below.
func requireYesForACLChange(command string) func(cmd *cobra.Command, in any) error {
	return func(cmd *cobra.Command, _ any) error {
		if yes, _ := cmd.Flags().GetBool("yes"); yes {
			return nil
		}
		return newUsageError(
			"network %s can change which traffic reaches a subnet; pass --yes to confirm", command)
	}
}

// requireYesAndPriorityToRemoveACLRule is remove-network-acl-rule's Guard.
//
// RemoveNetworkACLRuleInput's Priority field carries no vngcloud:"required"
// tag (see its doc comment in network/acl_rules_write.go): 0 is both
// core.CheckRequired's zero value and the live-observed marker for a
// default rule, so the SDK cannot use IsZero to tell "the flag was not
// given" apart from "naming that rule on purpose" without also refusing the
// one call that must be able to name it. The CLI does not have that
// problem: cobra reports whether --priority was actually set (Changed)
// regardless of its value, and cliInputJSONHasKey reports the same for an
// inline or file --cli-input-json value, so this guard requires one of the
// two here instead of letting a caller who simply forgot --priority
// silently target priority 0 and reach network.ErrDefaultResource with a
// confusing message.
func requireYesAndPriorityToRemoveACLRule(cmd *cobra.Command, _ any) error {
	if !cmd.Flags().Changed("priority") && !cliInputJSONHasKey(cmd, "Priority") {
		return newUsageError("--priority is required")
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"network remove-network-acl-rule can change which traffic reaches a subnet; pass --yes to confirm")
}

// cliInputJSONHasKey reports whether cmd's --cli-input-json value, literal
// or file://, sets key at its top level. A Guard that must tell "the caller
// explicitly set this field" apart from "the field is at its zero value"
// checks this alongside cmd.Flags().Changed, since --cli-input-json is the
// merged Input's other source (input.go). By the time any Guard runs,
// applyCLIInputJSON has already parsed and validated the same value for a
// command that got this far, so a failure here cannot mean anything but
// "no --cli-input-json was given", and is treated as key absent rather than
// a second, unreachable error return. Unlike svc_monitor.go's
// literalCLIInputJSONFields, this reads a file:// value too: that check
// exists to keep a secret off argv, which is not this guard's concern.
func cliInputJSONHasKey(cmd *cobra.Command, key string) bool {
	raw, err := cmd.Flags().GetString("cli-input-json")
	if err != nil || raw == "" {
		return false
	}
	data, err := cliInputJSONBytes(raw)
	if err != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}
	_, ok := fields[key]
	return ok
}
