package cli

// vServer paid writes: doc notes for compute's and volume's paid create,
// delete, and lifecycle commands, kept apart from gendocs_notes.go so
// neither file grows past the length limit.

// vserverDriftNote warns that a write outside the tool that provisioned a
// resource drifts its tracked state: the flag table shows only IDs and
// sizes, with no hint that a server or volume might be managed elsewhere.
const vserverDriftNote = "If this resource is managed by OpenTofu or Terraform, a write made here drifts " +
	"from that tracked state; keep such a resource's writes in the tool that manages it."

// volumeCreateVolumeNote documents create-volume's own price guard default,
// its duplicate-name guard, the unretried order, and the post-order wait
// bound: the flag table shows --max-price as a plain, optional float, with
// no hint that leaving it unset orders nothing at all.
const volumeCreateVolumeNote = "Orders nothing above --max-price, default 0: a bare create-volume refuses " +
	"with error code PriceAboveMax until --max-price is raised to at least the quoted price. A quote of 0 " +
	"is refused as Unpriced whatever --max-price says. Refuses, before " +
	"any request, a volume already named --name exactly. The order itself is never retried after a failure " +
	"that may have already reached the server; list volumes by name before ordering again rather than " +
	"repeating this command. Without --no-wait, waits up to 5 minutes for the new volume to reach AVAILABLE, " +
	"then prints it; a timeout, or ERROR during that wait, is NotSettled or WriteFailed, and this create must " +
	"not be repeated. --no-wait returns at once with only the new volume's UUID and Name set.\n\n" + vserverDriftNote

// volumeDeleteVolumeNote documents delete-volume's pre-delete guard, its
// wait bound, and that it destroys data: the flag table shows only
// --volume-id, with no hint of either.
const volumeDeleteVolumeNote = "Destroys the volume's data; there is no undo. Refuses, before any request, " +
	"with error code VolumeInUse when a pre-delete read shows the volume attached to a server; detach it " +
	"first. Without --no-wait, waits up to 5 minutes for the volume to reach 404 or DELETED; a timeout, or " +
	"ERROR during that wait, is NotSettled or WriteFailed, but a rerun is always safe, since this command " +
	"reads the volume first every time.\n\n" + vserverDriftNote

// computeCreateServerNote documents create-server's own price guard
// default, its duplicate-name guard, --user-data-file, the unretried order,
// and the post-order wait bound: the flag table shows --max-price as a
// plain, optional float and lists no UserData field at all, with no hint of
// any of this.
const computeCreateServerNote = "Orders nothing above --max-price, default 0: a bare create-server refuses with " +
	"error code PriceAboveMax until --max-price is raised to at least the quoted price. A quote of 0 is " +
	"refused as Unpriced whatever --max-price says. Refuses, before any " +
	"request, a server already named --name exactly. Needs at least one --security-group-id; the SDK picks no " +
	"default, so the project's own default group (open to the world on several ports) is only used when named " +
	"explicitly. Cloud-init user data comes only from --user-data-file <path>, read once at most 64 KiB: it " +
	"has no plain string flag, and an inline or file:// --cli-input-json value that sets UserData is refused " +
	"outright, since either could put a secret on argv or in a JSON file that shell history or a process " +
	"listing keeps; user data never reaches stdout, stderr, or --debug output. The order itself is never " +
	"retried after a failure that may have already reached the server; list servers by name before ordering " +
	"again rather than repeating this command. Without --no-wait, waits up to 15 minutes for the new server to " +
	"reach ACTIVE, then prints it; a timeout, or ERROR during that wait, is NotSettled or WriteFailed, and this " +
	"create must not be repeated. --no-wait returns at once with only the new server's UUID and Name set.\n\n" +
	idsForCreateServerLink + "\n\n" + vserverDriftNote

