package cli

// docOpNotes and the example-building tables below hold plain data: one
// paragraph or example per operation that needs it, kept apart from
// gendocs.go's rendering logic so that file stays under the length limit.

// monitorChannelRedactionNote documents the CLI's channel redaction rule,
// shared by list-channels and get-channel since both decode the same
// Channel shape through redactChannel before the output ever reaches
// renderOutput.
const monitorChannelRedactionNote = "Redacts every header value and every Address except an Email, SMS, " +
	"or Telegram channel's, since a Webhook, Slack, or other channel's Address can carry a bearer token; " +
	"only Email, SMS, and Telegram addresses print in full."

// monitorCreateChannelAddressNote documents create-channel's literal
// --address guard: the flag table shows --address as a plain, required
// string flag, which would otherwise read as safe to give literally, and an
// inline --cli-input-json value that sets Address or Headers is refused the
// same way even though the flag table cannot show it at all.
const monitorCreateChannelAddressNote = "Refuses a literal --address, or an inline --cli-input-json value that " +
	"sets Address, for every Type except Email, SMS, or Telegram, with exit code 2, since another type's " +
	"address can carry a bearer token. Refuses an inline --cli-input-json value that sets Headers for every " +
	"Type, since Headers has no flag of its own and a Webhook channel's header value can hold a secret. Pass " +
	"Address (and Headers) only through --cli-input-json file://channel.json. The write's own Output is " +
	"redacted the same way a channel read is."

// monitorUpdateChannelAddressNote documents update-channel's literal
// --address guard, which is unconditional: UpdateChannelInput carries no
// Type field, so the CLI cannot tell a Webhook or Slack channel apart from
// an Email, SMS, or Telegram one without a request of its own. An inline
// --cli-input-json value that sets Address or Headers is refused the same
// way, even though the flag table cannot show Headers at all.
const monitorUpdateChannelAddressNote = "Refuses every literal --address, or an inline --cli-input-json value " +
	"that sets Address or Headers, with exit code 2, since this Input carries no Type for the guard to check. " +
	"Pass Address (and Headers) only through --cli-input-json file://channel.json. The write's own Output is " +
	"redacted the same way a channel read is."

// monitorChannelOTPFlowNote documents the --otp-ref and --otp flags
// create-channel and update-channel both take: the flag table shows each
// as a plain string, with no hint that they come from send-channel-otp and
// a person reading the address, or that a wrong one changes the error
// class and sends nothing.
const monitorChannelOTPFlowNote = "Email, Slack, SMS, and Telegram need an OTP to create or to change Address: " +
	"run send-channel-otp first, then pass its Ref as --otp-ref and the code read from the address as --otp. " +
	"A wrong or expired OTP exits with error code OTPRejected and sends no create or update; the validate " +
	"step, like the create or update itself, is never retried after an ambiguous failure. Webhook needs " +
	"neither flag."

// monitorSendChannelOTPNote documents send-channel-otp's own guard, its
// cost, and how its Output feeds create-channel and update-channel: the
// flag table shows Type and Address as plain strings and cannot show any
// of this.
const monitorSendChannelOTPNote = "Refuses Type Webhook before any request; Webhook needs no OTP. Refuses a " +
	"literal --address, or an inline --cli-input-json value that sets Address, for Type Slack, since a Slack " +
	"address is a webhook URL that can carry a secret; refuses an inline --cli-input-json value that sets " +
	"Headers for every Type. Prints Ref and ExpiresAt: give Ref to create-channel or update-channel as " +
	"--otp-ref, with the code read from the address as --otp, before the OTP expires. Never retried after an " +
	"ambiguous failure, since a retry could message the address a second time. SMS and Email beyond the free " +
	"20 each spend a paid package, and sending this OTP counts toward it."

// monitorCheckNotificationsNote documents Notifications' own nested shape
// for create-check and update-check: the flag table shows it only as a Go
// type, monitor.CheckNotifications, with no field-level detail, since
// --cli-input-json is the only way to set it at all.
const monitorCheckNotificationsNote = "Notifications' three lists, In-alarm, Up, and Undetermined, name by " +
	"ID which channels a check alerts on each alarm transition; setting Notifications through " +
	"--cli-input-json replaces all three at once, so a partial value, such as only In-alarm, clears the " +
	"other two. Each key is the API's own wire spelling, not the Go field name (In-alarm, never InAlarm); " +
	"an unrecognized key is refused with exit code 2 before any request."

