package cli

// monitor's own log alarm doc notes, one paragraph per operation that
// needs one beyond its kind, flags, and example; see docOpNotes in
// gendocs_notes.go, which keys every entry below by "monitor <op-name>".
// Kept apart from that file so neither grows past the length limit.

// monitorCreateLogAlarmNote documents create-log-alarm's own guards and
// wait, and the create response's unverified shape: the flag table cannot
// show any of these.
const monitorCreateLogAlarmNote = "Unverified live: the create response's own shape has never been " +
	"captured, since the console ignores it; the create trusts an id at data.id or a top-level id, printed " +
	"as AlarmID, or otherwise finds the alarm by its exact --name once it settles. Refuses a NaN or " +
	"infinite --threshold-value, with InvalidUsage, before any request. It then refuses a --name a log " +
	"alarm already has, with InvalidUsage, creating nothing. --query-string and an inline or file Filter " +
	"(set only through --cli-input-json) must both be given or both left out; leaving both out sends a " +
	"match-all query. The create POST is never retried after a failure that may have already reached the " +
	"server: list-alarms by --name before creating again, rather than repeating this command. Without " +
	"--no-wait, waits up to 120 seconds, polling every 2 seconds, until the alarm reads ACTIVE; ERROR or " +
	"FAILED is WriteFailed. A timeout, or any other failure during that wait, is NotSettled, and the write must not be " +
	"repeated. --no-wait returns at once with only AlarmID set."

// monitorUpdateLogAlarmNote documents update-log-alarm's pre-request and
// post-read refusals, its merge with the read, and its wait: the flag
// table shows every field as independently optional, with no hint that an
// unset one keeps the alarm's current value rather than clearing it.
const monitorUpdateLogAlarmNote = "Refuses a NaN or infinite --threshold-value, and --query-string set " +
	"without an inline or file --cli-input-json Filter (or the reverse), both with InvalidUsage, before " +
	"any request. Reads the alarm first and refuses with InvalidUsage, sending no PUT, when its Kind is " +
	"not Log, since this command never updates a metric alarm; when its Status is anything but ACTIVE, " +
	"since the server answers 403 for about 30 seconds after a create; retry once get-alarm shows ACTIVE; or when the read is missing a field the create " +
	"body always sends (LogProjectID, ThresholdType, Condition, or a nonzero TimeFrame). Past that read, " +
	"sends every field the command line left unset back unchanged, so update-log-alarm --alarm-id <id> " +
	"--name <name> changes only the name. A set --query-string/Filter pair follows create-log-alarm's own " +
	"pairing rule; leaving both unset resends the read's own pairing unchanged, even if it was never valid " +
	"to create. A new --log-project-id is always re-read for its current name, even when it names the " +
	"same project the alarm already has. Without --no-wait, waits the same way create-log-alarm does; a " +
	"timeout, or any other failure during that wait, is NotSettled, and the write must not be repeated."

// monitorDeleteLogAlarmNote documents delete-log-alarm's pre-delete
// metric-alarm refusal, its lack of a wait unlike delete-log-project, and
// its retry behavior.
const monitorDeleteLogAlarmNote = "Reads the alarm first and refuses with InvalidUsage, deleting nothing, " +
	"when its Kind is not Log: nothing shows the server itself refuses a metric alarm's ID on this DELETE " +
	"path, so the command checks first. A 404 on that read exits the same as a 404 from the delete itself, " +
	"NotFound. Past that read, deletes the alarm and its history at once with no wait, unlike " +
	"delete-log-project. A retry that finds the alarm already gone returns NotFound, the same as a second " +
	"delete of the same --alarm-id."
