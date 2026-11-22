package cli

func init() {
	docOpNotes["network list-all-security-group-rules"] += `

Walks every parent page, up to 50 pages and 500000 groups. Repeated IDs or
pages, changing totals, and incomplete inventories return InvalidResponse
without partial output.`
	docOpNotes["network list-v-network-regions"] += `

Discovery cannot choose a credential destination. Requests use the fixed
regional gateway or the caller's explicit endpoint override; region matching
uses exact identities.`
}
