package network

import (
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// Zone is the vNetwork zone shape shared with other services' resource
// models, such as compute.Server.Zone.
type Zone = core.NetworkZone

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
	ListData  []ACL `json:"listData"`
	Page      int   `json:"page"`
	PageSize  int   `json:"pageSize"`
	TotalPage int   `json:"totalPage"`
	TotalItem int   `json:"totalItem"`
}

type listInterconnectsResponse struct {
	ListData  []Interconnect `json:"listData"`
	Page      int            `json:"page"`
	PageSize  int            `json:"pageSize"`
	TotalPage int            `json:"totalPage"`
	TotalItem int            `json:"totalItem"`
}

type listEndpointsResponse struct {
	Data      []Endpoint `json:"data"`
	Page      int        `json:"page"`
	Size      int        `json:"size"`
	TotalPage int        `json:"totalPage"`
	Total     int        `json:"total"`
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
	UUID           string   `json:"id"`
	Status         string   `json:"status"`
	ElasticIPs     []string `json:"elasticIps"`
	Name           string   `json:"displayName"`
	CreatedAt      string   `json:"createdAt"`
	CIDR           string   `json:"cidr"`
	DHCPOptionName string   `json:"dhcpOptionName"`
	DHCPOptionID   string   `json:"dhcpOptionId"`
	RouteTableName string   `json:"routeTableName"`
	RouteTableID   string   `json:"routeTableId"`
	Zone           Zone     `json:"zone"`
	DNSStatus      string   `json:"dnsStatus"`
	DNSID          string   `json:"dnsId"`
	MTU            int      `json:"mtu"`
	ServerCount    int      `json:"serverCount"`
	VolumeCount    int      `json:"volumeCount"`
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
	UUID            string   `json:"uuid"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	EndpointAddress string   `json:"ipAddress"`
	VPCID           string   `json:"networkId"`
	SubnetID        string   `json:"subnetId"`
	Description     string   `json:"description"`
	SubnetCIDR      string   `json:"subnetCIDR"`
	VPCCIDR         string   `json:"networkCIDR"`
	AddressPairIPs  []string `json:"addressPairIps"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"createdAt"`
	NetworkName     string   `json:"networkName"`
	SubnetName      string   `json:"subnetName"`
	Type            string   `json:"type"`
	Mode            string   `json:"mode"`
	Zone            Zone     `json:"zone"`
}

type Route struct {
	UUID                 string `json:"uuid"`
	RouteTableID         string `json:"routeTableId"`
	RoutingType          string `json:"routingType"`
	DestinationCIDRBlock string `json:"destinationCidrBlock"`
	Target               string `json:"target"`
	Status               string `json:"status"`
}

type RouteTable struct {
	UUID      string  `json:"uuid"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	NetworkID string  `json:"networkId"`
	CreatedAt string  `json:"createdAt"`
	Routes    []Route `json:"routes"`
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

type ACL struct {
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
	Zone                   Zone                    `json:"zone"`
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

type Endpoint struct {
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

type EndpointDetail struct {
	Endpoint
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