// computeGetSSHKeyNote documents that SSHKey never carries a private key,
// since the flag table gives no hint that one exists at all: only
// import-ssh-key and create-ssh-key ever see one, and only once.
const computeGetSSHKeyNote = "SSHKey never includes a private key; see import-ssh-key and create-ssh-key."

// computeImportSSHKeyPreferredNote documents the wiki's own recommendation
// for import-ssh-key, and the key type GreenNode actually accepts: the flag
// table cannot show either. Only an RSA public key is accepted live; an
// ssh-ed25519 key gets a 400 "Invalid public key" from the server, even
// though ssh-keygen happily makes one.
const computeImportSSHKeyPreferredNote = "Preferred over create-ssh-key: PublicKey is made elsewhere, for " +
	"example by ssh-keygen, so the private key never reaches GreenNode at all. Refuses a PublicKey that spans " +
	"more than one line, or that contains the text \"PRIVATE KEY\", before any request; neither error ever " +
	"quotes the value. Only an RSA public key is accepted: an ssh-ed25519 key is refused by the server with " +
	"400 \"Invalid public key\"."

// computeCreateSSHKeyNote documents create-ssh-key's --secret-file
// requirement and its cleanup-on-failure rule: the flag table shows no
// --secret-file at all, since it backs no Input field, and shows PrivateKey
// only as a plain field with no hint that it is redacted or written
// anywhere.
const computeCreateSSHKeyNote = "Prefer import-ssh-key instead: it never has GreenNode see the private key " +
	"at all. Needs --secret-file <path>: GreenNode generates the key pair here and returns the private key " +
	"once, and this command writes it only to that file, at mode 0600, never to stdout, stderr, --debug, or " +
	"an error message; the printed PrivateKey field always reads \"[redacted]\", and a SecretFile field names " +
	"the path. --secret-file must not already exist, symlink included, checked before any request. If " +
	"writing it fails after the create, the new key is deleted through the SDK and the command exits 1 with " +
	"error code SecretFileFailed; if that delete also fails, the message names the key only by its ID."

// computeDeleteSSHKeyNote documents delete-ssh-key's own unverified case:
// the flag table cannot show that a server might still reference the key.
const computeDeleteSSHKeyNote = "Deleting a key a server still uses has not been checked live: whether the " +
	"API refuses it, and what happens to the server if it does not, are both unknown."

// computeCreateServerGroupNote documents create-server-group's own
// duplicate-name status and its policy's permanence: the flag table shows
// --policy-id as a plain, required string, with no hint that
// update-server-group carries no such field at all.
const computeCreateServerGroupNote = "A duplicate --name fails with the server's own message at status 400. " +
	"The group's policy cannot change after create; update-server-group has no --policy-id flag."

// computeGetServerGroupNote documents get-server-group's own not-found
// status: the server answers a deleted or otherwise unknown group with
// status 200 and no data, not 404, and this command still reports the same
// NotFound error either way.
const computeGetServerGroupNote = "A deleted, or otherwise unknown, group ID reads as NotFound: the server " +
	"answers with status 200 and no data rather than 404, and this command reports the same NotFound error " +
	"either way."

// computeDeleteServerGroupNote documents delete-server-group's pre-delete
// guard and its unverified in-use status: the flag table shows only
// --server-group-id, with no hint of the list scan this command runs before
// its own DELETE.
const computeDeleteServerGroupNote = "Refuses, before any write, a group with any server attached, found " +
	"by a pre-delete list scan. Whether the server itself refuses a delete as in use for some other reason, " +
	"and what status that refusal carries, has not been confirmed live."

// computeListFlavorZonesNote documents that --zone-id filters client side:
// the flag table shows it as an ordinary optional string, with no hint that
// the API itself always returns every zone's flavor zones regardless.
const computeListFlavorZonesNote = "Filters client side: the API always returns every zone's flavor zones, " +
	"and --zone-id only narrows what this command then prints."

// computeQuoteCreateServerNote documents quote-create-server's own price
// guard exemptions and unit, and that the billing gateway ignores fields it
// does not price: the flag table shows every CreateServerInput field the
// same way create-server itself will, with no hint that this command never
// orders anything or that three of those fields do nothing here.
const computeQuoteCreateServerNote = "Never orders anything: prices the server CreateServerInput describes " +
	"without sending a create. OptimumPrice and every other price are VND a month, one prepaid period. " +
	"Ignores UserData, MaxPrice, and NoWait even when an inline --cli-input-json value sets them: UserData is " +
	"never sent to the quote, since it can hold secrets, and MaxPrice and NoWait govern only an actual create. " +
	"The billing gateway also ignores every key it does not price, such as Name, SecurityGroupIDs, SubnetID, " +
	"or a public IP: changing them does not change the quoted price."

