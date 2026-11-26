package pricing

import "encoding/json"

// EstimateInput specifies independent monthly units for one configured account.
// The setup requires 1..1000 rows and at most 10000 summed quantities. Component
// keys must be globally unique and match ^[A-Za-z0-9_-]{1,64}$.
// The contract is recorded in docs/design/cost-estimate.md.
type EstimateInput struct {
	// Servers specifies server groups, each including its root and optional
	// bundled data disk. Empty means no servers are requested.
	Servers []EstimateServer
	// Volumes specifies additional disks. Their quantities are independent of
	// server quantities. Empty means no separate volumes are requested.
	Volumes []EstimateVolume
	// PublicIPs specifies named IP products. Unverified kinds retain unknown
	// prices; empty means IP costs are outside the requested scope.
	PublicIPs []EstimatePublicIP
	// Storage specifies monthly packages, each at its own QuotaGB, across at
	// most 32 resolved regions. Empty means no storage packages are requested.
	Storage []EstimateStorage
	// MaxRequests bounds logical catalog and quote calls, excluding login and
	// transport retries. Zero selects 500; explicit values must be 1..2000.
	MaxRequests int
	// AllowPartial retains all rows when a valid result survives an operational
	// or arithmetic failure and returns a DiscoveryError. False returns nil
	// output on error.
	// Invalid setup input always rejects the whole call. Authentication,
	// exhausted throttling, cancellation, and limits stop further scheduling.
	AllowPartial bool
}

// EstimateServer specifies identical server units with included root and optional
// bundled data disks. Required Quantity is 1..1000; sizes are positive GB within
// returned type limits. Empty encryption selectors mean plain disks. See
// docs/design/cost-estimate.md for selector and image requirements.
type EstimateServer struct {
	// Key labels this component under EstimateInput's setup-wide key rules.
	Key string
	// ZoneID selects an explicit zone from the region's catalog.
	ZoneID string
	// FlavorID goes to the quote unchanged and establishes no deployability.
	FlavorID string
	// ImageID selects an explicit image. Pricing requires unlicensed Ubuntu
	// with a present positive minimum root size; other images can be unknown.
	ImageID string
	// Quantity is the required number of independent units, from 1 to 1000.
	Quantity int
	// RootDiskSize is GB per server, at least the image minimum and type minimum
	// and at most the returned type maximum.
	RootDiskSize int
	// RootDiskTypeID selects the returned root volume type.
	RootDiskTypeID string
	// RootDiskEncryptionTypeID is a returned encryption type key, not key
	// material. Empty means a plain root disk.
	RootDiskEncryptionTypeID string
	// DataDiskSize is GB per bundled disk. Zero omits the disk; a positive size
	// requires DataDiskTypeID and must satisfy the returned type limits.
	DataDiskSize int
	// DataDiskTypeID selects the bundled disk type and requires DataDiskSize.
	// Empty omits the disk together with a zero DataDiskSize.
	DataDiskTypeID string
	// DataDiskEncryptionTypeID is an optional returned encryption type key and
	// requires a bundled disk. Empty means a plain bundled disk.
	DataDiskEncryptionTypeID string
}

// EstimateVolume specifies additional identical disks, counted separately from
// server quotes. Quantity counts independent volumes even with ServerKey set.
// Association constraints are recorded in docs/design/cost-estimate.md.
type EstimateVolume struct {
	// Key labels this component under EstimateInput's setup-wide key rules.
	Key string
	// ServerKey optionally names a server group in the same zone. An encrypted
	// volume requires that group's root to be encrypted. No attachment occurs.
	ServerKey string
	// ZoneID selects an explicit zone from the region's catalog.
	ZoneID string
	// VolumeTypeID selects the returned disk type.
	VolumeTypeID string
	// Quantity is the required number of separate volumes, from 1 to 1000.
	Quantity int
	// Size is positive GB per volume within the returned type limits.
	Size int
	// EncryptionTypeID is a returned encryption type key, not key material.
	// Empty means a plain volume.
	EncryptionTypeID string
}