// computeDeleteServerNote documents delete-server's own volume disposition,
// its wait bound, and that it destroys the server: the flag table shows
// --delete-volumes as a plain, optional bool, with no hint of any of this.
// The boot volume always goes with the server; --delete-volumes governs
// only attached data volumes. DeletedVolumeIDs printed alongside a wait
// error is only what the delete requested, not a confirmed deletion (see
// DeleteServerOutput).
const computeDeleteServerNote = "Destroys the server; there is no undo. Without --delete-volumes, every " +
	"attached data volume stays and keeps being billed. The boot volume always goes with the server, with or " +
	"without --delete-volumes. Without --no-wait, this command " +
	"reads each kept volume back after the delete settles and prints the still-existing ones as KeptVolumeIDs, " +
	"so nothing costing money goes unnoticed; with --no-wait, KeptVolumeIDs instead names every volume the " +
	"server held before the delete, unconfirmed. With --delete-volumes, every attached volume is sent for " +
	"deletion with the server, data included, and DeletedVolumeIDs names them; a timeout or ERROR during the " +
	"wait below still prints DeletedVolumeIDs, but only as requested for deletion, not confirmed deleted. " +
	"Without --no-wait, waits up to 10 minutes for the server to reach 404 or DELETED; a timeout, or ERROR " +
	"during that wait, is NotSettled or WriteFailed, but a rerun is always safe, since this command reads the " +
	"server first every time.\n\n" + vserverDriftNote

// computeServerToggleWaitNote is the shared wait-and-repeat paragraph for
// start-server, stop-server, and reboot-server: each reads the server
// first, sends its own toggle at most once, and, without --no-wait, waits
// for it to settle. bound is the operation's own wait bound and settled is
// what it waits for, so callers of this function need write only what makes
// their own command different (whether a no-op is possible, and what
// status the toggle needs).
func computeServerToggleWaitNote(bound, settled string) string {
	return "Reads the server first and sends its own PUT at most once: a resend would act on a status " +
		"read that only grows staler. A status the server itself proves it never acted on (a 4xx) is returned " +
		"as is; any other failure after the send is NotSettled, and the recovery is to run this command again, " +
		"since it always reads first. Without --no-wait, waits up to " + bound + " for " + settled + "; " +
		"ERROR during that wait is WriteFailed, and the bound running out is NotSettled either way, a rerun is " +
		"safe.\n\n" + vserverDriftNote
}

// computeStartServerNote documents start-server's own no-op and status
// guard, then the shared toggle wait paragraph: the flag table shows only
// --server-id, with no hint that a STOPPED server is the only one this
// command actually acts on.
var computeStartServerNote = "Already ACTIVE: Changed is false and nothing is sent. Any status but STOPPED or " +
	"ACTIVE refuses with error code UnexpectedStatus, nothing sent, so a start is never sent to a server " +
	"mid-create.\n\n" + computeServerToggleWaitNote("5 minutes", "the server to reach ACTIVE")

// computeStopServerNote mirrors computeStartServerNote for stop-server,
// with the roles of ACTIVE and STOPPED reversed, plus why it needs --yes:
// the flag table cannot show that stopping a server cuts off what it runs
// and loses what it holds only in memory.
var computeStopServerNote = "Needs --yes: stopping a server cuts off what runs on it and loses what it " +
	"holds only in memory, unlike start-server, which undoes it. Already STOPPED: Changed is false and " +
	"nothing is sent. Any status but ACTIVE or STOPPED refuses with error code UnexpectedStatus, nothing " +
	"sent.\n\n" + computeServerToggleWaitNote("5 minutes", "the server to reach STOPPED")

// computeRebootServerNote documents reboot-server's own status guard, its
// need for --yes, and its own settle condition (ACTIVE read at least 10
// seconds after the reboot was sent, since an immediate read can still show
// the pre-reboot ACTIVE state), which the shared toggle wait paragraph does
// not cover. Unlike start-server and stop-server, reboot-server's own
// precondition (the server must read ACTIVE) is also what a settled reboot
// looks like, so a rerun after NotSettled cannot tell "never rebooted" from
// "already back to ACTIVE" and sends another reboot either way; the note
// says so instead of calling every rerun safe.
var computeRebootServerNote = "Needs --yes: a reboot interrupts what runs on the server. Needs the server " +
	"ACTIVE first; any other status refuses with error code UnexpectedStatus, nothing sent. Without " +
	"--no-wait, waits up to 5 minutes for a read showing ACTIVE at least 10 seconds after the reboot was " +
	"sent, since an immediate read can still show the pre-reboot ACTIVE state before REBOOTING even appears; " +
	"ERROR during that wait is WriteFailed. The bound running out is NotSettled; a rerun reads the server " +
	"first, but ACTIVE is exactly what this command's own precondition needs, so a rerun that reads ACTIVE " +
	"again reboots the server a second time rather than treating the first reboot as done, since neither read " +
	"can tell a settled reboot from a server that never left ACTIVE.\n\n" + vserverDriftNote

