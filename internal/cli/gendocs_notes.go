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
	"ambiguous failure, since a retry could message the address a second time. SMS beyond the free 20 spends " +
	"a paid package, and sending this OTP counts toward it."

// monitorCheckNotificationsNote documents Notifications' own nested shape
// for create-check and update-check: the flag table shows it only as a Go
// type, monitor.CheckNotifications, with no field-level detail, since
// --cli-input-json is the only way to set it at all.
const monitorCheckNotificationsNote = "Notifications' three lists, In-alarm, Up, and Undetermined, name by " +
	"ID which channels a check alerts on each alarm transition; setting Notifications through " +
	"--cli-input-json replaces all three at once, so a partial value, such as only In-alarm, clears the " +
	"other two. Each key is the API's own wire spelling, not the Go field name (In-alarm, never InAlarm); " +
	"an unrecognized key is refused with exit code 2 before any request."

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

// containerRegistryRepositoryUnverifiedNote flags list-repositories' output
// shape: the test account holds no repository, so the live call returns an
// empty list and nothing exercises Repository's map-backed decoding against
// a real row.
var containerRegistryRepositoryUnverifiedNote = "Unverified live: the test account has no repository, so the live call returns an empty list. Each row prints the API's own keys unchanged, and those keys have not been seen."

// containerRegistryRepositoryNote combines the unverified-live note above
// with the shared map redaction rule, since containerregistry.Repository is
// map-backed like portal's models.
var containerRegistryRepositoryNote = containerRegistryRepositoryUnverifiedNote + "\n\n" + portalMapRedactionNote

// globalLoadBalancerShapeUnverifiedNote flags an output shape the live
// checks cannot confirm: the test account holds no global load balancer, so
// nothing exercises this command's decoding against a real response.
var globalLoadBalancerShapeUnverifiedNote = unverifiedLiveNote("global load balancer")

// logProjectShapeUnverifiedNote flags get-log-project's and
// list-log-projects' output shape: the test account holds no log project,
// so nothing exercises either command's per-project field decoding against
// a real response; list-log-projects' own paging envelope is live-confirmed
// (empty), and list-log-project-classes returns a live-confirmed shape too
// (see monitor.LogProject's doc comment).
var logProjectShapeUnverifiedNote = unverifiedConsoleNote("log project")

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
	"a later release; this command neither orders anything nor waits."

// docOpNotes gives one operation a paragraph of prose beyond its kind,
// flags, and example, keyed by "service op-name". An operation goes here
// when its page needs to state a behavior the flag table cannot show, such
// as a redaction rule that changes what an otherwise plain Read command
// prints, or a guard that refuses a flag the table shows as a plain string.
var docOpNotes = map[string]string{
	"monitor list-channels":                   monitorChannelRedactionNote,
	"monitor get-channel":                     monitorChannelRedactionNote,
	"monitor send-channel-otp":                monitorSendChannelOTPNote,
	"monitor create-channel":                  monitorCreateChannelAddressNote + "\n\n" + monitorChannelOTPFlowNote,
	"monitor update-channel":                  monitorUpdateChannelAddressNote + "\n\n" + monitorChannelOTPFlowNote,
	"monitor get-log-project":                 logProjectShapeUnverifiedNote,
	"monitor list-log-projects":               logProjectShapeUnverifiedNote,
	"monitor quote-create-log-project":        monitorQuoteCreateLogProjectIgnoredFieldsNote,
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
	"containerregistry list-repositories":     containerRegistryRepositoryNote,
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
	"Locations": `["<location-id>"]`,
	"VPCIDs":    `["<vpc-id>"]`,
	"Values":    `[{"Value":"<value>"}]`,
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
// field to change.
var docExampleExtraFlag = map[string]string{
	"dns update-hosted-zone": "description",
	"dns update-record":      "ttl",
	"monitor update-check":   "name",
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
// the monitor design's own CLI example.
var docExampleOverride = map[string]string{
	"monitor send-channel-otp":         "vngcloud monitor send-channel-otp --type Email --address <address>",
	"monitor create-channel":           "vngcloud monitor create-channel --name <name> --type Webhook --cli-input-json file://channel.json",
	"monitor update-channel":           "vngcloud monitor update-channel --channel-id <channel-id> --cli-input-json file://channel.json",
	"monitor list-alarms":              "vngcloud monitor list-alarms --kind Log",
	"loadbalancer list-load-balancers": "vngcloud loadbalancer list-load-balancers --query 'Items[].{ID:UUID,Name:Name,Status:DisplayStatus}'",
}
