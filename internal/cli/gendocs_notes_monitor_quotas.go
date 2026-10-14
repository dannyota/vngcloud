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
	"an automatic allowance. Ordering notification quotas is not yet available."

const monitorSyntheticQuotaNote = "Reads quota identity, status, expiry, AutoRenew, and package detail. " +
	"Table/text timestamps show UTC and absent prices show unknown; JSON preserves SDK values. " +
	"Synthetic quota expiry stops checks. A missing quota is NotFound; inspect it in the console. " +
	"Renewal, deletion, and auto-renew changes are not yet available."

const monitorSyntheticUsageNote = "Reads synthetic usage counters and their descriptions. " +
	"Alert emails consume the Email quota, rather than the synthetic email counter. See Monitor Quotas."
