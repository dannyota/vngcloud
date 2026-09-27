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