// computeRenameServerNote documents rename-server's own free, retryable
// shape: the flag table shows --name as a plain, required string, with no
// hint that this write, alone among this design's server writes, is not a
// paid write, needs no --yes, and takes no wait at all.
const computeRenameServerNote = "Free: no quote, no --max-price, and no wait, since the response carries the " +
	"renamed server directly. Keeps the transport's normal PUT retries.\n\n" + vserverDriftNote

// volumeAttachVolumeNote documents attach-volume's own no-op case and wait
// bound: the flag table shows only --volume-id and --server-id, with no
// hint that a volume already attached elsewhere is left to the server's own
// refusal.
const volumeAttachVolumeNote = "Already attached to --server-id: Changed is false and nothing is sent. " +
	"Attached to a different server, the PUT reaches the server, which refuses it with its own error. Keeps " +
	"the transport's normal PUT retries: a repeat is refused as already attached, never a second charge. " +
	"Without --no-wait, waits up to 5 minutes for the volume to read IN-USE with --server-id among its " +
	"attached servers; ERROR during that wait is WriteFailed, and the bound running out is NotSettled, a " +
	"rerun is safe, since this command always reads first.\n\n" + vserverDriftNote

// volumeDetachVolumeNote documents detach-volume's own no-op case, its boot
// volume and running-server guards, and why the latter needs --allow-running
// rather than only --yes: the flag table shows --allow-running as a plain,
// optional bool, with no hint that skipping it can lose data.
const volumeDetachVolumeNote = "Needs --yes: detaching a volume can lose unwritten data if it is still " +
	"mounted. Not attached to --server-id: Changed is false and nothing is sent. Otherwise always reads " +
	"--server-id next, --allow-running included, since the boot-volume guard below needs that read " +
	"regardless. Refuses, before any request, with error code BootVolume when the volume is --server-id's " +
	"own boot volume, or when that read cannot confirm --server-id's boot volume at all; a server cannot " +
	"boot without one. Refuses, before any request, with error code ServerRunning when --server-id is not " +
	"STOPPED and --allow-running is not set, since the volume may be mounted there and detaching it under a " +
	"mounted filesystem can lose unwritten data; stop the server first, or unmount it yourself and pass " +
	"--allow-running to skip only this status check. Keeps the transport's normal PUT retries: a repeat is refused as " +
	"already available, never a second charge. Without --no-wait, waits up to 5 minutes for the volume to " +
	"read AVAILABLE; ERROR during that wait is WriteFailed, and the bound running out is NotSettled, a rerun " +
	"is safe, since this command always reads first.\n\n" + vserverDriftNote

// computeResizeServerNote documents resize-server's own price guard,
// same-flavor and status guards, why it needs --yes, its wait bound, and
// its own recovery advice: the flag table shows --max-price as a plain,
// optional float with no hint that a bare resize-server, with --max-price
// left at 0, already refuses almost every real flavor change, or that a
// rerun after an ambiguous failure is unsafe.
const computeResizeServerNote = "Needs --yes: a resize restarts the server and may charge more. Sends " +
	"nothing above --max-price, default 0: a bare resize-server refuses with error code PriceAboveMax " +
	"until --max-price is raised to at least the quoted price. Refuses, before any request, with error code " +
	"InvalidUsage when --flavor-id already names the server's current flavor, and with error code " +
	"UnexpectedStatus when the server is neither ACTIVE nor STOPPED. The resize is sent at most once and " +
	"never retried after a failure that may have already reached the server; check get-server rather than " +
	"repeating this command, since a repeat risks a second charge. Without --no-wait, waits up to 15 " +
	"minutes for a read showing the new flavor with Status ACTIVE or STOPPED; ERROR during that wait is " +
	"WriteFailed, and the bound running out is NotSettled either way, check get-server rather than " +
	"repeating this command. The root disk does not grow with the flavor; use resize-volume on the " +
	"server's own BootVolumeID (see get-server) for that.\n\n" + vserverDriftNote

// computeQuoteResizeServerNote documents quote-resize-server's own price
// guard exemptions and unit, matching computeQuoteCreateServerNote's shape
// for the two fields ResizeServerInput shares with every other paid write's
// Input.
const computeQuoteResizeServerNote = "Never sends a resize: prices the flavor change ResizeServerInput " +
	"describes without sending it. OptimumPrice and every other price are VND a month. Ignores MaxPrice and " +
	"NoWait even when an inline --cli-input-json value sets them: both govern only an actual resize."