// volumeQuoteCreateVolumeNote documents quote-create-volume's own price
// guard exemptions and unit, matching computeQuoteCreateServerNote's shape
// for the fields CreateVolumeInput shares with CreateServerInput.
const volumeQuoteCreateVolumeNote = "Never orders anything: prices the volume CreateVolumeInput describes " +
	"without sending a create. OptimumPrice and every other price are VND a month, one prepaid period. " +
	"Ignores MaxPrice and NoWait even when an inline --cli-input-json value sets them: both govern only an " +
	"actual create."

// volumeGetDefaultVolumeTypeNote documents --zone-id's own effect: without
// it, a disabled first zone reads as NotFound, which the flag table cannot
// show since --zone-id looks like an ordinary optional filter.
const volumeGetDefaultVolumeTypeNote = "Without --zone-id, the API looks up the region's first zone, which " +
	"can be disabled for the account and then reads as NotFound; pass an enabled zone's ID instead, found with " +
	"portal list-zones."

// portalMapRedactionNote documents the CLI's key redaction rule for
// map-backed Outputs, shared by every portal operation (portal.UserInfo,
// Zone, Quota, and TagQuota are all map[string]any) and by containerregistry
// list-repositories (Repository is map[string]any too), so every key the
// API returns reaches this rule.
const portalMapRedactionNote = "Values under a key that looks like a secret " +
	"(password, token, credential, and similar, matched after lower-casing and " +
	"stripping punctuation) print as `<redacted>`, at any depth."

// portalUserInfoNote documents get-user-info's own account-data risk beyond
// the shared map redaction rule: this command prints the caller's own
// account data, which an agent transcript that captures its output keeps
// too.
const portalUserInfoNote = "Prints account data: email, names, user ID, and cash and billing status. " +
	"It is the caller's own account, but an agent transcript that keeps this command's output keeps " +
	"that data too.\n\n" + portalMapRedactionNote

// unverifiedLiveNote builds the shared text for an output shape the live
// checks cannot confirm, because the test account holds no such resource and
// the shape comes from GreenNode's official SDK rather than a live capture.
// volume and loadbalancer notes reuse it, differing only in the resource
// named.
func unverifiedLiveNote(resource string) string {
	return "Unverified live: the test account has no " + resource +
		", so this output shape comes from GreenNode's official SDK, not a live capture."
}

// unverifiedConsoleNote is unverifiedLiveNote's variant for a shape that has
// no source in GreenNode's official SDK at all: monitor's log project and
// alarm shapes are inferred from the vMonitor console's own code and a
// third-party source (the design's Source section), never from an SDK
// GreenNode publishes.
func unverifiedConsoleNote(resource string) string {
	return "Unverified live: the test account has no " + resource +
		", so this output shape is inferred from the console's code, not a live capture."
}

// volumeShapeUnverifiedNote flags an output shape the live checks cannot
// confirm: the test account holds no volume, so nothing exercises this
// command's decoding against a real response.
var volumeShapeUnverifiedNote = unverifiedLiveNote("volume")

// loadBalancerShapeUnverifiedNote flags an output shape the live checks
// cannot confirm: the test account holds no load balancer, so nothing
// exercises this command's decoding, or any child resource's, against a
// real response.
var loadBalancerShapeUnverifiedNote = unverifiedLiveNote("load balancer")

// certificateShapeUnverifiedNote flags get-certificate's output shape: the
// test account holds no certificate, so nothing exercises its decoding
// against a real response.
var certificateShapeUnverifiedNote = unverifiedLiveNote("certificate")

// containerregistry's own doc notes (list-repositories through delete-user)
// live in gendocs_notes_containerregistry.go, kept apart from this file so
// neither grows past the length limit.

// globalLoadBalancerShapeUnverifiedNote flags an output shape the live
// checks cannot confirm: the test account holds no global load balancer, so
// nothing exercises this command's decoding against a real response.
var globalLoadBalancerShapeUnverifiedNote = unverifiedLiveNote("global load balancer")

// monitorAlarmShapeUnverifiedNote flags an output shape the live checks
// cannot confirm: the test account holds no alarm, so nothing exercises
// list-alarms' or get-alarm's decoding against a real response.
var monitorAlarmShapeUnverifiedNote = unverifiedConsoleNote("alarm")

