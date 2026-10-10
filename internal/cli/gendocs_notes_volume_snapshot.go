package cli

const volumeSnapshotBackendNote = "Works only in `hcm-3`. Find the snapshot backend ID with\n" +
	"`vngcloud volume list-snapshot-backends --name HCM-03`, then pass the ID\n" +
	"to `list-snapshot-policies --backend-id <id>`. Snapshot backend IDs are\n" +
	"not Backup Center IDs. Every list prints `{\"Items\": [...]}`, so a `--query`\n" +
	"starts with `Items`."

const volumeSnapshotPolicyNote = "Works only in `hcm-3`. Uses the profile's project and reads one page:\n" +
	"page 1 and size 10 by default. Weekly and monthly schedule details are not\n" +
	"included; only their enable flags are returned. Every list prints\n" +
	"`{\"Items\": [...]}`, so a `--query` starts with `Items`. Find the backend ID\n" +
	"with [list-snapshot-backends](CLI-Volume.md#list-snapshot-backends)."

func init() {
	docOpNotes["volume list-snapshot-backends"] = volumeSnapshotBackendNote
	docOpNotes["volume list-snapshot-policies"] = volumeSnapshotPolicyNote
}
