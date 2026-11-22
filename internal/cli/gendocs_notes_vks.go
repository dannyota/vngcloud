package cli

func init() {
	docServiceIntro["vks"] = "Reads Kubernetes cluster inventory, versions, and quota. " +
		"Only `hcm-3`\nand `han-1` are supported. `--project-id` does not select a VKS workspace.\n" +
		"All three commands work under `--read-only`. Output contains account data.\n" +
		"See [VKS](VKS.md) for the SDK models and scope.\n\n" +
		"Every list prints `{\"Items\": [...]}`, so a `--query` starts with `Items`."
	docOpNotes["vks list-clusters"] = "Pages start at 0, with a default size of 10. " +
		"Each call reads one page.\nRequest the next page with `--page 1`. " +
		"Use `--size` to set the page size."
	docExampleOverride["vks list-clusters"] = "vngcloud vks list-clusters \\\n  " +
		"--query 'Items[].{ID:ID,Name:Name,Status:Status,Version:Version}' \\\n  --output table"
	docOpNotes["vks create-cluster"] = "Requires --yes. There is no quote API. Worker servers and disks, the private cluster's managed load balancer, and four service endpoints incur charges. The order is prepaid for one month from credits; a one-node private cluster debited about 2,137,000 VND at create in a recorded run, and deleting it refunded the unused value. Keep that much credit available. Auto-renew is off. ACTIVE means provisioned, not workload ready. Cluster names must have 5 to 20 characters; node group names must have 5 to 15. Names use lowercase letters, digits, and hyphens, starting and ending with a letter or digit. The disk type, flavor, and subnet must be in the same zone. Use network get-subnet for ZoneID and volume get-default-volume-type --zone-id <that zone> for the disk type."
	docOpNotes["vks delete-cluster"] = "Deletion does not promise a refund."
}