// monitorGetAlarmUnknownIDNote documents get-alarm's own exit code for a
// missing alarm, since it differs from every other Get command's: the API
// answers an unknown ID with a 500, not a 404, so vngcloud.IsNotFound never
// matches it and the command exits 1 rather than 4.
const monitorGetAlarmUnknownIDNote = "A missing alarm exits 1, not 4: the API answers an unknown ID with a 500, " +
	"not a 404, so this command never reports the NotFound error class."

// monitorQuoteCreateLogProjectIgnoredFieldsNote documents
// quote-create-log-project's own nuance the flag table cannot show:
// CreateLogProjectInput's MaxPrice and NoWait fields are registered with
// NoFlag (see monitorOps), so they reach this command only through
// --cli-input-json, and even set there this read ignores both.
const monitorQuoteCreateLogProjectIgnoredFieldsNote = "Ignores MaxPrice and NoWait even when an inline " +
	"--cli-input-json value sets them: both govern only create-log-project's own price ceiling and wait, " +
	"never this read, which neither orders anything nor waits."

// logProjectDeleteResponseUnverifiedNote flags delete-log-project's delete
// and purge responses, neither ever captured live (see
// deleteLogProjectRequest in monitor/logprojects_write.go).
const logProjectDeleteResponseUnverifiedNote = "Unverified live: the delete and purge responses' own shape " +
	"and status have never been captured, so this command discards the response body and treats either 200 " +
	"or 204 as success."

// monitorCreateLogProjectNote documents create-log-project's price guard
// default, its MaxPrice and no-price-quote and same-name guards, the class
// read the quote and order now share, the Basic class's order quota,
// unretried order, post-order wait bound, and --no-wait's own Output shape:
// the order response is confirmed live to carry only amount, orderId, and
// paymentUrl, none of LogProject's own fields, so --no-wait can only ever
// return OrderID.
const monitorCreateLogProjectNote = "Orders nothing above --max-price, default 0: a bare " +
	"create-log-project --name <name> only orders a free class and retention option. --max-price NaN, " +
	"Inf, or negative exits 2 (InvalidUsage) before any request. The quote and the order build from one " +
	"class-list read and the same order body, so the order always prices what was just quoted; a quote " +
	"with no price, or a project already named --name, also refuses the order with InvalidUsage. The " +
	"order itself is never retried after a failure that may have already reached the server; list log " +
	"projects by name before ordering again rather than repeating this command. The Basic class allows " +
	"3 orders or recoveries a month; the next one gets 409 Conflict. Without --no-wait, waits up to 120 " +
	"seconds for the new project to reach ACTIVE, then prints it; a timeout, or any other failure during " +
	"that wait, is NotSettled, and the write must not be repeated. --no-wait returns at once with only " +
	"OrderID set, from the order response's own orderId: that response carries no project fields, so " +
	"LogProject stays at its zero value."

// monitorDeleteLogProjectNote documents --purge's second request, its
// tolerance of an already-trashed project, its NotFound when both the
// delete and the purge 404, and the post-write wait bound.
const monitorDeleteLogProjectNote = "Moves the project to trash, stopping its billing; its logs are " +
	"lost. --purge also deletes it from trash, as a second request in the same call, sent even when the " +
	"first delete 404s, since the project most likely already sits in trash from an earlier call; when " +
	"the purge 404s too, the command returns NotFound, the same as a plain delete of a project that " +
	"never existed. Without --no-wait, reads the project first as a baseline, then waits up to 60 " +
	"seconds after the delete (and purge) for a read to show the change; a timeout, or any other failure " +
	"during that wait, is NotSettled, and the write must not be repeated. With --purge, when that " +
	"baseline read itself 404s, the command skips the wait and returns once the delete and the purge " +
	"each either succeed or 404. --purge in one call has not run live; a purge sent right after a " +
	"delete returned 409 Conflict once.\n\n" + logProjectDeleteResponseUnverifiedNote

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

// networkCreateVPCNote documents that the server ignores CreateVPC's own
// zone, since CreateVPCInput carries no ZoneID field at all and a reader would
// otherwise have no way to learn that a VPC's zone is decided elsewhere.
const networkCreateVPCNote = "Takes no zone: the server ignores a VPC's zone and always places it in " +
	"the region's first zone, which can be disabled for the account. The zone that matters is a " +
	"subnet's own --zone-id."

// networkCreateSubnetNote documents create-subnet's --zone-id requirement
// and where to find a value for it, since the flag table shows --zone-id as
// a plain required string with no hint of what a valid value looks like.
const networkCreateSubnetNote = "--zone-id must name a zone enabled for the account; the SDK picks no " +
	"default, since a guess would place the subnet, and any server later created in it, in a zone " +
	"the caller did not choose. Run portal list-zones to find an enabled zone."

