package cli

func init() {
	docOpNotes["compute list-server-groups"] = `Uses --offset as the zero-based first item and --limit as the maximum item
count, default 10000. SDK and JSON input use Offset and Limit; migrate Page
and Size.`
	for _, op := range []string{"compute list-server-security-groups", "compute list-server-group-members"} {
		docOpNotes[op] = `Walks every parent page, up to 50 pages and 500000 parent rows. Repeated IDs
or pages, changing totals, and incomplete inventories return InvalidResponse
without partial output. Child collections must be explicit arrays with valid,
unique child UUIDs; missing or null arrays fail instead of implying empty.`
	}
	docOpNotes["compute reboot-server"] += `

After an uncertain result, read with compute get-server and inspect the
console. Do not repeat reboot to check whether the first reboot settled.`
}
