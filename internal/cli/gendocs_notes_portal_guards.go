package cli

func init() {
	for _, op := range []string{"portal get-user-info", "portal get-tag-quota", "portal list-zones", "portal list-quota-used", "portal get-quota"} {
		docOpNotes[op] += `

Responses are limited to 8 MiB, suppress captures, and reject reflected
credentials or conflicting known fields. Object reads reject arrays, scalars,
null, and conflicting envelopes with InvalidResponse.`
	}
}
