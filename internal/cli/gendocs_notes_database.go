package cli

func init() {
	docExampleOverride["database create-database-instance"] = "vngcloud database create-database-instance --name <name> --zone-id <zone-id> --flavor-id <flavor-id> --storage-type-id <storage-type> --storage-size <gb> --vpc-id <vpc-id> --subnet-id <subnet-id> --database-name <database> --username <user> --password-file <path> --yes"
	docOpNotes["database create-database-instance"] = "Requires --yes. Creates one private, single-node MySQL 8.0 instance with automatic backup off, paid from credits through IAM Auto Payment. " +
		"The `monthlyCost` of a flavor from `list-database-flavors` is a catalog estimate, not a quote; no quote API is available, so no price is checked before the charge. " +
		"The instance name is 1 to 20 letters, digits, hyphens, or underscores, starting with a letter or digit. A 400 reply names only the rejected input fields, as `invalid input rejected by the API: Name, Password`, with fixed text and no provider message or value. Choose every ID from the matching `list-database-*` catalog for one zone. " +
		"The master password must be 8 to 32 characters of letters, digits, and `$ ^ _ < >`, start with a letter, and end with a letter or digit; it is checked before any request. It comes only from `--password-file`, a private file or `-` for stdin, and never from argv, JSON input, or the environment. " +
		"The create is sent once and never resent. It waits up to 30 minutes for ACTIVE, treating a 404 right after the create as not yet visible. " +
		"On NotSettled or WriteFailed the partial output on stdout holds only the instance ID, when one is known; inspect inventory and billing before another mutation. Deleting does not promise a refund."
	docOpNotes["database delete-database-instance"] = "Requires --yes. Refuses, sending nothing, unless the instance is an ACTIVE MySQL instance that is not shared, is in the selected project and a database zone of this region, and has no backups and no replicas. " +
		"Sends no final backup and no backup purge, and waits up to 30 minutes for confirmed absence; `Deleted` is true only after that. Deleting does not promise a refund. " +
		"On NotSettled the partial output holds the instance ID; inspect inventory and billing before another mutation."
}
