package cli

// network's own network ACL doc notes, one paragraph per operation that
// needs one beyond its kind, flags, and example; see docOpNotes in
// gendocs_notes.go, which keys every entry below by "network <op-name>".
// Kept apart from that file so neither grows past the length limit.

// networkGetNetworkACLNote documents the two different field sets
// GetNetworkACL and ListNetworkACLs each fill, which the flag table cannot
// show at all: a caller reading only one of the two commands' output could
// otherwise expect a field the other never sets.
const networkGetNetworkACLNote = "Sets DefaultACL, VPCID, Rules, and SubnetIDs; list-network-acls leaves those " +
	"at their zero value and sets NetworkID and SubnetID instead, which this command leaves empty."

// networkCreateNetworkACLNote documents create-network-acl's confirmed-live
// default rules and duplicate-name status: the flag table shows only
// --vpc-id and --name, with no hint that the new ACL already carries rules
// of its own.
const networkCreateNetworkACLNote = "Confirmed live: the new ACL is already ACTIVE in the create response and " +
	"starts with an inbound and outbound pass-all rule at priority 0, ordinary and removable, plus an " +
	"inbound and outbound deny-all rule at priority 2000, server-protected. A rule is default, and never " +
	"removed by add-network-acl-rule or remove-network-acl-rule, when its priority is 2000 or above or it " +
	"decodes System true; the priority-0 pass-all rules are neither. Whether a deny rule at a caller " +
	"priority takes effect while those pass-all rules are still in the list has not been shown live; do " +
	"not rely on a deny rule alone to block traffic. A duplicate --name fails with the server's own message."

// networkDeleteNetworkACLNote documents delete-network-acl's pre-delete
// guards and its confirmed-live 500-not-404 delete status: the flag table
// shows only --network-acl-id, with no hint of either.
const networkDeleteNetworkACLNote = "Refuses, before any request, with error code DefaultResource for a " +
	"project's default ACL, and ResourceInUse when any subnet is still associated; disassociate every subnet " +
	"first. Confirmed live: a deleted ACL's own GET returns 500, not 404, so this command, and a repeat " +
	"delete, confirm through the ACL list instead of trusting that status alone; a plain 404 for an id that " +
	"was never valid still returns NotFound directly. A DELETE sent while the ACL is still settling an " +
	"earlier write gets the server's own busy 400 back, mapped to error code ResourceBusy rather than a " +
	"plain API error; nothing changed, so the command can be run again. After a subnet associate or " +
	"disassociate, a DELETE inside the ACL's own busy window (about 20-30 seconds after the change) gets " +
	"a 500 back instead; nothing changed, so the command can be run again. If a 500 is returned, the " +
	"command then checks the ACL list for up to 60 seconds and, if the ACL is still listed, exits with " +
	"that 500 as a plain API error; wait and run the command again."

// networkChangeACLRuleNote builds the shared shape of add-network-acl-rule's
// and remove-network-acl-rule's --yes requirement, pre-write wait, and
// read-merge write over an ACL's rule list, the same shape
// networkChangeRouteNote documents for routes. extra is the paragraph that
// differs between the two.
func networkChangeACLRuleNote(command, extra string) string {
	return "Needs --yes on every call: " + command + " can pass or drop traffic for every subnet this ACL " +
		"covers, and the CLI cannot tell cheaply whether the ACL is in active use. Waits for the ACL to reach " +
		"ACTIVE before sending; past that wait, error code ResourceBusy, nothing sent. Resends every rule " +
		"read, including the server's own default rules (priority 2000 or above, or a rule marked System), " +
		"which are never sent changed. " + extra + " Reads the ACL again right before " +
		"sending and refuses with ResourceBusy, nothing sent, if its rules changed since that first read; a " +
		"write that lands in the moment between this re-read and the send can still be overwritten. The " +
		"rules PUT itself is sent once and never retried: landing in the ACL's own busy window (confirmed " +
		"live, roughly 18 seconds after an earlier write) gets the server's own busy 400 back, mapped to " +
		"ResourceBusy, and changes nothing, so it can be run again; any other failure that may already have " +
		"reached the server, a 5xx, a network error, or a timeout, is NotSettled instead, and is not resent " +
		"automatically, so read the ACL first before trying again. Without --no-wait, a successful send waits " +
		"once more and confirms that a fresh read names exactly the rules just sent; a mismatch, such as from " +
		"another writer changing the ACL at the same time, is also NotSettled."
}

// networkAddNetworkACLRuleNote documents add-network-acl-rule's own no-op
// and conflict cases, which networkChangeACLRuleNote's shared text does not
// cover.
var networkAddNetworkACLRuleNote = networkChangeACLRuleNote("add-network-acl-rule",
	"For --protocol tcp or udp, needs an explicit port or range: --port-range-min and --port-range-max must "+
		"not both be left at 0, refused with InvalidUsage before any request; for every port pass "+
		"--port-range-min 0 --port-range-max 65535. Protocol ANY always requires that same full range; icmp "+
		"accepts it or 0 and 0 together, for every ICMP type. Adding a rule already present at the "+
		"same --direction and --priority with every other field equal is a no-op: Changed is false and "+
		"nothing is sent. The same --direction and --priority already there with a different field is "+
		"refused with InvalidUsage; remove-network-acl-rule the old one first.")

