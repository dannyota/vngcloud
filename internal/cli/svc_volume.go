package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/volume"
)

// volumeOps is volume's operation table. GetDefaultVolumeType stays out of
// it: the live call returns 404 in both regions today, and a command that
// always exits 4 would mislead an agent, per the CLI reads design's volume
// section.
var volumeOps = []Op[volume.Client]{
	Read[volume.Client, volume.ListVolumesInput, volume.ListVolumesOutput](
		kebab("ListVolumes"), (*volume.Client).ListVolumes),
	Read[volume.Client, volume.GetVolumeInput, volume.GetVolumeOutput](
		kebab("GetVolume"), (*volume.Client).GetVolume),
	Read[volume.Client, volume.GetUnderlyingVolumeInput, volume.GetUnderlyingVolumeOutput](
		kebab("GetUnderlyingVolume"), (*volume.Client).GetUnderlyingVolume),
	Read[volume.Client, volume.ListVolumeTypeZonesInput, volume.ListVolumeTypeZonesOutput](
		kebab("ListVolumeTypeZones"), (*volume.Client).ListVolumeTypeZones),
	Read[volume.Client, volume.ListVolumeTypesInput, volume.ListVolumeTypesOutput](
		kebab("ListVolumeTypes"), (*volume.Client).ListVolumeTypes),
	Read[volume.Client, volume.GetVolumeTypeInput, volume.GetVolumeTypeOutput](
		kebab("GetVolumeType"), (*volume.Client).GetVolumeType),
	Read[volume.Client, volume.ListEncryptionTypesInput, volume.ListEncryptionTypesOutput](
		kebab("ListEncryptionTypes"), (*volume.Client).ListEncryptionTypes),
	Read[volume.Client, volume.ListSnapshotsInput, volume.ListSnapshotsOutput](
		kebab("ListSnapshots"), (*volume.Client).ListSnapshots),
	Read[volume.Client, volume.ListAllSnapshotsInput, volume.ListAllSnapshotsOutput](
		kebab("ListAllSnapshots"), (*volume.Client).ListAllSnapshots),
}

func newVolumeCmd(e *env) *cobra.Command {
	return Service(e, "volume", "Block volumes, volume types, and snapshots", volume.New, volumeOps...)
}
