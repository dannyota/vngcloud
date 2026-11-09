package network

import "danny.vn/vngcloud/internal/core"

// ListNATInstancesInput selects parameters for ListNATInstances.
type ListNATInstancesInput struct {
	// ZoneID selects the zone; empty discovers one.
	ZoneID string
	// Page defaults to 1 when zero and must not be negative.
	Page int
	// Size defaults to 10 when zero and must not be negative.
	Size int
}

// ListNATInstancesOutput holds NATInstance items and pagination metadata.
type ListNATInstancesOutput = core.PagedList[NATInstance]

// NATInstance is a Public NAT inventory row. Status describes provisioning.
// See docs/design/network-services-api.md.
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

// NATPackage holds nat package data from the API.
// See docs/design/network-services-api.md.
type NATPackage struct {
	ID        string   `json:"id"`
	UUID      string   `json:"uuid"`
	Name      string   `json:"name"`
	CreatedAt string   `json:"createdAt"`
	PackageID string   `json:"packageId"`
	Default   bool     `json:"default"`
	Image     NATImage `json:"image"`
}

// NATImage holds nat image data from the API.
// See docs/design/network-services-api.md.
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
// See docs/design/network-services-api.md.
type NATPackageLimit struct {
	CPU      int `json:"cpu"`
	Memory   int `json:"memory"`
	DiskSize int `json:"diskSize"`
}

// NATVPC holds natvpc data from the API.
// See docs/design/network-services-api.md.
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

// ListNATZonesInput selects parameters for ListNATZones.
type ListNATZonesInput struct {
	// ZoneID selects the zone scope; empty discovers one.
	ZoneID string
}

// ListNATZonesOutput holds NATAvailabilityZone items.
type ListNATZonesOutput = core.List[NATAvailabilityZone]

// ListNATPackagesInput requires AvailabilityZoneID.
type ListNATPackagesInput struct {
	// ZoneID selects the zone scope; empty discovers one.
	ZoneID string
	// AvailabilityZoneID must identify one enabled availability zone and use
	// only ASCII letters, digits, or hyphens.
	AvailabilityZoneID string `vngcloud:"required"`
}

// ListNATPackagesOutput holds NATPackageOffer items.
type ListNATPackagesOutput = core.List[NATPackageOffer]

// NATAvailabilityZone holds nat availability zone data from the API.
// See docs/design/network-services-api.md.
type NATAvailabilityZone struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	ZoneType    string `json:"zoneType"`
	IsEnabled   bool   `json:"isEnabled"`
	IsDefault   bool   `json:"isDefault"`
	Description string `json:"description"`
}

// NATOfferPrice holds nat offer price data from the API.
// See docs/design/network-writes-api.md.
type NATOfferPrice struct {
	OptimumPrice    float64 `json:"optimumPrice"`
	OriginalPrice   float64 `json:"originalPrice"`
	DiscountPrice   float64 `json:"discountPrice"`
	DiscountPercent float64 `json:"discountPercent"`
}

// NATPackageOffer holds nat package offer data from the API.
// See docs/design/network-services-api.md.
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

// CreateNATInstanceInput requires Name, ZoneID, AvailabilityZoneID, PackageID
// and VPCID.
type CreateNATInstanceInput struct {
	// Name is required.
	Name string `vngcloud:"required"`
	// ZoneID is required; use ASCII letters, digits, or hyphens.
	ZoneID string `vngcloud:"required"`
	// AvailabilityZoneID is required; use ASCII letters, digits, or hyphens.
	AvailabilityZoneID string `vngcloud:"required"`
	// PackageID must identify one package in the selected availability zone
	// and use only ASCII letters, digits, or hyphens.
	PackageID string `vngcloud:"required"`
	// VPCID is required; use ASCII letters, digits, or hyphens.
	VPCID string `vngcloud:"required"`
	// MaxPrice bounds the create quote; QuoteCreateNATInstance ignores it.
	MaxPrice float64
}

// CreateNATInstanceOutput holds the result of CreateNATInstance.
type CreateNATInstanceOutput struct {
	NATInstance  *NATInstance
	OrderID      string
	MonthlyPrice float64
	TotalPrice   float64
	Currency     string
	AutoRenew    *bool
}

// DeleteNATInstanceInput requires ZoneID, VPCID and NATID.
type DeleteNATInstanceInput struct {
	// ZoneID is required; use ASCII letters, digits, or hyphens.
	ZoneID string `vngcloud:"required"`
	// VPCID is required; use ASCII letters, digits, or hyphens.
	VPCID string `vngcloud:"required"`
	// NATID is required; use ASCII letters, digits, or hyphens.
	NATID string `vngcloud:"required"`
	// NoWait skips the post-operation wait.
	NoWait bool
}

// DeleteNATInstanceOutput carries no result fields.
type DeleteNATInstanceOutput struct{}

// NetworkPriceProperty holds network price property data from the API.
// See docs/design/network-services-api.md.
type NetworkPriceProperty struct {
	OptimumPrice    float64  `json:"optimumPrice"`
	MonthlyPrice    float64  `json:"monthlyPrice"`
	CurrentPrice    *float64 `json:"currentPrice"`
	DiscountPercent *float64 `json:"discountPercent"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
}

// NetworkQuoteOutput holds the result of NetworkQuote.
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
