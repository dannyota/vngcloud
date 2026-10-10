package cli

func init() {
	docServiceIntro["backup"] = "Only `hcm-3` is supported. `--project-id` does not scope these reads.\n" +
		"Backup Center is separate from `volume` snapshots; see\n" +
		"[Backup Center](Backup.md).\n\n" +
		"Every list prints `{\"Items\": [...]}` with page metadata beside `Items`,\n" +
		"so a `--query` starts with `Items`."
	docOpNotes["backup list-policies"] = "Reads one page. `--page` defaults to 1 and `--size` defaults to 200.\n" +
		"Hourly, weekly, and monthly schedule details are not included, only their\n" +
		"enable flags."
	docExampleOverride["backup list-policies"] = "vngcloud backup list-policies \\\n" +
		"  --query 'Items[].{ID:ID,Name:Name,Default:IsDefault}' --output table"
}
