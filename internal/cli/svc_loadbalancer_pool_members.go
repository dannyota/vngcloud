package cli

import (
	"github.com/spf13/cobra"
)

// requireYesToChangePoolMembership is remove-pool-member's Guard: it refuses
// the command outright unless --yes is given, whatever the merged Input
// holds. Per the vLB writes design's CLI table, remove-pool-member needs
// --yes on every call since it stops a member taking traffic at once; it is
// not Destructive in the ADR 0002 rule 6 sense, since add-pool-member can
// restore the same member, so this Guard carries the requirement instead,
// the same shape network's requireYesToChangeRoutes gives add-route and
// remove-route.
func requireYesToChangePoolMembership(cmd *cobra.Command, _ any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError("remove-pool-member stops the member taking traffic at once; pass --yes to confirm")
}