// networkDeleteVPCNote documents delete-vpc's own subnet-list guard and the
// server's own hold after a subnet's delete, since the flag table shows only
// --vpc-id with no hint of either.
const networkDeleteVPCNote = "Refuses, before any request, a VPC that still has a server, a volume, or " +
	"a subnet; delete the subnets first. Even once every subnet is gone, the server keeps refusing " +
	"the VPC's own delete with error code ResourceInUse for several minutes afterward; this command " +
	"reads first every time, so a rerun once that window passes is safe."

// networkEnableVPCPrivateDNSNote documents enable-vpc-private-dns's one-way
// nature, its own timing, and why it needs --yes, since the flag table
// shows only --vpc-id with no hint that this write cannot be reversed.
const networkEnableVPCPrivateDNSNote = "One-way: the API has no call that disables Private DNS again, so " +
	"this command needs --yes. Takes about 6 minutes to settle; a server already on the VPC only " +
	"picks up the new resolver after its own DHCP renew."

// networkCreateRouteTableNote documents create-route-table's confirmed-live
// timing, its duplicate-name status, and the side effect of creating the
// first table in a VPC that has no main route table yet: the flag table
// shows only --vpc-id, --name, and --no-wait, with no hint of any of this.
const networkCreateRouteTableNote = "Confirmed live: the new table reaches ACTIVE about 5 seconds after the " +
	"create response, with no routes. A duplicate --name is refused with the server's own message at status " +
	"400. If --vpc-id names a VPC with no main route table yet, the new table becomes it; see " +
	"delete-route-table for what that means for a later delete."

// networkDeleteRouteTableNote documents delete-route-table's pre-delete
// reads and guards, and its confirmed-live delete timing: the flag table
// shows only --route-table-id and --no-wait, with no hint that this command
// reads the table's VPC and every one of its subnets before its own DELETE.
const networkDeleteRouteTableNote = "Reads the table, its VPC, and every subnet of that VPC first. Refuses, " +
	"before any request, with error code ResourceInUse when a subnet names this table, and with " +
	"DefaultResource when this is the VPC's main route table and some subnet names no table at all, so it " +
	"relies on this one; a main table no subnet relies on, including one with no subnets at all, deletes " +
	"normally. Confirmed live: the DELETE settles on a 404 read about 5 seconds later. A repeat delete of an " +
	"already-deleted table also returns NotFound."

// networkChangeRouteNote documents the shared shape of add-route's and
// remove-route's --yes requirement, pre-write wait, and read-merge write:
// the flag table shows only their own fields, with no hint that either
// command reads the table, waits for it to be ACTIVE, and resends every
// route it read alongside the caller's own change. sameRouteBehavior is the
// one paragraph that differs between the two: what happens when the named
// route is already there (add-route) or already gone (remove-route).
func networkChangeRouteNote(command, sameRouteBehavior string) string {
	return "Needs --yes on every call: " + command + " changes routing for every server behind the table, and " +
		"the CLI cannot tell cheaply whether that table is in use. Waits for the table to reach ACTIVE before " +
		"sending; past that wait, error code ResourceBusy, nothing sent. Matches --destination-cidr as a " +
		"parsed prefix, not by its exact text. " + sameRouteBehavior + " Reads the table again right before " +
		"sending and refuses with ResourceBusy, nothing sent, if its routes changed since that first read; a " +
		"write that lands in the moment between this re-read and the send can still be overwritten. Without " +
		"--no-wait, waits again after sending and confirms that a fresh read names exactly the routes just " +
		"sent, which only catches a write that lands after this command's own send, not one from before it; " +
		"a mismatch there is NotSettled."
}

// networkAddRouteNote documents add-route's own no-op and conflict cases,
// which networkChangeRouteNote's shared text does not cover.
var networkAddRouteNote = networkChangeRouteNote("add-route", "Adding a route to --destination-cidr that is "+
	"already there with the same --target is a no-op: Changed is false and nothing is sent. The same "+
	"--destination-cidr with a different --target already there is refused with InvalidUsage, naming the "+
	"current target; remove-route the old one first.")

// networkRemoveRouteNote documents remove-route's own missing-route and
// ambiguous-match cases, which networkChangeRouteNote's shared text does
// not cover.
var networkRemoveRouteNote = networkChangeRouteNote("remove-route", "Removing a --destination-cidr the table "+
	"does not have returns NotFound, nothing sent. A --destination-cidr matching more than one route by its "+
	"parsed prefix is refused, nothing sent.")

