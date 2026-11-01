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
	"ChannelLimits preserves null versus explicit zero; legacy integer limits collapse null to zero. Synthetic quota expiry stops checks. " +
	"A missing quota is NotFound; check monitor quota-status --kind synthetic and use monitor create-synthetic-quota for a free quota. " +
	"Auto-renew enable sends once; the free quota returns BadRequest (400, exit 1). Disable remains unavailable. Guarded free create, recovery, renewal, and destructive delete are available."

const monitorSyntheticUsageNote = "Reads synthetic usage counters and their descriptions. " +
	"Alert emails consume the Email quota, rather than the synthetic email counter. See Monitor Quotas."

func init() {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		docExampleOverride["monitor "+command] = "vngcloud monitor get-synthetic-quota"
		docOpNotes["monitor "+command] = "Requires an explicit identity guard and MaxPrice consent through flags or JSON, including zero. " +
			"Only the evidenced free Basic package is supported, with POC and auto-renew off. Detail supplies the list price. " +
			"Sends pay: true once without retries or auth resends; MaxPrice is a local guard, not a server cap. " +
			"Read-only refuses before requests. No --yes or prompt. --query is refused. " +
			"Every outcome prints Sent, Stage, Error, PreviousEndTime, Quota, OrderID, PaymentPending and the operation's confirmation boolean. " +
			"Only same-ID/package ACTIVE read-back with a later expiry confirms. --no-wait leaves an open unconfirmed attempt. " +
			"Uncertainty is NotSettled, exit 1, even over auth or NotFound. Serialize per account/kind and retain a durable pending marker. " +
			"Reconcile with reads and console billing, never blind retries. See Monitor Quotas for the outcome and exit table."
	}
	docOpNotes["monitor recover-synthetic-quota"] += " --expected-quota-id is required. Recovery requires expired IN-TRASH and starts a new term at recovery time."
	docOpNotes["monitor renew-synthetic-quota"] += " --expected-end-time is required as RFC 3339. ACTIVE future expiry is required. --max-days-left 7 avoids the visible early 409 Conflict."
	docOpNotes["monitor quote-renew-synthetic-quota"] = "Estimates one frozen month from the ACTIVE quota and identity-checked detail. Requires RFC 3339 ExpectedEndTime. Ignores MaxPrice, MaxDaysLeft, and NoWait; their flags are hidden. A quote never authorizes a write."
	docExampleOverride["monitor quote-renew-synthetic-quota"] = "vngcloud monitor get-synthetic-quota"

	docOpNotes["monitor create-synthetic-quota"] = "Requires --class-id, --package-id, --quantity, and explicit --max-price consent including zero. Only free Basic, one location, 10 API tests, and an absent quota are supported. Uses class unit price times quantity, never v1 prices. Sends one month with pay: true and buyWith {} once. MaxPrice is a local guard, not a server cap. Paid classes refuse. Every return prints Sent, Stage, Error, Quota, Created, OrderID, PaymentPending, SentAt. Only matching ACTIVE quota/detail read-back with StartTime at or after SentAt confirms. Clock skew can prevent confirmation; a concurrent matching create can satisfy it without order identity proof. --no-wait leaves an open unconfirmed attempt. --query refuses. Serialize writes and retain a durable pending marker; use read-only reconciliation after uncertainty. No raw create success body was retained."
	docOpNotes["monitor quote-create-synthetic-quota"] = "Requires --class-id, --package-id, and --quantity. Estimates the selected free Basic package for one month after exact quota absence and class/package eligibility checks. Ignores MaxPrice and NoWait; their flags are hidden. Uses class unit prices, never v1 prices. Paid classes refuse. Read-only permits this quote; it never sends an order."
	docOpNotes["monitor delete-synthetic-quota"] = syntheticDeleteEffect + " Requires --yes and --expected-quota-id. Only ACTIVE unexpired quotas with POC and auto-renew off are supported. Purge and --query refuse. Read-only refuses before requests. Every return prints Quota, Stage, Purged false, and Effect, including preflight errors. Sends the ID-less DELETE once and confirms only exact quota-route absence within 60 seconds. The target snapshot cannot prove which resource the server deleted; a replacement race remains. NotSettled retains the attempt. Never recreate checks or quota automatically; use read-only reconciliation."
	for _, command := range []string{"create-synthetic-quota", "quote-create-synthetic-quota", "delete-synthetic-quota"} {
		docExampleOverride["monitor "+command] = "vngcloud monitor list-synthetic-packages"
	}
	docOpNotes["monitor list-synthetic-packages"] = "Lists class options, class unit packages, and v1 packages separately, with their own identifiers. Class Packages shows unit API, location, tier, and monthly unit price. Options join only by exact package ID within the class. " +
		"There is no evidenced join: class option prices stay unknown even when v1 free10 costs 0 VND. " +
		"Disabled classes stay visible. Only the evidenced Basic option has auto-renew eligibility no; v1 eligibility stays unknown. " +
		"Settings periods are shown separately; known free periods are capped by the free maximum. Read-only settings allow this read."
	docOpNotes["monitor quota-status"] = "--kind optionally selects synthetic, email, or sms, case insensitively; omitted means all three. " +
		"Returns a sequential snapshot with one UTC AsOf, not permission to write. Synthetic uses only quota/detail reads, " +
		"with detail price and class and an unknown package name. Notification price is the current list price, not a historical billed price. " +
		"Missing quotas produce none rows and exit 0. Failed rows print partial output, ignore --query, and exit 1 as QuotaStatusIncomplete. " +
		"Null means unknown; false and zero remain distinct. RenewWindowOpen describes only the evidenced free Synthetic timing window. " +
		"NextSafeCommand currently uses read fallbacks; recovery and renewal require explicit guards from a fresh read. " +
		"Read-only settings allow this read."
	docExampleOverride["monitor quota-status"] = "vngcloud monitor quota-status --kind synthetic"

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
		"Purge remains unavailable. Partial output includes Stage on uncertain errors. " +
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
		"SMS orders need a listed package and sufficient --max-price. Notification renewal is unavailable. See Monitor Quotas."
}
