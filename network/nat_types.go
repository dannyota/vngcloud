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

type ListNATZonesInput struct{ ZoneID string }
type ListNATZonesOutput = core.List[NATAvailabilityZone]
type ListNATPackagesInput struct {
	ZoneID             string
	AvailabilityZoneID string `vngcloud:"required"`
}
type ListNATPackagesOutput = core.List[NATPackageOffer]

type NATAvailabilityZone struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	ZoneType    string `json:"zoneType"`
	IsEnabled   bool   `json:"isEnabled"`
	IsDefault   bool   `json:"isDefault"`
	Description string `json:"description"`
}

type NATOfferPrice struct {
	OptimumPrice    float64 `json:"optimumPrice"`
	OriginalPrice   float64 `json:"originalPrice"`
	DiscountPrice   float64 `json:"discountPrice"`
	DiscountPercent float64 `json:"discountPercent"`
}

type NATPackageOffer struct {
	UUID              string        `json:"uuid"`
	Name              string        `json:"name"`
	PackageID         string        `json:"packageId"`
	ResourceServiceID string        `json:"resourceServiceId"`
	BillingSKU        string        `json:"billingSku"`
	ServiceName       string        `json:"serviceName"`
	Description       *string       `json:"description"`
	CurrencyUnit      string        `json:"currencyUnit"`
	CreatedAt         string        `json:"createdAt"`
	IsDefault         bool          `json:"isDefault"`
	MonthlyPrice      float64       `json:"monthlyPrice"`
	Price             NATOfferPrice `json:"price"`
}

type CreateNATInstanceInput struct {
	Name               string `vngcloud:"required"`
	ZoneID             string `vngcloud:"required"`
	AvailabilityZoneID string `vngcloud:"required"`
	PackageID          string `vngcloud:"required"`
	VPCID              string `vngcloud:"required"`
	MaxPrice           float64
}

type CreateNATInstanceOutput struct {
	NATInstance  *NATInstance
	OrderID      string
	MonthlyPrice float64
	TotalPrice   float64
	Currency     string
	AutoRenew    *bool
}

type DeleteNATInstanceInput struct {
	ZoneID string `vngcloud:"required"`
	VPCID  string `vngcloud:"required"`
	NATID  string `vngcloud:"required"`
	NoWait bool
}
type DeleteNATInstanceOutput struct{}

type NetworkPriceProperty struct {
	OptimumPrice    float64  `json:"optimumPrice"`
	MonthlyPrice    float64  `json:"monthlyPrice"`
	CurrentPrice    *float64 `json:"currentPrice"`
	DiscountPercent *float64 `json:"discountPercent"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
}

type NetworkQuoteOutput struct {
	OptimumPrice    float64
	OriginalPrice   float64
	DiscountPrice   float64
	DiscountPercent *float64
	Properties      []NetworkPriceProperty
	MonthlyPrice    float64
	TotalPrice      float64
	Currency        string
}