// EstimatePublicIP specifies independent IP products without free-IP deductions.
// Unknown product prices remain nil under docs/design/cost-estimate.md.
type EstimatePublicIP struct {
	// Key labels this component under EstimateInput's setup-wide key rules.
	Key string
	// Kind is public-vip, floating-ip, external-interface, or reservation.
	// Only public-vip has an established quote; the other kinds remain unknown.
	Kind string
	// Type is public, public-vm, or public-mkp for public-vip, and must be empty
	// for other kinds. No default IP product is inferred.
	Type string
	// Quantity is the required number of independent products, from 1 to 1000.
	Quantity int
}

// EstimateStorage specifies independent monthly packages, each at QuotaGB.
// Monthly offering and quota selection follow docs/design/cost-estimate.md.
type EstimateStorage struct {
	// Key labels this component under EstimateInput's setup-wide key rules.
	Key string
	// Region selects a returned storage region name. Empty uses configured
	// resolution, normally HCM04 for hcm-3 and HAN02 for han-1.
	Region string
	// Type is the exact returned active project type with a monthly offering.
	Type string
	// Quantity is the required number of separate packages, from 1 to 1000.
	Quantity int
	// QuotaGB is positive GB per package within the region's configured limits.
	QuotaGB int64
}

// EstimateOutput preserves input order and reports nullable VND sums, coverage,
// and UTC completion metadata. It prices only specified recurring components,
// never a complete bill. See docs/design/cost-estimate.md.
type EstimateOutput struct {
	Items    []EstimateItem
	Total    EstimateTotal
	Coverage []EstimateCoverage
	Metadata DiscoveryMetadata
}

// EstimateItem preserves one requested row. Price.MonthlyPrice is the unit quote
// in VND; MonthlyAmount includes Quantity. Failed, unknown, and inapplicable rows
// have nil amounts. Breakdown contains per-unit informational charges, already
// included in the quote, under docs/design/cost-estimate.md.
type EstimateItem struct {
	Key, Kind, ServerKey, Region, ZoneID string
	Quantity                             int
	Price                                DiscoveryPrice
	MonthlyAmount                        *float64
	Breakdown                            []EstimateCharge
}

// EstimateCharge preserves a property name and optional per-unit monthly VND
// amount. Nil means unknown; descriptions are omitted. These charges are already
// included in the top-level quote, as recorded in docs/design/cost-estimate.md.
type EstimateCharge struct {
	Name         string
	MonthlyPrice *float64
}

// EstimateTotal reports unrounded independent monthly VND quotes with VAT as
// returned. KnownMonthlySubtotal is nil when no valid amount survives or the sum
// overflows. RequestedMonthlyTotal requires every row to be quoted and processing
// to finish; RequestedComplete expresses that condition. CostComplete is always
// false because usage and excluded products remain unpriced. See
// docs/design/cost-estimate.md for the fixed Basis, Tax, and Scope labels.
type EstimateTotal struct {
	KnownMonthlySubtotal, RequestedMonthlyTotal *float64
	Currency, Tax, Basis, Scope                 string
	RequestedComplete, CostComplete             bool
}

// EstimateCoverage explains quoted scope, unknown or excluded costs, and failed
// processing. Scope names a requested key or setup; not-attempted identifies
// unscheduled rows. Topics and reasons follow docs/design/cost-estimate.md.
type EstimateCoverage struct {
	Topic, Scope, Status, Reason string
}

// MarshalJSON encodes absent item and coverage lists as [] rather than null.
func (o EstimateOutput) MarshalJSON() ([]byte, error) {
	type output EstimateOutput
	if o.Items == nil {
		o.Items = []EstimateItem{}
	}
	if o.Coverage == nil {
		o.Coverage = []EstimateCoverage{}
	}
	return json.Marshal(output(o))
}

// MarshalJSON encodes an absent breakdown as [] rather than null.
func (i EstimateItem) MarshalJSON() ([]byte, error) {
	type item EstimateItem
	if i.Breakdown == nil {
		i.Breakdown = []EstimateCharge{}
	}
	return json.Marshal(item(i))
}
