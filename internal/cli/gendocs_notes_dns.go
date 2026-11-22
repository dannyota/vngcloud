package cli

const dnsRecordPagingNote = "Returns page metadata, but record continuation parameters remain unverified. " +
	"A response reporting more pages returns partial output with an explicit incomplete-list error. " +
	"Never treat that output as a complete inventory."

const dnsZoneReplacementNote = "Reads the requested zone before replacing its description and VPC list. " +
	"The snapshot must explicitly contain the matching ID, status, description, and a non-null VPC list. " +
	"Missing or conflicting evidence refuses the PUT. An explicit empty VPC list remains valid. Sends the " +
	"replacement once; uncertain outcomes retain partial output with NotSettled. Read and reconcile the " +
	"zone before another write."

const dnsWaitEvidenceNote = "Polling requests and sleeps share the earlier caller or 60-second operation " +
	"deadline. Late responses cannot confirm success, and confirmation must identify the requested resource. " +
	"TXT payloads compare exactly, so old and OLD never confirm the same update."
