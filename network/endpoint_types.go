package network

// CreateEndpointInput names the vStorage Standard endpoint to buy in hcm-3.
// The VPC and subnet must already exist in availability zone HCM03-1C.
type CreateEndpointInput struct {
	// Name is required and must not be blank or hold control characters.
	Name string `vngcloud:"required"`
	// VPCID is required and has the form net-<uuid>.
	VPCID string `vngcloud:"required"`
	// SubnetID is required and has the form sub-<uuid>.
	SubnetID string `vngcloud:"required"`
	// MaxPrice caps the fresh monthly quote in VND; QuoteCreateEndpoint
	// ignores it. Zero blocks every purchase.
	MaxPrice float64
}

// QuoteCreateEndpointOutput holds a fresh monthly quote.
type QuoteCreateEndpointOutput struct {
	// MonthlyPrice is the quoted amount in Currency per month.
	MonthlyPrice float64
	// Currency is VND.
	Currency string
}

// CreateEndpointOutput holds the result of CreateEndpoint. MonthlyPrice is the
// fresh quote, not the debit. It carries no renewal or billing status.
type CreateEndpointOutput struct {
	// EndpointID is the endpoint the create returned, set only once screened.
	EndpointID string
	// Status is the last state a scoped list verified for EndpointID.
	Status string
	// MonthlyPrice is the quoted amount in Currency per month.
	MonthlyPrice float64
	// Currency is VND.
	Currency string
}

// DeleteEndpointInput names the endpoint to delete and its VPC.
type DeleteEndpointInput struct {
	// EndpointID is required and has the form enp-<uuid>.
	EndpointID string `vngcloud:"required"`
	// VPCID is required, has the form net-<uuid>, and must match the endpoint.
	VPCID string `vngcloud:"required"`
}

// DeleteEndpointOutput holds the result of DeleteEndpoint.
type DeleteEndpointOutput struct {
	// EndpointID is the endpoint the delete targeted.
	EndpointID string
	// Deleted is true only when a complete scoped list omits EndpointID.
	Deleted bool
}
