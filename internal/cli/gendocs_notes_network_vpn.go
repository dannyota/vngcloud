package cli

func init() {
	docOpNotes["network list-vpn-connections"] = "Reads one page in `hcm-3` or `han-1`. Read-only profiles can run this command.\n" +
		"`--page` defaults to 1 and `--size` defaults to 10. No automatic paging occurs.\n" +
		"Live multi-page behavior remains unverified. Sites and tunnels are inline.\n" +
		"Table and text output use one row per VPN with compact JSON for nested fields.\n" +
		"The example selects site and tunnel counts instead. Status describes\n" +
		"provisioning, not working VPN connectivity. Pre-shared keys are omitted.\n" +
		"Server error messages and codes are withheld. See [Network VPN](Network-VPN.md)."
	docExampleOverride["network list-vpn-connections"] = "vngcloud network list-vpn-connections --page 1 --size 10 \\\n" +
		"  --query \"Items[].{ID:UUID,Name:VPNName,Sites:length(VPNSites),\\\n" +
		"Status:Status,Tunnels:length(VPNSites[].Tunnels[])}\" --output table"
}
