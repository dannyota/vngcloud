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
const volumeCreateVolumeNote = "Orders nothing above --max-price, default 0: a bare create-volume orders " +
	"only a free volume, and the smallest real SSD volume already prices above that, so it always refuses " +
	"with error code PriceAboveMax until --max-price is raised to at least the quoted price. Refuses, before " +
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
const computeCreateServerNote = "Orders nothing above --max-price, default 0: a bare create-server orders " +
	"only a free server, and the smallest real flavor already prices above that, so it always refuses with " +
	"error code PriceAboveMax until --max-price is raised to at least the quoted price. Refuses, before any " +
	"request, a server already named --name exactly. Needs at least one --security-group-id; the SDK picks no " +
	"default, so the project's own default group (open to the world on several ports) is only used when named " +
	"explicitly. Cloud-init user data comes only from --user-data-file <path>, read once at most 64 KiB: it " +
	"has no plain string flag, and an inline or file:// --cli-input-json value that sets UserData is refused " +
	"outright, since either could put a secret on argv or in a JSON file that shell history or a process " +
	"listing keeps; user data never reaches stdout, stderr, or --debug output. The order itself is never " +
	"retried after a failure that may have already reached the server; list servers by name before ordering " +
	"again rather than repeating this command. Without --no-wait, waits up to 15 minutes for the new server to " +
	"reach ACTIVE, then prints it; a timeout, or ERROR during that wait, is NotSettled or WriteFailed, and this " +
	"create must not be repeated. --no-wait returns at once with only the new server's UUID and Name set.\n\n" + vserverDriftNote

// computeDeleteServerNote documents delete-server's own volume disposition,
// its wait bound, and that it destroys the server: the flag table shows
// --delete-volumes as a plain, optional bool, with no hint of any of this.
const computeDeleteServerNote = "Destroys the server; there is no undo. Without --delete-volumes, every " +
	"attached volume, boot volume included, stays and keeps being billed: without --no-wait, this command " +
	"reads each one back after the delete settles and prints the still-existing ones as KeptVolumeIDs, so " +
	"nothing costing money goes unnoticed; with --no-wait, KeptVolumeIDs instead names every volume the server " +
	"held before the delete, unconfirmed. With --delete-volumes, every attached volume is destroyed with the " +
	"server, data included, and DeletedVolumeIDs names them. Without --no-wait, waits up to 10 minutes for the " +
	"server to reach 404 or DELETED; a timeout, or ERROR during that wait, is NotSettled or WriteFailed, but a " +
	"rerun is always safe, since this command reads the server first every time.\n\n" + vserverDriftNote

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
// not cover.
var computeRebootServerNote = "Needs --yes: a reboot interrupts what runs on the server. Needs the server " +
	"ACTIVE first; any other status refuses with error code UnexpectedStatus, nothing sent. Without " +
	"--no-wait, waits up to 5 minutes for a read showing ACTIVE at least 10 seconds after the reboot was " +
	"sent, since an immediate read can still show the pre-reboot ACTIVE state before REBOOTING even appears; " +
	"ERROR during that wait is WriteFailed, and the bound running out is NotSettled, a rerun is safe, since " +
	"this command always reads first.\n\n" + vserverDriftNote

// computeRenameServerNote documents rename-server's own free, retryable
// shape: the flag table shows --name as a plain, required string, with no
// hint that this write, alone among this design's server writes, is not a
// paid write, needs no --yes, and takes no wait at all.
const computeRenameServerNote = "Free: no quote, no --max-price, and no wait, since the response carries the " +
	"renamed server directly. Keeps the transport's normal PUT retries.\n\n" + vserverDriftNote
