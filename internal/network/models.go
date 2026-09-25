package network

import (
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// Server duplicates the public compute package's Server shape. network
// cannot import that package yet: the root package still imports this
// internal package directly, and compute imports the root package for its
// Config parameter, so importing compute here would form an import cycle.
// This copy is removed once network itself becomes a public package that
// the root package no longer imports directly.
type Server struct {
	BootVolumeID          string             `json:"bootVolumeId"`
	CreatedAt             string             `json:"createdAt"`
	Description           string             `json:"description"`
	EncryptionVolume      bool               `json:"encryptionVolume"`
	EnableLog             bool               `json:"enableLog"`
	EnableMetric          bool               `json:"enableMetric"`
	Licence               bool               `json:"licence"`
	LicenseKey            string             `json:"licenseKey"`
	Location              string             `json:"location"`
	Metadata              string             `json:"metadata"`
	MigrateState          string             `json:"migrateState"`
	MigrationStatus       string             `json:"migrationStatus"`
	Name                  string             `json:"name"`
	Product               string             `json:"product"`
	ServerGroupID         any                `json:"serverGroupId"`
	ServerGroupName       string             `json:"serverGroupName"`
	SSHKeyName            string             `json:"sshKeyName"`
	Status                string             `json:"status"`
	StopBeforeMigrate     bool               `json:"stopBeforeMigrate"`
	User                  string             `json:"user"`
	UUID                  string             `json:"uuid"`
	Image                 ServerImage        `json:"image"`
	Flavor                ServerFlavor       `json:"flavor"`
	SecurityGroups        []ServerSecgroup   `json:"secGroups"`
	ExternalInterfaces    []NetworkInterface `json:"externalInterfaces"`
	InternalInterfaces    []NetworkInterface `json:"internalInterfaces"`
	ZoneID                string             `json:"zoneId"`
	Zone                  core.NetworkZone   `json:"zone"`
	AppLicense            any                `json:"appLicense"`
	AppLicenseName        string             `json:"appLicenseName"`
	AppPackageVersionName string             `json:"appPackageVersionName"`
	DefaultTagIDs         []string           `json:"defaultTagIds"`
	FlavorZoneID          string             `json:"flavorZoneId"`
	FlavorZones           any                `json:"flavorZones"`
	GPUMemory             any                `json:"gpuMemory"`
	HostGroupID           string             `json:"hostGroupId"`
}

type NetworkInterface struct {
	CreatedAt     string `json:"createdAt"`
	FixedIP       string `json:"fixedIp"`
	FloatingIP    string `json:"floatingIp"`
	FloatingIPID  string `json:"floatingIpId"`
	InterfaceType string `json:"interfaceType"`
	MAC           string `json:"mac"`
	NetworkUUID   string `json:"networkUuid"`
	PortUUID      string `json:"portUuid"`
	Product       string `json:"product"`
	ServerUUID    string `json:"serverUuid"`
	Status        string `json:"status"`
	SubnetUUID    string `json:"subnetUuid"`
	Type          string `json:"type"`
	UpdatedAt     string `json:"updatedAt"`
	UUID          string `json:"uuid"`
}

// ServerFlavor duplicates compute.Flavor; see the comment on Server.
type ServerFlavor struct {
	Bandwidth              int64  `json:"bandwidth"`
	BandwidthUnit          string `json:"bandwidthUnit"`
	CPU                    int64  `json:"cpu"`
	CPUPlatformDescription string `json:"cpuPlatformDescription"`
	FlavorID               string `json:"flavorId"`
	GPU                    int64  `json:"gpu"`
	Group                  string `json:"group"`
	Memory                 int64  `json:"memory"`
	Metadata               string `json:"metaData"`
	Name                   string `json:"name"`
	RemainingVMs           int64  `json:"remainingVms"`
	ZoneID                 string `json:"zoneId"`
}

// ServerImage duplicates compute.Image; see the comment on Server.
type ServerImage struct {
	FlavorZoneIDs []string             `json:"flavorZoneIds"`
	ID            string               `json:"id"`
	ImageType     string               `json:"imageType"`
	ImageVersion  string               `json:"imageVersion"`
	Licence       bool                 `json:"licence"`
	PackageLimit  ServerImagePackLimit `json:"packageLimit"`
}

// ServerImagePackLimit duplicates compute.PackageLimit; see the comment on
// Server.
type ServerImagePackLimit struct {
	CPU      int64 `json:"cpu"`
	DiskSize int64 `json:"diskSize"`
	Memory   int64 `json:"memory"`
}

type ServerSecgroup struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

type listVPCsResponse struct {
	ListData  []VPC `json:"listData"`
	Page      int   `json:"page"`
	PageSize  int   `json:"pageSize"`
	TotalPage int   `json:"totalPage"`
	TotalItem int   `json:"totalItem"`
}

type listWANIPsResponse struct {
	ListData  []WANIP `json:"listData"`
	Page      int     `json:"page"`
	PageSize  int     `json:"pageSize"`
	TotalPage int     `json:"totalPage"`
	TotalItem int     `json:"totalItem"`
}

type listNetworkInterfacesResponse struct {
	ListData  []ElasticNetworkInterface `json:"listData"`
	Page      int                       `json:"page"`
	PageSize  int                       `json:"pageSize"`
	TotalPage int                       `json:"totalPage"`
	TotalItem int                       `json:"totalItem"`
}

type listSecurityGroupsResponse struct {
	ListData  []SecurityGroup `json:"listData"`
	Page      int             `json:"page"`
	PageSize  int             `json:"pageSize"`
	TotalPage int             `json:"totalPage"`
	TotalItem int             `json:"totalItem"`
}

type listVirtualIPAddressesResponse struct {
	ListData  []VirtualIPAddress `json:"listData"`
	Page      int                `json:"page"`
	PageSize  int                `json:"pageSize"`
	TotalPage int                `json:"totalPage"`
	TotalItem int                `json:"totalItem"`
}

type listRouteTablesResponse struct {
	ListData  []RouteTable `json:"listData"`
	Page      int          `json:"page"`
	PageSize  int          `json:"pageSize"`
	TotalPage int          `json:"totalPage"`
	TotalItem int          `json:"totalItem"`
}

type listPeeringsResponse struct {
	ListData  []Peering `json:"listData"`
	Page      int       `json:"page"`
	PageSize  int       `json:"pageSize"`
	TotalPage int       `json:"totalPage"`
	TotalItem int       `json:"totalItem"`
}

type listNetworkACLsResponse struct {
	ListData  []NetworkACL `json:"listData"`
	Page      int          `json:"page"`
	PageSize  int          `json:"pageSize"`
	TotalPage int          `json:"totalPage"`
	TotalItem int          `json:"totalItem"`
}

type listInterconnectsResponse struct {
	ListData  []Interconnect `json:"listData"`
	Page      int            `json:"page"`
	PageSize  int            `json:"pageSize"`
	TotalPage int            `json:"totalPage"`
	TotalItem int            `json:"totalItem"`
}

type listEndpointsResponse struct {
	Data      []NetworkEndpoint `json:"data"`
	Page      int               `json:"page"`
	Size      int               `json:"size"`
	TotalPage int               `json:"totalPage"`
	Total     int               `json:"total"`
}

type subnetResponse struct {
	UUID                   string                  `json:"uuid"`
	Status                 string                  `json:"status"`
	CIDR                   string                  `json:"cidr"`
	NetworkUUID            string                  `json:"networkUuid"`
	RouteTableUUID         string                  `json:"routeTableUuid"`
	Name                   string                  `json:"name"`
	InterfaceACLPolicyUUID string                  `json:"interfaceAclPolicyUuid"`
	InterfaceACLPolicyName string                  `json:"interfaceAclPolicyName"`
	SecondarySubnets       []SubnetSecondarySubnet `json:"secondarySubnets"`
	Zone                   struct {
		UUID string `json:"uuid"`
	} `json:"zone"`
}

func (s subnetResponse) toSubnet() Subnet {
	return Subnet{
		UUID:                   s.UUID,
		Name:                   s.Name,
		NetworkID:              s.NetworkUUID,
		CIDR:                   s.CIDR,
		Status:                 s.Status,
		InterfaceACLPolicyID:   s.InterfaceACLPolicyUUID,
		InterfaceACLPolicyName: s.InterfaceACLPolicyName,
		RouteTableID:           s.RouteTableUUID,
		ZoneID:                 s.Zone.UUID,
		SecondarySubnets:       s.SecondarySubnets,
	}
}

type VPC struct {
	UUID           string      `json:"id"`
	Status         string      `json:"status"`
	ElasticIPs     []string    `json:"elasticIps"`
	Name           string      `json:"displayName"`
	CreatedAt      string      `json:"createdAt"`
	CIDR           string      `json:"cidr"`
	DHCPOptionName string      `json:"dhcpOptionName"`
	DHCPOptionID   string      `json:"dhcpOptionId"`
	RouteTableName string      `json:"routeTableName"`
	RouteTableID   string      `json:"routeTableId"`
	Zone           NetworkZone `json:"zone"`
	DNSStatus      string      `json:"dnsStatus"`
	DNSID          string      `json:"dnsId"`
	MTU            int         `json:"mtu"`
	ServerCount    int         `json:"serverCount"`
	VolumeCount    int         `json:"volumeCount"`
}

type WANIP struct {
	UUID               string `json:"uuid"`
	ID                 string `json:"id"`
	Name               string `json:"name"`
	IPAddress          string `json:"ipAddress"`
	FloatingIP         string `json:"floatingIp"`
	NetworkInterfaceID string `json:"networkInterfaceId"`
	ServerID           string `json:"serverId"`
	Status             string `json:"status"`
	Type               string `json:"type"`
	CreatedAt          string `json:"createdAt"`
}

type ElasticNetworkInterface struct {
	UUID        string `json:"uuid"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	FixedIP     string `json:"fixedIp"`
	IPAddress   string `json:"ipAddress"`
	MAC         string `json:"mac"`
	NetworkID   string `json:"networkId"`
	NetworkUUID string `json:"networkUuid"`
	SubnetID    string `json:"subnetId"`
	SubnetUUID  string `json:"subnetUuid"`
	ServerID    string `json:"serverId"`
	ServerUUID  string `json:"serverUuid"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
}

type SecurityGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	IsSystem    bool   `json:"isSystem"`
	System      bool   `json:"system"`
}