// networkRemoveNetworkACLRuleNote documents remove-network-acl-rule's own
// --priority requirement, missing-rule, and default-rule cases, which
// networkChangeACLRuleNote's shared text does not cover.
var networkRemoveNetworkACLRuleNote = networkChangeACLRuleNote("remove-network-acl-rule",
	"Needs --priority even to name priority 0: it carries no vngcloud:\"required\" tag on the SDK's own Input, "+
		"since 0 is also the priority of the ACL's own ordinary pass-all rule, which a caller may want to "+
		"remove, so this command requires the flag (or a --cli-input-json Priority, inline or file://) so a "+
		"caller who simply forgot it is never mistaken for one naming that rule on purpose. Removing a "+
		"--direction and --priority the ACL does not have returns NotFound, nothing sent; removing a default "+
		"rule (priority 2000 or above, or a rule marked System) is refused with DefaultResource, nothing sent.")

// networkChangeACLSubnetNote builds the shared shape of
// associate-network-acl-subnet's and disassociate-network-acl-subnet's
// --yes requirement, pre-write wait, and read-merge write over an ACL's
// subnet list. extra is the paragraph that differs between the two.
func networkChangeACLSubnetNote(command, extra string) string {
	return "Needs --yes on every call: " + command + " can change which ACL's rules apply to a subnet's " +
		"traffic at once, and the CLI cannot tell cheaply whether the ACL is in active use. Waits for the ACL " +
		"to reach ACTIVE before sending; past that wait, error code ResourceBusy, nothing sent. " + extra + " " +
		"Reads the ACL again right before sending and refuses with ResourceBusy, nothing sent, if its subnet " +
		"list changed since that first read; a write that lands in the moment between this re-read and the " +
		"send can still be overwritten. The subnets PUT itself is sent once and never retried: landing in " +
		"the ACL's own busy window gets the server's own busy 400 back, mapped to ResourceBusy, and changes " +
		"nothing, so it can be run again; any other failure that may already have reached the server, a 5xx, " +
		"a network error, or a timeout, is NotSettled instead, and is not resent automatically, so read the " +
		"ACL first before trying again. Without --no-wait, a successful send waits once more and confirms " +
		"that a fresh read names exactly the subnets just sent; a mismatch, such as from another writer " +
		"changing the ACL at the same time, is also NotSettled. Confirmed live, a successful call still " +
		"leaves the ACL busy for about 20 more seconds, and, unlike after a rules write, the ACL's own " +
		"status reads ACTIVE throughout, so nothing in a read marks the window: the very next write to this " +
		"ACL, of any kind, can still get ResourceBusy during that time, which is safe to wait out and retry. " +
		"See [Limitations](Limitations.md#a-network-acls-busy-window)."
}

// networkAssociateNetworkACLSubnetNote documents associate-network-acl-subnet's
// own move-and-no-op behavior and its pre-send subnet read, which
// networkChangeACLSubnetNote's shared text does not cover.
var networkAssociateNetworkACLSubnetNote = networkChangeACLSubnetNote("associate-network-acl-subnet",
	"A subnet belongs to at most one ACL, so associating one already associated with a different ACL moves "+
		"it there, and this ACL's rules apply to its traffic at once. Associating a subnet already in this "+
		"ACL's list is a no-op: Changed is false and nothing is sent, including no read of the subnet itself. "+
		"Otherwise reads the subnet under this ACL's own VPC first, so a subnet of a different VPC is refused "+
		"with NotFound before anything is sent. The output's PreviousNetworkACLID names the ACL the subnet "+
		"moved from, if any, read from the subnet just before the move; it is set only when Changed is true.")

// networkDisassociateNetworkACLSubnetNote documents
// disassociate-network-acl-subnet's own no-op case, which
// networkChangeACLSubnetNote's shared text does not cover.
var networkDisassociateNetworkACLSubnetNote = networkChangeACLSubnetNote("disassociate-network-acl-subnet",
	"Disassociating a subnet not in this ACL's list is a no-op: Changed is false and nothing is sent. What a "+
		"subnet falls back to once disassociated is not yet confirmed live.")

// networkDeleteSubnetNote documents delete-subnet's own network ACL guard,
// which the flag table cannot show at all: keyed to "network delete-subnet"
// in gendocs_notes.go's docOpNotes map, but kept here since it is a network
// ACL note like the others in this file.
const networkDeleteSubnetNote = "Refuses, before any request, with error code ResourceInUse when a network " +
	"ACL in the subnet's own VPC still lists it; disassociate the subnet from that ACL first. Also refuses " +
	"with error code InvalidInput if the subnet read names a VPC other than --vpc-id. After disassociating " +
	"the subnet from a network ACL, wait about 30 seconds before deleting it, since this command cannot see " +
	"the ACL's own busy window."
