package globalloadbalancer

import "time"

// CreatePoolInput selects a TCP pool with one private member group and one
// vServer backend on an existing Layer 4 global load balancer. See
// docs/design/glb-pool.md.
type CreatePoolInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// Name is the pool name and is required.
	Name string `vngcloud:"required"`
	// MemberName names the member group and its one backend row and is required.
	MemberName string `vngcloud:"required"`
	// BackendProjectID must equal the client's resolved project and is required.
	BackendProjectID string `vngcloud:"required"`
	// BackendServerID is the vServer server that backs the pool and is required.
	BackendServerID string `vngcloud:"required"`
	// BackendVPCID is the VPC of the server's interface and is required.
	BackendVPCID string `vngcloud:"required"`
	// BackendSubnetID is the subnet of the server's interface and is required.
	BackendSubnetID string `vngcloud:"required"`
	// BackendPort is the service and health check port, 1 through 65535, and is required.
	BackendPort int `vngcloud:"required"`
}

// CreatePoolOutput holds the confirmed or partially confirmed pool and its group.
type CreatePoolOutput struct {
	Pool       Pool
	PoolMember PoolMember
}

// CreateListenerInput selects a TCP listener bound to the load balancer's
// only pool. See docs/design/glb-pool.md.
type CreateListenerInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// Name is the listener name and is required.
	Name string `vngcloud:"required"`
	// PoolID is the pool the listener forwards to and is required.
	PoolID string `vngcloud:"required"`
	// Port is the listener port, 1 through 65535, and is required.
	Port int `vngcloud:"required"`
	// AllowedCIDRs lists canonical IPv4 prefixes from /8 through /32 that may
	// connect. It is required and may not cover all IPv4 addresses.
	AllowedCIDRs []string `vngcloud:"required"`
	// BackendProjectID must equal the client's resolved project and is required.
	BackendProjectID string `vngcloud:"required"`
	// BackendServerID is the vServer server whose address the pool holds and is required.
	BackendServerID string `vngcloud:"required"`
}

// CreateListenerOutput holds the confirmed or partially confirmed listener.
type CreateListenerOutput struct{ Listener Listener }

// DeleteListenerInput identifies a listener.
type DeleteListenerInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// ListenerID is required.
	ListenerID string `vngcloud:"required"`
}

// DeleteListenerOutput identifies the listener and confirmed absence.
type DeleteListenerOutput struct {
	LoadBalancerID string
	ListenerID     string
	Deleted        bool
}

// DeletePoolInput identifies an empty pool.
type DeletePoolInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
	// PoolID is required.
	PoolID string `vngcloud:"required"`
}

// DeletePoolOutput identifies the pool and confirmed absence.
type DeletePoolOutput struct {
	LoadBalancerID string
	PoolID         string
	Deleted        bool
}

const (
	createWait = 15 * time.Minute
	deleteWait = 10 * time.Minute
)

func (c *Client) pollEvery() time.Duration {
	if c.poll > 0 {
		return c.poll
	}
	return 5 * time.Second
}
