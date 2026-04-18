package core

type NetworkZone struct {
	UUID          string   `json:"uuid"`
	Name          string   `json:"name"`
	ZoneType      string   `json:"zoneType"`
	IsDefault     bool     `json:"isDefault"`
	Description   string   `json:"description"`
	IsEnabled     bool     `json:"isEnabled"`
	OpenStackZone string   `json:"openstackZone"`
	IPRanges      []string `json:"ipRanges"`
	ServerCount   int      `json:"serverCount"`
	VolumeCount   int      `json:"volumeCount"`
}
