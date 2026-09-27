package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/volume"
)

// volumeOps is volume's operation table. GetDefaultVolumeType now takes an
// optional ZoneID: the live 404 the CLI reads design found came from the
// region's first zone being disabled for the account, not a broken
// endpoint, so passing an enabled zone's ID (portal list-zones finds one)
// avoids it. ListVolumesByServer and QuoteCreateVolume are the other paid
// writes design reads; QuoteCreateVolume is Read, per ADR 0002 rule 1, and
// hides MaxPrice and NoWait with NoFlag since both govern only an actual
// create, which this SDK does not send yet.
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
	Read[volume.Client, volume.CreateVolumeInput, pricing.GetQuoteOutput](
		kebab("QuoteCreateVolume"), (*volume.Client).QuoteCreateVolume,
		NoFlag("MaxPrice", "NoWait")),
	// CreateVolume is Write but not Destructive: per the paid writes design,
	// --max-price is its own consent, and the default of 0 already orders
	// nothing since the quote guard refuses any priced order above it.
	Write[volume.Client, volume.CreateVolumeInput, volume.CreateVolumeOutput](
		kebab("CreateVolume"), (*volume.Client).CreateVolume),
	Write[volume.Client, volume.DeleteVolumeInput, volume.DeleteVolumeOutput](
		kebab("DeleteVolume"), (*volume.Client).DeleteVolume, Destructive()),
}

func newVolumeCmd(e *env) *cobra.Command {
	return Service(e, "volume", "Block volumes, volume types, and snapshots", volume.New, volumeOps...)
}
