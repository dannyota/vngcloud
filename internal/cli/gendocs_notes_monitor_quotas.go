package cli

const monitorGetCheckAlarmNote = "Reads the current alarm state and its location states through the " +
	"check's HistoryAlarmID. An empty alarm reference is NotFound (exit 4). An unknown definition can " +
	"return server error 500 (exit 1), rather than NotFound. Status on a check is its ENABLED/DISABLED " +
	"run state; MonitorStatus is its alarm state."

const monitorCheckHistoryNote = "--start and --end are RFC 3339 strings. End defaults to now and Start " +
	"to 24 hours before End. Invalid, empty, or reversed ranges at millisecond precision exit 2 before " +
	"requests. --location-id selects one probe location. Items contains only returned transitions: " +
	"paging and truncation are unknown, and no state at the window start is supplied. Empty Items " +
	"proves neither that no transition occurred nor that an alarm exists. Use get-check-alarm for current state."

const monitorQuotaUsageNote = "Reads composite usage and freeEmailQuota/freeSmsQuota. UnmodeledKinds " +
	"names nonempty usage arrays whose item shapes are unknown; they contribute no invented totals. " +
	"Alert emails consume Email quota usage, while the synthetic email counter can stay zero. " +
	"Allowances depend on the account and package. See Monitor Quotas."

const monitorNotificationPackagesNote = "--kind accepts Email or SMS, case insensitively. Prices are " +
	"in VND; absent prices print unknown in table/text and null in JSON. A zero-price package is not " +
	"an automatic allowance. Email and SMS quota reads, quotes, and orders are available; SMS usage refuses locally."

const monitorSyntheticQuotaNote = "Reads quota identity, status, expiry, AutoRenew, and package detail. " +
	"Table/text timestamps show UTC and absent prices show unknown; JSON preserves SDK values. " +
	"Synthetic quota expiry stops checks. A missing quota is NotFound; inspect it in the console. " +
	"Auto-renew enable sends once; the free quota returns BadRequest (400, exit 1). Disable, renewal, and deletion remain unavailable."

const monitorSyntheticUsageNote = "Reads synthetic usage counters and their descriptions. " +
	"Alert emails consume the Email quota, rather than the synthetic email counter. See Monitor Quotas."

func init() {
	for _, command := range []string{"set-notification-quota-auto-renew", "delete-notification-quota"} {
		docExampleOverride["monitor "+command] = "vngcloud monitor get-notification-quota --kind SMS"
	}
	docExampleOverride["monitor set-synthetic-quota-auto-renew"] = "vngcloud monitor get-synthetic-quota"
	docOpNotes["monitor set-notification-quota-auto-renew"] = "Disables Email or SMS auto-renew with explicit --enabled=false. " +
		"Enable refuses before requests (exit 2). --expected-quota-id binds the intended snapshot. " +
		"Read-only refuses before requests. No --yes is required. Disable needs no quote. " +
		"Partial output includes Stage on uncertain errors; reconcile with reads, never retry automatically."
	docOpNotes["monitor set-synthetic-quota-auto-renew"] = "Requires explicit --enabled=true; disable refuses before requests. " +
		"Quotes and guards enable, then sends once. The free quota returns visible BadRequest (400, exit 1). " +
		"A warning precedes the call for free or POC quotas. --expected-quota-id binds the intended snapshot. " +
		"Read-only refuses before requests. Partial output includes Stage on uncertain errors."
	docOpNotes["monitor delete-notification-quota"] = "Deletes an ACTIVE Email or SMS quota with auto-renew and POC off. " +
		"Requires --yes; read-only refuses before requests. --expected-quota-id binds the intended snapshot, " +
		"but the kind-wide server request has no conditional identity guard. Deleted quotas cannot be recovered by these commands. " +
		"Purge and synthetic delete remain unavailable. Partial output includes Stage on uncertain errors. " +
		"Reconcile with reads and console billing; never retry automatically. See Monitor Quotas."
	docExampleOverride["monitor create-notification-quota"] = "vngcloud monitor quote-create-notification-quota --kind Email"
	docExampleOverride["monitor quote-create-notification-quota"] = "vngcloud monitor quote-create-notification-quota --kind Email"
	docExampleOverride["monitor get-notification-quota"] = "vngcloud monitor get-notification-quota --kind Email --query Quota"
	docExampleOverride["monitor get-notification-quota-usage"] = "vngcloud monitor get-notification-quota-usage --kind Email --query Usage"

	docOpNotes["monitor get-notification-quota"] = "--kind accepts Email or SMS, case insensitively. " +
		"Reads quota identity, status, expiry, and AutoRenew. Table/text timestamps show UTC. " +
		"A missing quota is NotFound (exit 4)."
	docOpNotes["monitor get-notification-quota-usage"] = "--kind accepts Email, case insensitively. " +
		"Reads message consumption and descriptions. A missing quota is NotFound (exit 4). " +
		"SMS refuses locally before requests."
	docOpNotes["monitor quote-create-notification-quota"] = "Estimates one month of Email or SMS capacity without ordering. " +
		"An omitted --package-id selects the unique zero-price package. Refuses an existing quota. " +
		"Ignores MaxPrice and NoWait supplied through JSON; their flags are hidden. " +
		"Prices are in VND; Email pre-VAT treatment is inferred and SMS VAT treatment is unverified. GuardPrice is the higher of list and quote prices. " +
		"A quote never authorizes a purchase."
	docOpNotes["monitor create-notification-quota"] = "Orders one month of Email or SMS capacity with pay: true. " +
		"Consent is finite nonnegative --max-price, default 0, without --yes. " +
		"For Email, an omitted --package-id selects the unique zero-price package, emailfree (20 emails a month). " +
		"MaxPrice is a local guard on the higher list/quote price, not a server cap. " +
		"Email quotes are inferred pre-VAT; SMS VAT treatment is unverified; actual debit can exceed estimates. A positive budget prints a stderr warning. " +
		"Read-only refuses before requests. --no-wait returns an unconfirmed Output with Quota nil. " +
		"An uncertain result prints partial Output even with an error, ignoring --query. " +
		"Serialize writes across processes per account/kind; reconcile with read-only quota reads and console billing. " +
		"A stale 404 never authorizes a retry. Monthly action accounting is unknown. " +
		"SMS orders need a listed package and sufficient --max-price. Renewal is unavailable. See Monitor Quotas."
}
