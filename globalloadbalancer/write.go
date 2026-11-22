package globalloadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// ErrNotSettled means a write may have succeeded but has not been confirmed.
var ErrNotSettled = errors.New("globalloadbalancer: write not settled; inspect inventory and billing before another mutation")

// CreateLoadBalancerInput selects a global load balancer configuration.
type CreateLoadBalancerInput struct {
	// Name is required.
	Name string `vngcloud:"required"`
	// PackageID is required.
	PackageID string `vngcloud:"required"`
	// Description is optional.
	Description string
	// Listener optionally configures one TCP listener.
	Listener *CreateListenerInput
	// Pool optionally configures one TCP pool.
	Pool *CreatePoolInput
}

// CreateListenerInput configures a TCP listener.
type CreateListenerInput struct {
	// Name is required.
	Name string
	// Port is between 1 and 65535.
	Port int
	// AllowedCIDRs contains canonical restricted IPv4 CIDRs.
	AllowedCIDRs []string
}

// CreatePoolInput configures a TCP pool.
type CreatePoolInput struct {
	// Name is required.
	Name string
	// HealthCheck optionally overrides TCP health-check defaults.
	HealthCheck *HealthCheckInput
	// Members lists private backend members.
	Members []PoolMemberInput
}

// HealthCheckInput configures TCP health checks.
type HealthCheckInput struct {
	// HealthyThreshold is the number of successful probes required.
	HealthyThreshold int
	// UnhealthyThreshold is the number of failed probes required.
	UnhealthyThreshold int
	// Interval is the probe interval.
	Interval int
	// Timeout is the probe timeout.
	Timeout int
}

// PoolMemberInput identifies a private backend.
type PoolMemberInput struct {
	// Region identifies the backend region.
	Region string
	// VPCID identifies the backend VPC.
	VPCID string
	// SubnetID identifies the backend subnet.
	SubnetID string
	// Address is the backend IPv4 address.
	Address string
	// Port is between 1 and 65535.
	Port int
}

// CreateLoadBalancerOutput contains the confirmed or partially confirmed load balancer.
type CreateLoadBalancerOutput struct{ LoadBalancer LoadBalancer }

// DeleteLoadBalancerInput identifies a global load balancer.
type DeleteLoadBalancerInput struct {
	// LoadBalancerID is required.
	LoadBalancerID string `vngcloud:"required"`
}

// DeleteLoadBalancerOutput identifies the resource and confirmed absence.
type DeleteLoadBalancerOutput struct {
	LoadBalancerID string
	Deleted        bool
}

type createBody struct {
	Name        string `json:"name"`
	Package     string `json:"package"`
	Description string `json:"description,omitempty"`
	PaymentFlow string `json:"paymentFlow"`
	Type        string `json:"type"`
	Listener    any    `json:"globalListener,omitempty"`
	Pool        any    `json:"globalPool,omitempty"`
}

