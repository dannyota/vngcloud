package cli

import "github.com/spf13/cobra"

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

func cliInputJSONHasKey(cmd *cobra.Command, key string) bool {
	presence, _ := cmd.Context().Value(inputPresenceKey{}).(map[string]bool)
	return presence[key]
}
