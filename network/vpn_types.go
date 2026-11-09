package network

import "danny.vn/vngcloud/internal/core"

// ListVPNConnectionsInput selects parameters for ListVPNConnections.
type ListVPNConnectionsInput struct {
	// Page defaults to 1 when zero and must not be negative.
	Page int
	// Size defaults to 10 when zero and must not be negative.
	Size int
}

// ListVPNConnectionsOutput holds VPNConnection items and pagination metadata.
type ListVPNConnectionsOutput = core.PagedList[VPNConnection]

// VPNConnection is VPN inventory. Status describes provisioning, not connectivity.
// See docs/design/network-services-api.md.
type VPNConnection struct {
	UUID               string     `json:"uuid"`
	VPNName            string     `json:"vpnName"`
	PackageUUID        string     `json:"packageUuid"`
	LocalNetworkCIDR   string     `json:"localNetworkCidr"`
	CreatedAt          string     `json:"createdAt"`
	Status             string     `json:"status"`
	BillingStatus      string     `json:"billingStatus"`
	ZoneUUID           string     `json:"zoneUuid"`
	LocalGatewayIP     *string    `json:"localGatewayIp"`
	VPNGatewayIP       *string    `json:"vpnGatewayIp"`
	SubnetDetailModel  VPNSubnet  `json:"subnetDetailModel"`
	VPCDetailModel     VPNVPC     `json:"vpcDetailModel"`
	ProjectDetailModel VPNProject `json:"projectDetailModel"`
	PackageModel       VPNPackage `json:"packageModel"`
	VPNSites           []VPNSite  `json:"vpnSites"`
}

// VPNSubnet holds vpn subnet data from the API.
// See docs/design/network-services-api.md.
type VPNSubnet struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	CIDR         string `json:"cidr"`
	SubnetType   string `json:"subnetType"`
	UpdatedAt    string `json:"updatedAt"`
	LastSyncTime string `json:"lastSyncTime"`
	ZoneID       string `json:"zoneId"`
}

// VPNVPC holds vpnvpc data from the API.
// See docs/design/network-services-api.md.
type VPNVPC struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	CIDR         string `json:"cidr"`
	Status       string `json:"status"`
	RegionID     string `json:"regionId"`
	ProjectID    string `json:"projectId"`
	LastSyncTime string `json:"lastSyncTime"`
	DNSStatus    string `json:"dnsStatus"`
}

// VPNProject holds vpn project data from the API.
// See docs/design/network-services-api.md.
type VPNProject struct {
	ID               string `json:"id"`
	BackendProjectID string `json:"backendProjectId"`
	VServerProjectID string `json:"vserverProjectId"`
}

// VPNPackage holds vpn package data from the API.
// See docs/design/network-services-api.md.
type VPNPackage struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	PackageID   string `json:"packageId"`
	TunnelLimit int    `json:"tunnelLimit"`
	Default     bool   `json:"default"`
}

// VPNSite holds vpn site data from the API.
// See docs/design/network-services-api.md.
type VPNSite struct {
	RemoteGatewayIP string            `json:"remoteGatewayIp"`
	UUID            string            `json:"uuid"`
	Status          string            `json:"status"`
	SiteName        string            `json:"siteName"`
	CreatedAt       string            `json:"createdAt"`
	Phase1Configs   []VPNPhase1Config `json:"phase1Configs"`
	Tunnels         []VPNTunnel       `json:"tunnels"`
}

// VPNPhase1Config holds vpn phase1 config data from the API.
// See docs/design/network-services-api.md.
type VPNPhase1Config struct {
	Phase1Algorithm   string `json:"phase1Algorithm"`
	Phase1Hash        string `json:"phase1Hash"`
	Phase1DHGroup     string `json:"phase1DhGroup"`
	Phase1IKELifetime string `json:"phase1IkeLifeTime"`
}

// VPNTunnel holds vpn tunnel data from the API.
// See docs/design/network-services-api.md.
type VPNTunnel struct {
	SiteUUID          string            `json:"siteUuid"`
	TunnelName        string            `json:"tunnelName"`
	RemoteNetworkCIDR string            `json:"remoteNetworkCidr"`
	UUID              string            `json:"uuid"`
	Status            string            `json:"status"`
	CreatedAt         string            `json:"createdAt"`
	Phase2Configs     []VPNPhase2Config `json:"phase2Configs"`
}

// VPNPhase2Config holds vpn phase2 config data from the API.
// See docs/design/network-services-api.md.
type VPNPhase2Config struct {
	Phase2Algorithm   string `json:"phase2Algorithm"`
	Phase2Hash        string `json:"phase2Hash"`
	Phase2DHGroup     string `json:"phase2DhGroup"`
	Phase2IKELifetime string `json:"phase2IkeLifeTime"`
}
