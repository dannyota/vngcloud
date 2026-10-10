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
}
