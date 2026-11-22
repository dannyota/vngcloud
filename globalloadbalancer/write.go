package globalloadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

//nolint:revive
var ErrFailed = errors.New("globalloadbalancer: write failed; inspect inventory and billing before another mutation")

// CreateLoadBalancerInput selects a global load balancer configuration.
type CreateLoadBalancerInput struct {
	// Name is required.
	Name string `vngcloud:"required"`
	// PackageID is required.
	PackageID string `vngcloud:"required"`
	// Description is optional.
	Description string
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
}

func buildCreate(in *CreateLoadBalancerInput) (createBody, error) {
	const op = "globalloadbalancer.CreateLoadBalancer"
	var body createBody
	if err := core.CheckRequired(op, in); err != nil {
		return body, err
	}
	body = createBody{Name: in.Name, Package: in.PackageID, Description: in.Description, PaymentFlow: "automated", Type: "Layer 4"}
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
			if lb.ID != id || lb.Name != body.Name || lb.Package != body.Package || lb.Type != "Layer 4" {
				return out, fmt.Errorf("%w: create detail does not match", ErrNotSettled)
			}
			switch strings.ToUpper(lb.Status) {
			case "ACTIVE":
				out.LoadBalancer = lb
				return out, nil
			case "ERROR":
				return out, ErrFailed
			case "CREATING", "PENDING", "PROVISIONING", "UPDATING":
				out.LoadBalancer = lb
			default:
				return out, fmt.Errorf("%w: create status unrecognized", ErrNotSettled)
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
	if got.ID != in.LoadBalancerID || got.Type != "Layer 4" || (!strings.EqualFold(got.Status, "ACTIVE") && !strings.EqualFold(got.Status, "ERROR")) {
		return nil, fmt.Errorf("%w: preflight resource is not eligible for deletion", core.ErrInvalidInput)
	}
	out := &DeleteLoadBalancerOutput{LoadBalancerID: in.LoadBalancerID}
	notSent := false
	var sentCredentials []string
	err = c.c.DoJSON(ctx, transport.Request{Operation: op, Method: http.MethodDelete, URL: c.glbURL([]string{"global-load-balancers", in.LoadBalancerID}, nil), OK: []int{200, 202}, Once: true, NoRedirect: true, Sensitive: true, KeepObservedStatus: true, NotSent: &notSent, SentCredentials: &sentCredentials}, nil)
	if err != nil {
		if definitiveWriteError(err, notSent) {
			return nil, sanitizeCreateError(err, nil, sentCredentials)
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
		Type    string `json:"type"`
	}
	err := c.c.DoJSON(ctx, transport.Request{Operation: "globalloadbalancer.WriteDetail", Method: http.MethodGet, URL: c.glbURL([]string{"global-load-balancers", id}, nil), OK: []int{200}, NoRedirect: true, StrictJSON: true, Sensitive: true, WithholdMessage: "response withheld"}, &wire)
	return LoadBalancer{ID: wire.ID, Name: wire.Name, Package: wire.Package, Status: wire.Status, Type: wire.Type}, sanitizeCreateError(err, nil, nil)
}

var unsafeProviderMessage = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|(?:access|refresh)?token|authorization|bearer|credential|cookie|session|api[-_ ]?key)\b\s*(?:[:=]|\bis\b)\s*[^\s,;]+|\bbearer\s+[^\s,;]+|\b[a-z][a-z0-9]*-[a-z0-9]*[0-9][a-z0-9-]*\b|\b[0-9]{8,}\b|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[\w.+-]+@[\w.-]+\.[a-z]{2,}|[\x00-\x1f\x7f])`)

func sanitizeCreateError(err error, in *CreateLoadBalancerInput, credentials []string) error {
	var api *vngcloud.APIError
	if !errors.As(err, &api) {
		return err
	}
	safe := *api
	safe.Code = core.ResolvedCode(api.StatusCode, "")
	if len(api.Message) > 512 || unsafeProviderMessage.MatchString(api.Message) {
		safe.Message = "provider response withheld"
	} else {
		values := append([]string(nil), credentials...)
		if in != nil {
			values = append(values, in.Name, in.PackageID, in.Description)
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