func buildCreate(in *CreateLoadBalancerInput) (createBody, error) {
	const op = "globalloadbalancer.CreateLoadBalancer"
	var body createBody
	if err := core.CheckRequired(op, in); err != nil {
		return body, err
	}
	body = createBody{Name: in.Name, Package: in.PackageID, Description: in.Description, PaymentFlow: "automated", Type: "Layer 4"}
	if in.Listener != nil {
		l := in.Listener
		if in.Pool == nil || l.Name == "" || l.Port < 1 || l.Port > 65535 || len(l.AllowedCIDRs) == 0 {
			return body, fmt.Errorf("%w: %s: listener requires a pool, name, port and restricted CIDRs", core.ErrInvalidInput, op)
		}
		for _, cidr := range l.AllowedCIDRs {
			p, err := netip.ParsePrefix(cidr)
			if err != nil || !p.Addr().Is4() || p.Bits() == 0 || p != p.Masked() || p.String() != cidr {
				return body, fmt.Errorf("%w: %s: listener CIDRs must be canonical restricted IPv4 prefixes", core.ErrInvalidInput, op)
			}
		}
		body.Listener = map[string]any{"name": l.Name, "port": l.Port, "allowedCidrs": strings.Join(l.AllowedCIDRs, ","), "protocol": "TCP", "timeoutClient": 50, "timeoutConnection": 5, "timeoutMember": 50}
	}
	if in.Pool != nil {
		p := in.Pool
		if p.Name == "" {
			return body, fmt.Errorf("%w: %s: pool name is required", core.ErrInvalidInput, op)
		}
		h := HealthCheckInput{HealthyThreshold: 3, UnhealthyThreshold: 3, Interval: 30, Timeout: 5}
		if p.HealthCheck != nil {
			h = *p.HealthCheck
		}
		if h.HealthyThreshold < 1 || h.UnhealthyThreshold < 1 || h.Interval < 1 || h.Timeout < 1 || h.Timeout >= h.Interval {
			return body, fmt.Errorf("%w: %s: invalid health check", core.ErrInvalidInput, op)
		}
		members := make([]any, 0, len(p.Members))
		for i, m := range p.Members {
			addr, err := netip.ParseAddr(m.Address)
			if m.Region == "" || m.VPCID == "" || m.SubnetID == "" || err != nil || !addr.Is4() || m.Port < 1 || m.Port > 65535 {
				return body, fmt.Errorf("%w: %s: invalid pool member", core.ErrInvalidInput, op)
			}
			name := fmt.Sprintf("member-%d", i+1)
			members = append(members, map[string]any{"name": name, "region": m.Region, "vpcId": m.VPCID, "type": "PRIVATE", "members": []any{map[string]any{"address": m.Address, "subnetId": m.SubnetID, "port": m.Port, "backupRole": false}}})
		}
		body.Pool = map[string]any{"name": p.Name, "protocol": "TCP", "algorithm": "ROUND_ROBIN", "health": map[string]any{"protocol": "TCP", "healthyThreshold": h.HealthyThreshold, "unhealthyThreshold": h.UnhealthyThreshold, "interval": h.Interval, "timeout": h.Timeout}, "globalPoolMembers": members}
	}
	return body, nil
}

// CreateLoadBalancer submits one create request and waits for active status.
func (c *Client) CreateLoadBalancer(ctx context.Context, in *CreateLoadBalancerInput) (*CreateLoadBalancerOutput, error) {
	body, err := buildCreate(in)
	if err != nil {
		return nil, err
	}
	if err := c.writeProvenance(); err != nil {
		return nil, err
	}
	var reply struct {
		LoadBalancer LoadBalancer `json:"globalLoadBalancer"`
	}
	notSent := false
	var sentCredentials []string
	err = c.c.DoJSON(ctx, transport.Request{Operation: "globalloadbalancer.CreateLoadBalancer", Method: http.MethodPost, URL: c.glbURL([]string{"global-load-balancers"}, nil), Body: body, OK: []int{200, 202}, Once: true, NoRedirect: true, StrictJSON: true, Sensitive: true, KeepObservedStatus: true, NotSent: &notSent, SentCredentials: &sentCredentials}, &reply)
	if err != nil {
		if definitiveWriteError(err, notSent) {
			return nil, sanitizeCreateError(err, in, sentCredentials)
		}
		return nil, fmt.Errorf("%w: create request unconfirmed", ErrNotSettled)
	}
	id := reply.LoadBalancer.ID
	if core.CheckPathID("globalloadbalancer.CreateLoadBalancer", "LoadBalancerID", id) != nil || id == "" {
		return nil, fmt.Errorf("%w: create response lacks a valid ID", ErrNotSettled)
	}
	out := &CreateLoadBalancerOutput{LoadBalancer: LoadBalancer{ID: id}}
	wait, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for {
		lb, err := c.writeDetail(wait, id)
		if err == nil {
			if lb.ID != id || lb.Name != body.Name || lb.Package != body.Package {
				return out, fmt.Errorf("%w: create detail does not match", ErrNotSettled)
			}
			out.LoadBalancer = lb
			if strings.EqualFold(lb.Status, "active") {
				return out, nil
			}
		} else {
			return out, fmt.Errorf("%w: create confirmation interrupted", ErrNotSettled)
		}
		if !pause(wait) {
			return out, fmt.Errorf("%w: create confirmation interrupted", ErrNotSettled)
		}
	}
}

