//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package quoteinput

import "danny.vn/vngcloud/internal/core"

type Server struct {
	ZoneID, FlavorID, ImageID                string
	RootDiskSize                             int
	RootDiskTypeID, RootDiskEncryptionTypeID string
	DataDiskSize                             int
	DataDiskTypeID, DataDiskEncryptionTypeID string
}

type serverBody struct {
	ZoneID           string `json:"zoneId"`
	FlavorID         string `json:"flavorId"`
	ImageID          string `json:"imageId"`
	RootDiskSize     int    `json:"rootDiskSize"`
	RootDiskTypeID   string `json:"rootDiskTypeId"`
	EncryptionVolume bool   `json:"encryptionVolume"`
	DataDiskSize     int    `json:"dataDiskSize,omitempty"`
	DataDiskTypeID   string `json:"dataDiskTypeId,omitempty"`
}

func ServerInfo(in Server) (map[string]any, error) {
	return core.QuoteResourceInfo(serverBody{
		ZoneID: in.ZoneID, FlavorID: in.FlavorID, ImageID: in.ImageID,
		RootDiskSize: in.RootDiskSize, RootDiskTypeID: in.RootDiskTypeID,
		EncryptionVolume: in.RootDiskEncryptionTypeID != "" || in.DataDiskEncryptionTypeID != "",
		DataDiskSize:     in.DataDiskSize, DataDiskTypeID: in.DataDiskTypeID,
	})
}
