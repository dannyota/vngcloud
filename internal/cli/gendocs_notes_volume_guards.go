package cli

const volumeDeleteEvidenceNote = "The detail UUID must match --volume-id. Deletion requires AVAILABLE " +
	"or ERROR and explicit empty serverId and serverIdList fields. Missing, null, conflicting, or unknown " +
	"state sends no DELETE. Polling requests and sleeps use the earlier caller or five-minute deadline; " +
	"late success cannot settle a write."

const volumeSnapshotInventoryNote = "Reads every volume page and every snapshot page per volume, " +
	"up to 50 pages per list. Missing lists, conflicting fields, repeated pages or IDs, unstable totals, " +
	"count mismatches, and wrong parent volumes fail without partial aggregate output."