// DeleteLoadBalancer deletes one load balancer and confirms its absence.
func (c *Client) DeleteLoadBalancer(ctx context.Context, in *DeleteLoadBalancerInput) (*DeleteLoadBalancerOutput, error) {
	const op = "globalloadbalancer.DeleteLoadBalancer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := c.writeProvenance(); err != nil {
		return nil, err
	}
	got, err := c.writeDetail(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	if got.ID != in.LoadBalancerID {
		return nil, fmt.Errorf("%w: preflight ID mismatch", core.ErrInvalidInput)
	}
	out := &DeleteLoadBalancerOutput{LoadBalancerID: in.LoadBalancerID}
	notSent := false
	err = c.c.DoJSON(ctx, transport.Request{Operation: op, Method: http.MethodDelete, URL: c.glbURL([]string{"global-load-balancers", in.LoadBalancerID}, nil), OK: []int{200, 202}, Once: true, NoRedirect: true, Sensitive: true, NotSent: &notSent, WithholdMessage: "response withheld"}, nil)
	if err != nil {
		if definitiveWriteError(err, notSent) {
			return nil, err
		}
		return out, fmt.Errorf("%w: delete request unconfirmed", ErrNotSettled)
	}
	wait, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		current, err := c.writeDetail(wait, in.LoadBalancerID)
		if core.IsNotFound(err) {
			out.Deleted = true
			return out, nil
		}
		if err == nil && current.ID != in.LoadBalancerID {
			return out, fmt.Errorf("%w: delete detail ID mismatch", ErrNotSettled)
		}
		if err != nil || !pause(wait) {
			return out, fmt.Errorf("%w: delete confirmation interrupted", ErrNotSettled)
		}
	}
}

func (c *Client) writeProvenance() error {
	expected := endpoints.ResolveIAMUser(c.c.Region(), endpoints.Overrides{})
	if !c.c.UsesIAMUserLogin() || !c.c.UsesDefaultAuthEndpoints() || c.c.Endpoint(routes.ProductGLB) != expected.GLB || c.c.Endpoint(routes.ProductDashboard) != expected.Dashboard {
		return fmt.Errorf("%w: GLB writes require SDK IAM-user login and default endpoints", core.ErrInvalidConfig)
	}
	return nil
}

func (c *Client) writeDetail(ctx context.Context, id string) (LoadBalancer, error) {
	var wire struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Package string `json:"package"`
		Status  string `json:"status"`
	}
	err := c.c.DoJSON(ctx, transport.Request{Operation: "globalloadbalancer.WriteDetail", Method: http.MethodGet, URL: c.glbURL([]string{"global-load-balancers", id}, nil), OK: []int{200}, NoRedirect: true, StrictJSON: true, Sensitive: true, WithholdMessage: "response withheld"}, &wire)
	return LoadBalancer{ID: wire.ID, Name: wire.Name, Package: wire.Package, Status: wire.Status}, err
}

var unsafeProviderMessage = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|bearer|credential|cookie|session|api[-_ ]?key|\b(?:account|user|project|resource|vpc|subnet|uuid|id|[a-z]+id)\b|\b[a-z][a-z0-9]*-[a-z0-9]*[0-9][a-z0-9-]*\b|\b[0-9]{8,}\b|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|https?://|[\w.+-]+@[\w.-]+|\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b|[\x00-\x1f\x7f])`)

func sanitizeCreateError(err error, in *CreateLoadBalancerInput, credentials []string) error {
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode < 400 || api.StatusCode >= 500 {
		return err
	}
	safe := *api
	safe.Code = core.ResolvedCode(api.StatusCode, "")
	if len(api.Message) > 512 || unsafeProviderMessage.MatchString(api.Message) {
		safe.Message = "provider response withheld"
	} else {
		values := append([]string{in.Name, in.PackageID, in.Description}, credentials...)
		if in.Listener != nil {
			values = append(values, in.Listener.Name)
			values = append(values, in.Listener.AllowedCIDRs...)
		}
		if in.Pool != nil {
			values = append(values, in.Pool.Name)
			for _, m := range in.Pool.Members {
				values = append(values, m.Region, m.VPCID, m.SubnetID, m.Address)
			}
		}
		safe.Message = transport.RedactErrorText(api.Message, values...)
	}
	return &safe
}

func definitiveWriteError(err error, notSent bool) bool {
	if notSent {
		return true
	}
	var api *vngcloud.APIError
	return errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode < 500
}

func pause(ctx context.Context) bool {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