// docOpNotes gives one operation a paragraph of prose beyond its kind,
// flags, and example, keyed by "service op-name". An operation goes here
// when its page needs to state a behavior the flag table cannot show, such
// as a redaction rule that changes what an otherwise plain Read command
// prints, or a guard that refuses a flag the table shows as a plain string.
var docOpNotes = map[string]string{
	"iam create-service-account":              iamCreateServiceAccountNote,
	"iam update-service-account":              iamUpdateServiceAccountNote,
	"iam reset-service-account-secret":        iamResetServiceAccountSecretNote,
	"iam delete-service-account":              iamDeleteServiceAccountNote,
	"iam create-policy":                       iamCreatePolicyNote,
	"iam update-policy":                       iamUpdatePolicyNote,
	"iam delete-policy":                       iamDeletePolicyNote,
	"iam attach-service-account-policy":       iamAttachServiceAccountPolicyNote,
	"iam detach-service-account-policy":       iamDetachServiceAccountPolicyNote,
	"iam create-group":                        iamCreateGroupNote,
	"iam update-group":                        iamUpdateGroupNote,
	"iam delete-group":                        iamDeleteGroupNote,
	"iam add-user-to-group":                   iamAddUserToGroupNote,
	"iam remove-user-from-group":              iamRemoveUserFromGroupNote,
	"iam attach-group-policy":                 iamAttachGroupPolicyNote,
	"iam detach-group-policy":                 iamDetachGroupPolicyNote,
	"iam attach-user-policy":                  iamAttachUserPolicyNote,
	"iam detach-user-policy":                  iamDetachUserPolicyNote,
	"compute get-ssh-key":                     computeGetSSHKeyNote,
	"compute import-ssh-key":                  computeImportSSHKeyPreferredNote,
	"compute create-ssh-key":                  computeCreateSSHKeyNote,
	"compute delete-ssh-key":                  computeDeleteSSHKeyNote,
	"compute get-server-group":                computeGetServerGroupNote,
	"compute create-server-group":             computeCreateServerGroupNote,
	"compute delete-server-group":             computeDeleteServerGroupNote,
	"compute list-flavor-zones":               computeListFlavorZonesNote,
	"compute quote-create-server":             computeQuoteCreateServerNote,
	"network create-security-group":           networkCreateSecurityGroupNote,
	"network create-security-group-rule":      networkCreateSecurityGroupRuleNote,
	"network delete-security-group":           networkDeleteSecurityGroupNote,
	"network delete-security-group-rule":      networkDeleteSecurityGroupRuleNote,
	"network create-vpc":                      networkCreateVPCNote,
	"network create-subnet":                   networkCreateSubnetNote,
	"network delete-vpc":                      networkDeleteVPCNote,
	"network enable-vpc-private-dns":          networkEnableVPCPrivateDNSNote,
	"network create-route-table":              networkCreateRouteTableNote,
	"network delete-route-table":              networkDeleteRouteTableNote,
	"network add-route":                       networkAddRouteNote,
	"network remove-route":                    networkRemoveRouteNote,
	"monitor list-channels":                   monitorChannelRedactionNote,
	"monitor get-channel":                     monitorChannelRedactionNote,
	"monitor send-channel-otp":                monitorSendChannelOTPNote,
	"monitor create-channel":                  monitorCreateChannelAddressNote + "\n\n" + monitorChannelOTPFlowNote,
	"monitor update-channel":                  monitorUpdateChannelAddressNote + "\n\n" + monitorChannelOTPFlowNote,
	"monitor quote-create-log-project":        monitorQuoteCreateLogProjectIgnoredFieldsNote,
	"monitor create-log-project":              monitorCreateLogProjectNote,
	"monitor delete-log-project":              monitorDeleteLogProjectNote,
	"monitor list-alarms":                     monitorAlarmShapeUnverifiedNote,
	"monitor get-alarm":                       monitorAlarmShapeUnverifiedNote + "\n\n" + monitorGetAlarmUnknownIDNote,
	"monitor create-check":                    monitorCheckNotificationsNote,
	"monitor update-check":                    monitorCheckNotificationsNote,
	"portal get-user-info":                    portalUserInfoNote,
	"portal list-zones":                       portalMapRedactionNote,
	"portal list-quota-used":                  portalMapRedactionNote,
	"portal get-quota":                        portalMapRedactionNote,
	"portal get-tag-quota":                    portalMapRedactionNote,
	"volume get-volume":                       volumeShapeUnverifiedNote,
	"volume get-underlying-volume":            volumeShapeUnverifiedNote,
	"volume list-snapshots":                   volumeShapeUnverifiedNote,
	"volume get-default-volume-type":          volumeGetDefaultVolumeTypeNote,
	"volume quote-create-volume":              volumeQuoteCreateVolumeNote,
	"loadbalancer get-load-balancer":          loadBalancerShapeUnverifiedNote,
	"loadbalancer get-certificate":            certificateShapeUnverifiedNote,
	"loadbalancer list-listeners":             loadBalancerShapeUnverifiedNote,
	"loadbalancer get-listener":               loadBalancerShapeUnverifiedNote,
	"loadbalancer list-pools":                 loadBalancerShapeUnverifiedNote,
	"loadbalancer get-pool":                   loadBalancerShapeUnverifiedNote,
	"loadbalancer get-pool-health-monitor":    loadBalancerShapeUnverifiedNote,
	"loadbalancer list-pool-members":          loadBalancerShapeUnverifiedNote,
	"loadbalancer list-policies":              loadBalancerShapeUnverifiedNote,
	"loadbalancer get-policy":                 loadBalancerShapeUnverifiedNote,
	"loadbalancer list-tags":                  loadBalancerShapeUnverifiedNote,
	"loadbalancer import-certificate":         loadbalancerImportCertificateNote,
	"loadbalancer delete-certificate":         loadbalancerDeleteCertificateNote,
	"loadbalancer quote-create-load-balancer": loadbalancerQuoteCreateLoadBalancerNote,
	"loadbalancer quote-resize-load-balancer": loadbalancerQuoteResizeLoadBalancerNote,
	"loadbalancer create-load-balancer":       loadbalancerCreateLoadBalancerNote,
	"loadbalancer delete-load-balancer":       loadbalancerDeleteLoadBalancerNote,
	"loadbalancer resize-load-balancer":       loadbalancerResizeLoadBalancerNote,
	"loadbalancer create-pool":                loadbalancerCreatePoolNote,
	"loadbalancer update-pool":                loadbalancerUpdatePoolNote,
	"loadbalancer delete-pool":                loadbalancerDeletePoolNote,
	"loadbalancer add-pool-member":            loadbalancerAddPoolMemberNote,
	"loadbalancer update-pool-member":         loadbalancerUpdatePoolMemberNote,
	"loadbalancer remove-pool-member":         loadbalancerRemovePoolMemberNote,
	"containerregistry list-repositories":     containerRegistryListRepositoriesNote,
	"containerregistry get-repository":        containerRegistryGetRepositoryNote,
	"containerregistry create-repository":     containerRegistryCreateRepositoryNote,
	"containerregistry delete-repository":     containerRegistryDeleteRepositoryNote,
	"containerregistry list-users":            containerRegistryUserNameNote,
	"containerregistry list-repository-users": containerRegistryUserNameNote,
	"containerregistry create-user":           containerRegistryCreateUserNote,
	"containerregistry delete-user":           containerRegistryDeleteUserNote,
	"globalloadbalancer get-load-balancer":    globalLoadBalancerShapeUnverifiedNote,
	"globalloadbalancer list-pools":           globalLoadBalancerShapeUnverifiedNote,
	"globalloadbalancer list-listeners":       globalLoadBalancerShapeUnverifiedNote,
	"globalloadbalancer get-listener":         globalLoadBalancerShapeUnverifiedNote,
	"globalloadbalancer list-pool-members":    globalLoadBalancerShapeUnverifiedNote,
	"globalloadbalancer get-pool-member":      globalLoadBalancerShapeUnverifiedNote,
	"globalloadbalancer list-usage-histories": globalLoadBalancerShapeUnverifiedNote + " The formats and allowed values of --from, --to, and --type are unknown; the CLI passes them through unchecked.",
}

