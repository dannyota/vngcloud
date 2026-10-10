package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/volume"
)

// QuoteCreateVolume and QuoteResizeVolume are reads under ADR 0002:
// quoting does not order resources. MaxPrice and NoWait govern writes only,
// so quote commands hide those fields. Snapshot backend and policy reads
// use the SDK's region guard and remain available to read-only profiles.
var volumeOps = []Op[volume.Client]{
	Read[volume.Client, volume.ListVolumesInput, volume.ListVolumesOutput](
		kebab("ListVolumes"), (*volume.Client).ListVolumes),
	Read[volume.Client, volume.GetVolumeInput, volume.GetVolumeOutput](
		kebab("GetVolume"), (*volume.Client).GetVolume),
	Read[volume.Client, volume.GetUnderlyingVolumeInput, volume.GetUnderlyingVolumeOutput](
		kebab("GetUnderlyingVolume"), (*volume.Client).GetUnderlyingVolume),
	Read[volume.Client, volume.ListVolumesByServerInput, volume.ListVolumesByServerOutput](
		kebab("ListVolumesByServer"), (*volume.Client).ListVolumesByServer),
	Read[volume.Client, volume.ListVolumeTypeZonesInput, volume.ListVolumeTypeZonesOutput](
		kebab("ListVolumeTypeZones"), (*volume.Client).ListVolumeTypeZones),
	Read[volume.Client, volume.ListVolumeTypesInput, volume.ListVolumeTypesOutput](
		kebab("ListVolumeTypes"), (*volume.Client).ListVolumeTypes),
	Read[volume.Client, volume.GetVolumeTypeInput, volume.GetVolumeTypeOutput](
		kebab("GetVolumeType"), (*volume.Client).GetVolumeType),
	Read[volume.Client, volume.GetDefaultVolumeTypeInput, volume.GetDefaultVolumeTypeOutput](
		kebab("GetDefaultVolumeType"), (*volume.Client).GetDefaultVolumeType),
	Read[volume.Client, volume.ListEncryptionTypesInput, volume.ListEncryptionTypesOutput](
		kebab("ListEncryptionTypes"), (*volume.Client).ListEncryptionTypes),
	Read[volume.Client, volume.ListSnapshotsInput, volume.ListSnapshotsOutput](
		kebab("ListSnapshots"), (*volume.Client).ListSnapshots),
	Read[volume.Client, volume.ListAllSnapshotsInput, volume.ListAllSnapshotsOutput](
		kebab("ListAllSnapshots"), (*volume.Client).ListAllSnapshots),
	Read[volume.Client, volume.ListSnapshotBackendsInput, volume.ListSnapshotBackendsOutput](
		kebab("ListSnapshotBackends"), (*volume.Client).ListSnapshotBackends),
	Read[volume.Client, volume.ListSnapshotPoliciesInput, volume.ListSnapshotPoliciesOutput](
		kebab("ListSnapshotPolicies"), (*volume.Client).ListSnapshotPolicies),
	Read[volume.Client, volume.CreateVolumeInput, pricing.GetQuoteOutput](
		kebab("QuoteCreateVolume"), (*volume.Client).QuoteCreateVolume,
		NoFlag("MaxPrice", "NoWait"), Optional("Name")),
	// CreateVolume is Write but not Destructive: per the paid writes design,
	// --max-price is its own consent, and the default of 0 already orders
	// nothing since the quote guard refuses any priced order above it.
	Write[volume.Client, volume.CreateVolumeInput, volume.CreateVolumeOutput](
		kebab("CreateVolume"), (*volume.Client).CreateVolume),
	Write[volume.Client, volume.DeleteVolumeInput, volume.DeleteVolumeOutput](
		kebab("DeleteVolume"), (*volume.Client).DeleteVolume, Destructive()),
	Write[volume.Client, volume.AttachVolumeInput, volume.AttachVolumeOutput](
		kebab("AttachVolume"), (*volume.Client).AttachVolume),
	Write[volume.Client, volume.DetachVolumeInput, volume.DetachVolumeOutput](
		kebab("DetachVolume"), (*volume.Client).DetachVolume, Destructive()),
	Read[volume.Client, volume.ResizeVolumeInput, pricing.GetQuoteOutput](
		kebab("QuoteResizeVolume"), (*volume.Client).QuoteResizeVolume,
		NoFlag("MaxPrice", "NoWait")),
	// ResizeVolume is Destructive per the paid writes design's --yes table,
	// unlike CreateVolume: a resize can grow a volume's cost with no
	// duplicate-name guard to make a mistaken rerun safe.
	Write[volume.Client, volume.ResizeVolumeInput, volume.ResizeVolumeOutput](
		kebab("ResizeVolume"), (*volume.Client).ResizeVolume, Destructive()),
}

func newVolumeCmd(e *env) *cobra.Command {
	return Service(e, "volume", "Block volumes, volume types, and snapshots", volume.New, volumeOps...)
}
