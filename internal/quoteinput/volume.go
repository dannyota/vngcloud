//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package quoteinput

import "danny.vn/vngcloud/internal/core"

type Volume struct {
	ZoneID, VolumeTypeID string
	Size                 int
	EncryptionTypeID     string
}

type volumeBody struct {
	Size           int    `json:"size"`
	VolumeTypeID   string `json:"volumeTypeId"`
	ZoneID         string `json:"zoneId"`
	EncryptionType string `json:"encryptionType,omitempty"`
}

func VolumeInfo(in Volume) (map[string]any, error) {
	return core.QuoteResourceInfo(volumeBody{Size: in.Size, VolumeTypeID: in.VolumeTypeID, ZoneID: in.ZoneID, EncryptionType: in.EncryptionTypeID})
}