// docJSONPlaceholders gives the JSON literal buildExample writes into
// --cli-input-json for a required Input field that has no flag (viaJSON),
// keyed by its Go field name. A required viaJSON field missing here would
// make buildExample print a command that exits 2 when run as shown, so
// buildExample panics instead of silently omitting the field.
var docJSONPlaceholders = map[string]string{
	"Locations":   `["<location-id>"]`,
	"VPCIDs":      `["<vpc-id>"]`,
	"Values":      `[{"Value":"<value>"}]`,
	"Permissions": `[{"RepositoryID":"<repository-id>","Actions":["Pull Images"]}]`,
}

// docExampleExtraFlag names one flag buildExample adds to an operation's
// example beyond its required fields, keyed by "service op-name". An
// operation goes here when none of its Input fields are marked "required"
// for this purpose, yet the operation itself rejects a call that leaves a
// whole group of fields unset: update-hosted-zone's only required field is
// HostedZoneID, but UpdateHostedZone also requires at least one of
// Description or VPCIDs, so the plain required-flags-only example would
// print a command that exits 2 with InvalidUsage when run as shown.
// update-record is the same shape: HostedZoneID and RecordID are its only
// required fields, but UpdateRecord also requires at least one other field
// to change. monitor update-check is the same shape again: CheckID is its
// only required field, but UpdateCheck also requires at least one other
// field to change. compute update-server-group is the same shape: it also
// requires Name or Description. compute create-ssh-key does not need an entry here even
// though --secret-file backs no Input field: extraDocFields (gendocs.go)
// already gives it a required docField of its own, which the same
// required-fields loop below picks up. loadbalancer's update-pool,
// update-pool-member, update-listener, and update-policy are the same shape
// again: each requires at least one field to change beyond its path IDs.
var docExampleExtraFlag = map[string]string{
	"compute update-server-group":     "name",
	"dns update-hosted-zone":          "description",
	"dns update-record":               "ttl",
	"monitor update-check":            "name",
	"loadbalancer update-pool":        "algorithm",
	"loadbalancer update-pool-member": "weight",
}

