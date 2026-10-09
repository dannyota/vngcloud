package cli

import (
	"github.com/spf13/cobra"
)

// requireYesToSetVPCDHCPOptions is set-vpc-dhcp-options's Guard: it refuses
// the command outright unless --yes is given, whatever the merged Input
// holds, the same shape requireYesToChangeRoutes (svc_network_write.go) uses
// for add-route and remove-route. Per the design, this command needs --yes
// on every call, even one that turns out to be a no-op because the VPC
// already has the target set: once the write lands, every server in the VPC
// picks up the new resolvers after its own next DHCP renew or reboot.
// Running the command again with the VPC's previous set id restores it
// while that set still exists; see requireYesToClearVPCDHCPOptions for
// moving the VPC to no set instead.
func requireYesToSetVPCDHCPOptions(cmd *cobra.Command, _ any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"network set-vpc-dhcp-options moves the VPC to a DHCP options set and changes DNS for every server in it; pass --yes to confirm")
}

// requireYesToClearVPCDHCPOptions is clear-vpc-dhcp-options's Guard, the
// same shape requireYesToSetVPCDHCPOptions uses: it refuses the command
// outright unless --yes is given, whatever the merged Input holds. Per the
// design, this command needs --yes on every call, even one that turns out to
// be a no-op because the VPC already has no set: once the write lands, every
// server in the VPC picks up the change after its own next DHCP renew or
// reboot. Running set-vpc-dhcp-options with the VPC's previous set id
// restores it while that set still exists.
func requireYesToClearVPCDHCPOptions(cmd *cobra.Command, _ any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"network clear-vpc-dhcp-options removes the VPC's DHCP options set and changes DNS for every server in it; pass --yes to confirm")
}
