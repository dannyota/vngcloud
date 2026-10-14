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
	"an automatic allowance. Email quota reads, quotes, and orders are available; SMS quota operations refuse locally."

const monitorSyntheticQuotaNote = "Reads quota identity, status, expiry, AutoRenew, and package detail. " +
	"Table/text timestamps show UTC and absent prices show unknown; JSON preserves SDK values. " +
	"Synthetic quota expiry stops checks. A missing quota is NotFound; inspect it in the console. " +
	"Renewal, deletion, and auto-renew changes are not yet available."

const monitorSyntheticUsageNote = "Reads synthetic usage counters and their descriptions. " +
	"Alert emails consume the Email quota, rather than the synthetic email counter. See Monitor Quotas."

func init() {
	docExampleOverride["monitor create-notification-quota"] = "vngcloud monitor quote-create-notification-quota --kind Email"
	docExampleOverride["monitor quote-create-notification-quota"] = "vngcloud monitor quote-create-notification-quota --kind Email"
	docExampleOverride["monitor get-notification-quota"] = "vngcloud monitor get-notification-quota --kind Email --query Quota"
	docExampleOverride["monitor get-notification-quota-usage"] = "vngcloud monitor get-notification-quota-usage --kind Email --query Usage"

	docOpNotes["monitor get-notification-quota"] = "--kind accepts Email, case insensitively. " +
		"Reads quota identity, status, expiry, and AutoRenew. Table/text timestamps show UTC. " +
		"A missing quota is NotFound (exit 4). SMS refuses locally before requests."
	docOpNotes["monitor get-notification-quota-usage"] = "--kind accepts Email, case insensitively. " +
		"Reads message consumption and descriptions. A missing quota is NotFound (exit 4). " +
		"SMS refuses locally before requests."
	docOpNotes["monitor quote-create-notification-quota"] = "Estimates one month of Email capacity without ordering. " +
		"An omitted --package-id selects the unique zero-price package. Refuses an existing quota. " +
		"Ignores MaxPrice and NoWait supplied through JSON; their flags are hidden. " +
		"Prices are pre-VAT in VND; GuardPrice is the higher of list and quote prices. " +
		"A quote never authorizes a purchase. SMS refuses locally."
	docOpNotes["monitor create-notification-quota"] = "Orders one month of Email capacity with pay: true. " +
		"Consent is finite nonnegative --max-price, default 0, without --yes. " +
		"An omitted --package-id selects the unique zero-price package, emailfree (20 emails a month). " +
		"MaxPrice is a local guard on the higher list/quote price, not a server cap. " +
		"Quotes are pre-VAT; actual debit can exceed them. A positive budget prints a stderr warning. " +
		"Read-only refuses before requests. --no-wait returns an unconfirmed Output with Quota nil. " +
		"An uncertain result prints partial Output even with an error, ignoring --query. " +
		"Serialize writes across processes per account/kind; reconcile with read-only quota reads and console billing. " +
		"A stale 404 never authorizes a retry. Monthly action accounting is unknown. " +
		"SMS refuses locally; renewal arrives in a later release. See Monitor Quotas."
}