// docExampleOverride gives a full example command line for "service
// op-name", replacing buildExample's generic, per-field derivation.
// create-channel and update-channel need this: buildExample would otherwise
// print a literal --address flag, since Address is a required, flag-settable
// string field, but the CLI's own guard refuses exactly that flag for a real
// Webhook or Slack channel. The override shows the runnable form instead:
// Address (and Headers) through --cli-input-json file://channel.json.
// list-alarms needs it too: buildExample would otherwise print the
// placeholder "--kind <kind>" for its required Kind field, which is not a
// value the command accepts, so the override names a real one, Log.
// send-channel-otp is the same shape as list-alarms: Type only accepts
// Email, Slack, SMS, or Telegram, so the override names Email, matching
// the monitor design's own CLI example. network add-route and remove-route
// need this too: neither is Destructive (see networkOps in svc_network.go),
// so buildExample's own destructive-only rule would never append --yes, but
// requireYesToChangeRoutes (svc_network_write.go) refuses either command
// without it on every call.
// loadbalancer import-certificate needs it for a different reason: its
// required Certificate field is NoFlag'd (importCertificateOp,
// svc_loadbalancer_certificates.go), so buildExample's loop would otherwise
// try to build a --cli-input-json placeholder for it, which would panic
// (docJSONPlaceholders has no entry for Certificate, on purpose: PEM text
// makes a poor placeholder) and, even with one, would print a command that
// sets Certificate two contradictory ways at once. The override shows the
// one runnable form: every PEM and key field through its own file flag.
// iam create-policy needs it because Statements is required and viaJSON:
// buildExample would otherwise need a docJSONPlaceholders entry for it and
// print an unrunnable --cli-input-json blob, when --document-file is the
// command's own real, documented way to set it. iam update-policy is not
// required to have an override (Statements carries no vngcloud:"required"
// tag there), but gets one anyway so its example shows --document-file too,
// rather than leaving Statements out of the example entirely.
var docExampleOverride = map[string]string{
	"iam create-policy":                "vngcloud iam create-policy --name <name> --document-file policy.json",
	"iam update-policy":                "vngcloud iam update-policy --policy-id <policy-id> --document-file policy.json --yes",
	"monitor send-channel-otp":         "vngcloud monitor send-channel-otp --type Email --address <address>",
	"monitor create-channel":           "vngcloud monitor create-channel --name <name> --type Webhook --cli-input-json file://channel.json",
	"monitor update-channel":           "vngcloud monitor update-channel --channel-id <channel-id> --cli-input-json file://channel.json",
	"monitor list-alarms":              "vngcloud monitor list-alarms --kind Log",
	"loadbalancer list-load-balancers": "vngcloud loadbalancer list-load-balancers --query 'Items[].{ID:UUID,Name:Name,Status:DisplayStatus}'",
	"network add-route":                "vngcloud network add-route --route-table-id <route-table-id> --destination-cidr <destination-cidr> --target <target> --yes",
	"network remove-route":             "vngcloud network remove-route --route-table-id <route-table-id> --destination-cidr <destination-cidr> --yes",
	"loadbalancer import-certificate": "vngcloud loadbalancer import-certificate --name example-com " +
		"--type TLS/SSL --certificate-file cert.pem --certificate-chain-file chain.pem --private-key-file key.pem",
}
