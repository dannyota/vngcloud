package cli

func init() {
	docOpNotes["network list-all-security-group-rules"] += `

Walks every parent page, up to 50 pages and 500000 groups. Repeated IDs or
pages, changing totals, and incomplete inventories return InvalidResponse
without partial output. Rule collections must be explicit arrays with valid,
unique rule IDs; missing or null arrays fail instead of implying empty.`
	for _, op := range []string{"network add-route", "network remove-route"} {
		docOpNotes[op] += `

All requests share one credential history. Responses suppress captures;
outputs and errors are checked against every observed operation credential.`
	}
	for _, op := range []string{"network delete-subnet", "network get-network-acl"} {
		docOpNotes[op] += `

ACL associations require explicit, nonempty, valid, unique subnet IDs.
Null or invalid association elements cannot authorize subnet deletion.`
	}

	docOpNotes["network list-v-network-regions"] += `

Discovery cannot choose a credential destination. Requests use the fixed
regional gateway or the caller's explicit endpoint override; region matching
uses exact identities.`
}
