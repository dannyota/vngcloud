package network

import "danny.vn/vngcloud/internal/core"

type ListNATInstancesInput struct {
	ZoneID string
	Page   int
	Size   int
}

type ListNATInstancesOutput = core.PagedList[NATInstance]

// NATInstance is a Public NAT inventory row. Status describes provisioning.
type NATInstance struct {
	UUID          string     `json:"uuid"`
	NATName       string     `json:"natName"`
	Status        string     `json:"status"`
	CreatedAt     string     `json:"createdAt"`
	UpdatedAt     string     `json:"updatedAt"`
	ProjectUUID   string     `json:"projectUuid"`
	ZoneUUID      string     `json:"zoneUuid"`
	NATGatewayIP  *string    `json:"natGatewayIp"`
	PublicIP      *string    `json:"publicIp"`
	DeletedAt     *string    `json:"deletedAt"`
	BillingStatus *string    `json:"billingStatus"`
	NATPackage    NATPackage `json:"natPackage"`
	VPC           NATVPC     `json:"vpc"`
}

type NATPackage struct {
	ID        string   `json:"id"`
	UUID      string   `json:"uuid"`
	Name      string   `json:"name"`
	CreatedAt string   `json:"createdAt"`
	PackageID string   `json:"packageId"`
	Default   bool     `json:"default"`
	Image     NATImage `json:"image"`
}

type NATImage struct {
	ID            string          `json:"id"`
	UUID          string          `json:"uuid"`
	ImageType     string          `json:"imageType"`
	ImageVersion  string          `json:"imageVersion"`
	Licence       string          `json:"licence"`
	FlavorZoneIDs []string        `json:"flavorZoneIds"`
	PackageLimit  NATPackageLimit `json:"packageLimit"`
}

// NATPackageLimit preserves the API's integers; their units are unverified.
type NATPackageLimit struct {
	CPU      int `json:"cpu"`
	Memory   int `json:"memory"`
	DiskSize int `json:"diskSize"`
}

type NATVPC struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	CIDR         string `json:"cidr"`
	Status       string `json:"status"`
	RegionID     string `json:"regionId"`
	ProjectID    string `json:"projectId"`
	LastSyncTime string `json:"lastSyncTime"`
	DNSStatus    string `json:"dnsStatus"`
}
