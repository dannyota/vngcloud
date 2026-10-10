package cli

func init() {
	docOpNotes["network list-nat-instances"] = "Reads one page in `hcm-3` or `han-1`. Read-only profiles can run this command.\n" +
		"`--page` defaults to 1 and `--size` defaults to 10. No automatic paging occurs.\n" +
		"Live multi-page behavior remains unverified. An omitted `--zone-id` requires\n" +
		"a unique zone mapping for the selected region. Status describes provisioning.\n" +
		"See [Network NAT](Network-NAT.md) for fields and routing."
	docExampleOverride["network list-nat-instances"] = "vngcloud network list-nat-instances --page 1 --size 10 \\\n" +
		"  --query 'Items[].{ID:UUID,Name:NATName,Status:Status}' --output table"
}