// volumeResizeVolumeNote documents resize-volume's own price guard,
// grow-only and status guards, why it needs --yes, its wait bound, and its
// own recovery advice: the flag table shows --size as a plain, required int
// with no hint that it must exceed the volume's current size, that the
// type never changes, or that a rerun after an ambiguous failure is unsafe.
const volumeResizeVolumeNote = "Needs --yes: a resize can charge more, and this design only grows a volume. " +
	"Sends nothing above --max-price, default 0: a bare resize-volume refuses with error code PriceAboveMax " +
	"until --max-price is raised to at least the quoted price. Refuses, before any request, with error code " +
	"InvalidUsage when --size is at or below the volume's current size, since shrinking would cut off the " +
	"end of the data, and with error code UnexpectedStatus when the volume is neither AVAILABLE nor " +
	"IN-USE. Resends the volume's own current volume type, so a type never changes by accident. The resize " +
	"is sent at most once and never retried after a failure that may have already reached the server; " +
	"check get-volume rather than repeating this command, since a repeat risks a second charge. Without " +
	"--no-wait, waits up to 5 minutes for a read showing the new size with Status AVAILABLE or IN-USE; " +
	"ERROR during that wait is WriteFailed, and the bound running out is NotSettled either way, check " +
	"get-volume rather than repeating this command. The filesystem inside a server that has this volume " +
	"attached must still be grown separately; this command only grows the block device.\n\n" + vserverDriftNote

// volumeQuoteResizeVolumeNote documents quote-resize-volume's own price
// guard exemptions and unit, and that it always reads the volume fresh:
// the flag table shows --max-price and --no-wait as plain, optional
// fields, with no hint that this read still reaches the network once for
// the volume's own current size and type before it ever reaches the quote.
const volumeQuoteResizeVolumeNote = "Reads the volume first, on every call, for its current size and type, " +
	"then prices the grow --size describes without sending it. OptimumPrice and every other price are VND a " +
	"month. Ignores MaxPrice and NoWait even when an inline --cli-input-json value sets them: both govern " +
	"only an actual resize."

// idsForCreateServerLink points the commands that take or find a server's
// IDs at the page that maps each create-server flag to its lookup command.
const idsForCreateServerLink = "The command that finds each ID create-server takes is on " +
	"[IDs for Create Server](IDs-for-Create-Server.md)."

// computeListFlavorsNote documents list-flavors' one-of zone rule, its
// request fan-out and cost, the exact-match --name, and the meaning of a
// row's ZoneID: the flag table shows three plain optional strings.
const computeListFlavorsNote = "Set exactly one of --flavor-zone-id and --zone-id: neither, or both, exits 2 " +
	"with the SDK's message before any request. --flavor-zone-id makes one request. --zone-id (a network zone, " +
	"from portal list-zones) first lists that zone's flavor zones, then lists the flavors of each, one at a " +
	"time: 1+N requests for N flavor zones. --name keeps only flavors whose Name equals it exactly, case " +
	"included; use --query with contains() for a partial match. Rows follow the flavor zone order, and each " +
	"row's FlavorZoneID names the flavor zone it came from. A row's ZoneID is the API's own zone identifier, " +
	"not the network zone name: filter rows by FlavorZoneID or by the zone you asked for. Sold-out flavors " +
	"stay listed with IsSoldOut true; drop them with --query \"Items[?!IsSoldOut]\".\n\n" + idsForCreateServerLink

// volumeListVolumeTypesNote documents list-volume-types' zone fan-out and
// cost, the exact-match --iops, and the meaning of a row's ZoneID: the flag
// table shows three plain optional flags.
const volumeListVolumeTypesNote = "Without a zone flag, lists the project's volume types. --volume-type-zone-id " +
	"makes one request. --zone-id (a network zone, from portal list-zones) first lists that zone's volume type " +
	"zones, then lists the types of each, one at a time: 1+M requests for M volume type zones. Setting both " +
	"zone flags exits 2 before any request. --iops keeps only types whose IOPS equals it exactly, in every " +
	"mode; 0 is no filter and a negative value exits 2. Rows follow the volume type zone order, and each " +
	"row's VolumeTypeZoneID names the volume type zone it came from. A row's ZoneID is the API's own zone " +
	"identifier, not the network zone name: filter rows by VolumeTypeZoneID or by the zone you asked " +
	"for.\n\n" + idsForCreateServerLink