type VirtualIPAddress struct {
	UUID            string      `json:"uuid"`
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	EndpointAddress string      `json:"ipAddress"`
	VPCID           string      `json:"networkId"`
	SubnetID        string      `json:"subnetId"`
	Description     string      `json:"description"`
	SubnetCIDR      string      `json:"subnetCIDR"`
	VPCCIDR         string      `json:"networkCIDR"`
	AddressPairIPs  []string    `json:"addressPairIps"`
	Status          string      `json:"status"`
	CreatedAt       string      `json:"createdAt"`
	NetworkName     string      `json:"networkName"`
	SubnetName      string      `json:"subnetName"`
	Type            string      `json:"type"`
	Mode            string      `json:"mode"`
	Zone            NetworkZone `json:"zone"`
}

type NetworkRoute struct {
	UUID                 string `json:"uuid"`
	RouteTableID         string `json:"routeTableId"`
	RoutingType          string `json:"routingType"`
	DestinationCIDRBlock string `json:"destinationCidrBlock"`
	Target               string `json:"target"`
	Status               string `json:"status"`
}

type RouteTable struct {
	UUID      string         `json:"uuid"`
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	NetworkID string         `json:"networkId"`
	CreatedAt string         `json:"createdAt"`
	Routes    []NetworkRoute `json:"routes"`
}

