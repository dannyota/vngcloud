package loadbalancer

// ListLoadBalancersInput selects a page of load balancers.
type ListLoadBalancersInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// GetLoadBalancerInput identifies a load balancer to read.
type GetLoadBalancerInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
}

// ListPackagesInput optionally filters packages by zone.
type ListPackagesInput struct {
	// ZoneID is sent as the zoneId query parameter when nonempty.
	ZoneID string
}

// ListCertificatesInput selects a page of certificates.
type ListCertificatesInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListListenersInput identifies the load balancer whose listeners to read.
type ListListenersInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
}

// GetListenerInput identifies a listener to read.
type GetListenerInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// ListenerID is required.
	ListenerID string `vngcloud:"required"`
}

// ListPoolsInput identifies the load balancer whose pools to read.
type ListPoolsInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
}

// GetPoolInput identifies a pool to read.
type GetPoolInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// PoolID is required.
	PoolID string `vngcloud:"required"`
}

// GetPoolHealthMonitorInput identifies the pool whose health monitor to read.
type GetPoolHealthMonitorInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// PoolID is required.
	PoolID string `vngcloud:"required"`
}

// ListPoolMembersInput identifies the pool whose members to read.
type ListPoolMembersInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// PoolID is required.
	PoolID string `vngcloud:"required"`
}

// ListPoliciesInput identifies the listener whose policies to read.
type ListPoliciesInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// ListenerID is required.
	ListenerID string `vngcloud:"required"`
}

// GetPolicyInput identifies a listener policy to read.
type GetPolicyInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// ListenerID is required.
	ListenerID string `vngcloud:"required"`
	// PolicyID is required.
	PolicyID string `vngcloud:"required"`
}

// ListTagsInput identifies the load balancer whose tags to read.
type ListTagsInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
}

// GetCertificateInput identifies a certificate to read.
type GetCertificateInput struct {
	// CertificateID is required.
	CertificateID string `vngcloud:"required"`
}
