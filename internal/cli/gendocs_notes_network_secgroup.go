package cli

// network's own security group doc notes, one paragraph per operation that
// needs one beyond its kind, flags, and example; see docOpNotes in
// gendocs_notes.go, which keys every entry below by "network <op-name>".
// Kept apart from that file so neither grows past the length limit.

// networkCreateSecurityGroupNote documents create-security-group's
// confirmed-live wait behavior and its duplicate-name status: the flag
// table shows only --name and --description, with no hint that the wait
// this command runs afterward never really polls in practice.
const networkCreateSecurityGroupNote = "Confirmed live: the new group is already ACTIVE in the create " +
	"response itself, so the wait this command runs afterward settles on its first read. A duplicate " +
	"--name fails with the server's own message at status 400."

// networkCreateSecurityGroupRuleNote documents create-security-group-rule's
// confirmed-live no-wait behavior, its duplicate and overlap statuses, that
// a prefix with host bits set is stored exactly as given rather than masked
// to its network address, and the CLI's own world-open guard, which the
// flag table cannot show since --direction and --remote-ip-prefix are each
// listed as a plain, unconditional string.
const networkCreateSecurityGroupRuleNote = "Confirmed live: the new rule is already ACTIVE in the create " +
	"response, so this command takes no wait. A rule that exactly duplicates an existing one fails with " +
	"status 409; a rule that overlaps an existing one without duplicating it fails with status 400. " +
	"--remote-ip-prefix is stored exactly as sent, host bits included: 203.0.113.5/24 is not masked to " +
	"203.0.113.0/24. A rule whose --direction is not egress and whose --remote-ip-prefix has prefix length " +
	"0, such as 0.0.0.0/0 or ::/0, needs --yes: it opens every port the rule names to the entire internet."

// networkDeleteSecurityGroupNote documents delete-security-group's pre-read
// guards and its unverified in-use status: the flag table shows only
// --security-group-id, with no hint of the reads this command makes before
// its own DELETE.
const networkDeleteSecurityGroupNote = "Refuses, before any write, a system group or a group with any " +
	"server attached. A repeat delete of an already-deleted group returns NotFound. The status of a delete " +
	"the server itself refuses as in use for some other reason has not been confirmed live."

// networkDeleteSecurityGroupRuleNote documents delete-security-group-rule's
// pre-read guard and repeat-delete status: the flag table shows only the
// two IDs, with no hint that this command lists the group's rules first.
const networkDeleteSecurityGroupRuleNote = "Refuses, before any write, a rule that does not belong to " +
	"the named group. A repeat delete of an already-deleted rule also returns NotFound."