type Peering struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	FromVPCID   string `json:"fromVpcId"`
	FromVPCUUID string `json:"fromVpcUuid"`
	FromCIDR    string `json:"fromCidr"`
	EndVPCID    string `json:"endVpcId"`
	EndVPCUUID  string `json:"endVpcUuid"`
	EndCIDR     string `json:"endCidr"`
	CreatedAt   string `json:"createdAt"`
}

type NetworkACL struct {
	UUID        string `json:"uuid"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	NetworkID   string `json:"networkId"`
	SubnetID    string `json:"subnetId"`
	CreatedAt   string `json:"createdAt"`
}

type Subnet struct {
	UUID                   string                  `json:"uuid"`
	Name                   string                  `json:"name"`
	NetworkID              string                  `json:"networkId"`
	NetworkUUID            string                  `json:"networkUuid"`
	CIDR                   string                  `json:"cidr"`
	Status                 string                  `json:"status"`
	InterfaceACLPolicyID   string                  `json:"interfaceAclPolicyId"`
	InterfaceACLPolicyUUID string                  `json:"interfaceAclPolicyUuid"`
	InterfaceACLPolicyName string                  `json:"interfaceAclPolicyName"`
	RouteTableID           string                  `json:"routeTableId"`
	RouteTableUUID         string                  `json:"routeTableUuid"`
	ZoneID                 string                  `json:"zoneId"`
	Zone                   NetworkZone             `json:"zone"`
	Description            string                  `json:"description"`
	CreatedAt              string                  `json:"createdAt"`
	UpdatedAt              string                  `json:"updatedAt"`
	ServerCount            int                     `json:"serverCount"`
	VolumeCount            int                     `json:"volumeCount"`
	SecondarySubnets       []SubnetSecondarySubnet `json:"secondarySubnets"`
}

type SubnetSecondarySubnet struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	CIDR string `json:"cidr"`
}

type SecurityGroupRule struct {
	ID              string `json:"id"`
	SecurityGroupID string `json:"securityGroupId"`
	RuleID          string `json:"ruleId"`
	Direction       string `json:"direction"`
	EtherType       string `json:"etherType"`
	Protocol        string `json:"protocol"`
	PortRangeMin    int    `json:"portRangeMin"`
	PortRangeMax    int    `json:"portRangeMax"`
	RemoteIPPrefix  string `json:"remoteIpPrefix"`
	RemoteGroupID   string `json:"remoteGroupId"`
	Status          string `json:"status"`
	Description     string `json:"description"`
	CreatedAt       string `json:"createdAt"`
}

type RouteTableRoute struct {
	RouteTableID         string `json:"routeTableId"`
	UUID                 string `json:"uuid"`
	RoutingType          string `json:"routingType"`
	DestinationCIDRBlock string `json:"destinationCidrBlock"`
	Target               string `json:"target"`
	Status               string `json:"status"`
}

type NetworkEndpoint struct {
	UUID                      string `json:"uuid"`
	ID                        string `json:"id"`
	Name                      string `json:"name"`
	EndpointName              string `json:"endpointName"`
	EndpointURL               string `json:"endpointUrl"`
	EndpointURLSnake          string `json:"endpoint_url"`
	EndpointAuthURL           string `json:"endpointAuthUrl"`
	EndpointAuthURLSnake      string `json:"endpoint_auth_url"`
	EndpointEncryptURL        string `json:"endpoint_encrypt_url"`
	EndpointIP                string `json:"endpointIp"`
	EndpointServiceID         string `json:"endpointServiceId"`
	EndpointServiceName       string `json:"endpointServiceName"`
	InterfaceType             string `json:"interfaceType"`
	EnablePrivateDNS          bool   `json:"enablePrivateDns"`
	EnableDNSName             bool   `json:"enableDnsName"`
	Status                    string `json:"status"`
	CreatedAt                 string `json:"createdAt"`
	LastSyncTime              string `json:"lastSyncTime"`
	VPCID                     string `json:"vpcId"`
	SubnetID                  string `json:"subnetId"`
	EndpointDomains           any    `json:"endpointDomains"`
	ProjectID                 string `json:"projectId"`
	ProjectUUID               string `json:"projectUuid"`
	Project                   any    `json:"project"`
	PortalUserID              any    `json:"portalUserId"`
	RegionID                  string `json:"regionId"`
	RegionUUID                string `json:"regionUuid"`
	Region                    any    `json:"region"`
	ResourceServiceID         string `json:"resourceServiceId"`
	EndpointResource          any    `json:"endpointResource"`
	EndpointDetailInformation any    `json:"endpointDetailInformation"`
	EndpointType              string `json:"endpointType"`
	Category                  any    `json:"category"`
	Package                   any    `json:"apackage"`
	PackageID                 any    `json:"packageId"`
	PackageName               string `json:"packageName"`
	Packages                  any    `json:"packages"`
	Price                     any    `json:"price"`
	MonthlyPrice              any    `json:"monthlyPrice"`
	CurrencyUnit              string `json:"currencyUnit"`
	BillingSKU                string `json:"billingSku"`
	BillingStatus             string `json:"billingStatus"`
	BackendProjectID          string `json:"backendProjectId"`
	Description               string `json:"description"`
	DNSStatus                 string `json:"dnsStatus"`
	ElasticIPs                any    `json:"elasticIps"`
	CIDR                      string `json:"cidr"`
	RouteTableUUID            string `json:"routeTableUuid"`
	InterfaceACLPolicyName    string `json:"interfaceAclPolicyName"`
	InterfaceACLPolicyUUID    string `json:"interfaceAclPolicyUuid"`
	IsDefault                 bool   `json:"isDefault"`
	SecGroups                 any    `json:"secGroups"`
	Subnets                   any    `json:"subnets"`
	Service                   any    `json:"service"`
	Subnet                    any    `json:"subnet"`
	UpdatedAt                 string `json:"updatedAt"`
	Version                   string `json:"version"`
	VPC                       any    `json:"vpc"`
	ZoneUUID                  string `json:"zoneUuid"`
}

type NetworkEndpointDetail struct {
	NetworkEndpoint
}

type Tag struct {
	UUID         string `json:"uuid"`
	Key          string `json:"tagKey"`
	Value        string `json:"tagValue"`
	ResourceUUID string `json:"resourceUuid"`
	ResourceType string `json:"resourceType"`
	SystemTag    bool   `json:"systemTag"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type Interconnect struct {
	UUID         string `json:"uuid"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Description  string `json:"description"`
	TypeID       string `json:"typeId"`
	TypeName     string `json:"typeName"`
	PackageID    string `json:"packageId"`
	CreatedAt    string `json:"createdAt"`
	ProjectID    string `json:"projectId"`
	CircuitID    any    `json:"circuitId"`
	EnableGW2    bool   `json:"enableGw2"`
	GW01IP       string `json:"gw01Ip"`
	GW02IP       string `json:"gw02Ip"`
	GWVIP        string `json:"gwVip"`
	RemoteGW01IP string `json:"remoteGw01Ip"`
	RemoteGW02IP string `json:"remoteGw02Ip"`
}

type AddressPair struct {
	UUID               string `json:"uuid"`
	VirtualIPAddressID string `json:"virtualIpAddressId"`
	VirtualSubnetID    string `json:"virtualSubnetId"`
	NetworkInterfaceIP string `json:"networkInterfaceIp"`
	NetworkInterfaceID string `json:"networkInterfaceId"`
	CIDR               string `json:"cidr"`
	CreatedAt          string `json:"createdAt"`
}

type VNetworkRegion struct {
	UUID             string `json:"uuid"`
	Name             string `json:"name"`
	Code             string `json:"code"`
	GatewayURL       string `json:"gatewayUrl"`
	DashboardURL     string `json:"vnetworkDashboard"`
	VServerEndpoint  string `json:"vserverEndpoint"`
	VLBEndpoint      string `json:"vlbEndpoint"`
	UIServerEndpoint string `json:"uiServerEndpoint"`
	VNetworkEndpoint string `json:"vnetworkEndpoint"`
	VDNSEndpoint     string `json:"vdnsEndpoint"`
}

func (r VNetworkRegion) matches(region string) bool {
	if region == "" {
		return false
	}
	for _, value := range []string{
		r.Name,
		r.Code,
		r.GatewayURL,
		r.DashboardURL,
		r.VServerEndpoint,
		r.VLBEndpoint,
		r.UIServerEndpoint,
		r.VNetworkEndpoint,
	} {
		if strings.Contains(strings.ToLower(value), strings.ToLower(region)) {
			return true
		}
	}
	return false
}

func (r VNetworkRegion) endpoint() string {
	switch {
	case r.DashboardURL != "":
		return strings.TrimRight(r.DashboardURL, "/") + "/vnetwork-gateway/"
	case r.GatewayURL != "":
		return strings.TrimRight(r.GatewayURL, "/") + "/"
	default:
		return ""
	}
}
