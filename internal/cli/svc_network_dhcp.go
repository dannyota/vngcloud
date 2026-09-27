package cli

import (
	"github.com/spf13/cobra"
)

// requireYesToSetVPCDHCPOptions is set-vpc-dhcp-options's Guard: it refuses
// the command outright unless --yes is given, whatever the merged Input
// holds, the same shape requireYesToChangeRoutes (svc_network_write.go) uses
// for add-route and remove-route. Per the design, this command needs --yes
// on every call, even one that turns out to be a no-op because the VPC
// already has the target set: the API has no call that returns a VPC to no
// DHCP options set at all, and once the write lands, every server in the
// VPC picks up the new resolvers after its own next DHCP renew or reboot.
func requireYesToSetVPCDHCPOptions(cmd *cobra.Command, _ any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"network set-vpc-dhcp-options moves the VPC to a DHCP options set with no call back to none, and changes DNS for every server in it; pass --yes to confirm")
}
