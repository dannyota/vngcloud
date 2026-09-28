package cli

// network's own DHCP options doc notes, one paragraph per operation that
// needs one beyond its kind, flags, and example; see docOpNotes in
// gendocs_notes.go, which keys every entry below by "network <op-name>".
// Kept apart from that file so neither grows past the length limit.

// networkCreateDHCPOptionsNote documents create-dhcp-options's own
// --dns-servers requirement, its reserved-name guard, and its retry advice:
// the flag table cannot show either the address rules or the reserved
// prefix or the create's own no-retry rule.
const networkCreateDHCPOptionsNote = "--dns-servers must be given at least once, each an IPv4 address; the " +
	"four-address limit stays on the server. Name must not start with dhcp-option-dns-, reserved for the set " +
	"VPC Private DNS creates; either problem is refused with InvalidUsage before any request. MTU is sent only " +
	"when set; the server's own default is 1450. Never retried after a failure that may have already reached " +
	"the server; list-dhcp-options --name and match the name exactly before creating it again rather than " +
	"retrying blind."

// networkDeleteDHCPOptionsNote documents delete-dhcp-options's pre-delete
// guard and its system-set and repeat-delete behavior: the flag table shows
// only --dhcp-options-id, with no hint of the read this command makes
// before its own DELETE.
const networkDeleteDHCPOptionsNote = "Refuses, before any request, with error code ResourceInUse, a set still " +
	"attached to any VPC, naming them; detach it from every VPC first. A set left behind unattached by a " +
	"deleted Private DNS VPC deletes like any other set. A repeat delete of an already-deleted set returns " +
	"NotFound."

// networkSetVPCDHCPOptionsNote documents set-vpc-dhcp-options's own --yes
// requirement, its DefaultResource and ResourceBusy guards, its no-op case,
// and its post-write wait: the flag table shows only --vpc-id and
// --dhcp-options-id, with no hint of any of this.
const networkSetVPCDHCPOptionsNote = "Needs --yes on every call: moving a VPC's set changes DNS for every " +
	"server already in it, which only picks up the new resolvers after its own next DHCP renew or reboot, and " +
	"clear-vpc-dhcp-options can return the VPC to no set afterward but not back to this one. Refuses, before " +
	"any request, with error code DefaultResource, a VPC whose Private DNS is enabled or whose current set " +
	"already is one Private DNS created, or a target set that is itself one Private DNS created: replacing it " +
	"would cut every server in the VPC off from its private zone lookups, and there is no call to put it back. " +
	"Refuses, before any request, with error code ResourceBusy, a target set that is not yet ACTIVE. Setting " +
	"the VPC's current set again is a no-op: Changed is false and nothing is sent, even though --yes is still " +
	"required. Once the PATCH is sent, waits up to 60 seconds for a read to show the new set: a VPC that " +
	"reaches ERROR is WriteFailed, and the wait running out is NotSettled, but either way the PATCH already " +
	"landed and is safe to send again with the same --dhcp-options-id."

// networkClearVPCDHCPOptionsNote documents clear-vpc-dhcp-options's own
// --yes requirement, its DefaultResource guard, its no-op case, its
// post-write wait, and that it does not restore a VPC's previous set: the
// flag table shows only --vpc-id, with no hint of any of this.
const networkClearVPCDHCPOptionsNote = "Needs --yes on every call: clearing a VPC's set changes DNS for every " +
	"server already in it, which only picks up the change after its own next DHCP renew or reboot, and the API " +
	"gives no call that restores the set or the resolvers the VPC had before. Refuses, before any request, " +
	"with error code DefaultResource, a VPC whose Private DNS is enabled or whose current set already is one " +
	"Private DNS created: clearing it would cut every server in the VPC off from its private zone lookups, and " +
	"there is no call to put it back. Clearing a VPC that already has no set is a no-op: Changed is false and " +
	"nothing is sent, even though --yes is still required. Once the PATCH is sent, waits up to 60 seconds for " +
	"a read to show an empty set: a VPC that reaches ERROR is WriteFailed, and the wait running out is " +
	"NotSettled, but either way the PATCH already landed and is safe to send again. To restore a VPC's default " +
	"resolvers, create-dhcp-options a set with the region's documented defaults and set-vpc-dhcp-options it " +
	"onto the VPC; there is no command that restores them directly."
