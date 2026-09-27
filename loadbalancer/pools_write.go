package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreatePoolInput's Protocol values.
const (
	PoolProtocolHTTP  = "HTTP"
	PoolProtocolTCP   = "TCP"
	PoolProtocolUDP   = "UDP"
	PoolProtocolPROXY = "PROXY"
)

// CreatePoolInput's Algorithm values. Empty sends AlgorithmRoundRobin.
const (
	AlgorithmRoundRobin      = "ROUND_ROBIN"
	AlgorithmLeastConnection = "LEAST_CONNECTIONS"
	AlgorithmSourceIP        = "SOURCE_IP"
)

// CreatePoolInput's HealthCheckProtocol values.
const (
	HealthCheckProtocolTCP     = "TCP"
	HealthCheckProtocolHTTP    = "HTTP"
	HealthCheckProtocolHTTPS   = "HTTPS"
	HealthCheckProtocolPingUDP = "PING-UDP"
)

// Defaults CreatePool sends when the matching field is left at its zero
// value, per the design.
const (
	defaultAlgorithm           = AlgorithmRoundRobin
	defaultHealthyThreshold    = 3
	defaultUnhealthyThreshold  = 3
	defaultHealthCheckInterval = 30
	defaultHealthCheckTimeout  = 5
)

// isHTTPHealthCheck reports whether protocol is one of the two health check
// protocols that carry the HTTP fields (HealthCheckPath, HealthCheckMethod,
// HealthCheckHTTPVersion, HealthCheckDomainName, HealthCheckSuccessCode).
func isHTTPHealthCheck(protocol string) bool {
	return protocol == HealthCheckProtocolHTTP || protocol == HealthCheckProtocolHTTPS
}

// poolHealthMonitorBody is the healthMonitor object CreatePool and
// UpdatePool both send. HealthCheckProtocol is fixed at create and has no
// field in an update body, so UpdatePool leaves it empty; omitempty then
// drops it from that request only.
type poolHealthMonitorBody struct {
	HealthCheckProtocol string `json:"healthCheckProtocol,omitempty"`
	HealthyThreshold    int    `json:"healthyThreshold"`
	UnhealthyThreshold  int    `json:"unhealthyThreshold"`
	Interval            int    `json:"interval"`
	Timeout             int    `json:"timeout"`
	HealthCheckPath     string `json:"healthCheckPath,omitempty"`
	HealthCheckMethod   string `json:"healthCheckMethod,omitempty"`
	HTTPVersion         string `json:"httpVersion,omitempty"`
	DomainName          string `json:"domainName,omitempty"`
	SuccessCode         string `json:"successCode,omitempty"`
}

// createPoolBody is CreatePool's request body.
type createPoolBody struct {
	Name          string                `json:"poolName"`
	Protocol      string                `json:"poolProtocol"`
	Algorithm     string                `json:"algorithm"`
	Stickiness    *bool                 `json:"stickiness,omitempty"`
	TLSEncryption *bool                 `json:"tlsEncryption,omitempty"`
	HealthMonitor poolHealthMonitorBody `json:"healthMonitor"`
}

// CreatePoolInput creates a pool with its health monitor. Empty Algorithm
// sends AlgorithmRoundRobin; a zero HealthyThreshold, UnhealthyThreshold,
// HealthCheckInterval, or HealthCheckTimeout sends the server's own default
// (3, 3, 30, and 5). Stickiness and TLSEncryption are sent only when set,
// since a Layer 4 pool has no use for either.
//
// The HTTP health check fields (HealthCheckPath, HealthCheckMethod,
// HealthCheckHTTPVersion, HealthCheckDomainName, HealthCheckSuccessCode) are
// sent only when HealthCheckProtocol is HealthCheckProtocolHTTP or
// HealthCheckProtocolHTTPS; setting any of them with a different
// HealthCheckProtocol is core.ErrInvalidInput, before any request. The SDK
// never invents a HealthCheckDomainName for HTTP/1.1: an HTTP check with no
// domain name reaches the server empty, which then refuses it.
type CreatePoolInput struct {
	LoadBalancerID string `vngcloud:"required"`
	Name           string `vngcloud:"required"`
	// Protocol is one of the PoolProtocol constants.
	Protocol  string `vngcloud:"required"`
	Algorithm string

	Stickiness    *bool
	TLSEncryption *bool

	// HealthCheckProtocol is one of the HealthCheckProtocol constants.
	HealthCheckProtocol    string `vngcloud:"required"`
	HealthCheckPath        string
	HealthCheckMethod      string
	HealthCheckHTTPVersion string
	HealthCheckDomainName  string
	HealthCheckSuccessCode string
	HealthyThreshold       int
	UnhealthyThreshold     int
	HealthCheckInterval    int
	HealthCheckTimeout     int

	NoWait bool
}

type CreatePoolOutput struct {
	Pool Pool
}

// checkPoolHTTPFields returns core.ErrInvalidInput, naming the first field
// found set, when healthCheckProtocol is not HTTP or HTTPS but any of the
// five HTTP health check fields is non-empty.
func checkPoolHTTPFields(op, healthCheckProtocol string, path, method, httpVersion, domainName, successCode string) error {
	if isHTTPHealthCheck(healthCheckProtocol) {
		return nil
	}
	for _, f := range [...]struct{ name, value string }{
		{"HealthCheckPath", path},
		{"HealthCheckMethod", method},
		{"HealthCheckHTTPVersion", httpVersion},
		{"HealthCheckDomainName", domainName},
		{"HealthCheckSuccessCode", successCode},
	} {
		if f.value != "" {
			return fmt.Errorf("%w: %s: %s is only valid when HealthCheckProtocol is HTTP or HTTPS, got %q",
				core.ErrInvalidInput, op, f.name, healthCheckProtocol)
		}
	}
	return nil
}

// buildPoolHealthMonitorBody builds the healthMonitor object CreatePool
// sends, applying the design's defaults and including the HTTP fields only
// for an HTTP or HTTPS check. protocol is included; UpdatePool builds its
// own body directly, since its wire shape omits the protocol.
func buildPoolHealthMonitorBody(protocol, path, method, httpVersion, domainName, successCode string, healthyThreshold, unhealthyThreshold, interval, timeout int) poolHealthMonitorBody {
	if healthyThreshold == 0 {
		healthyThreshold = defaultHealthyThreshold
	}
	if unhealthyThreshold == 0 {
		unhealthyThreshold = defaultUnhealthyThreshold
	}
	if interval == 0 {
		interval = defaultHealthCheckInterval
	}
	if timeout == 0 {
		timeout = defaultHealthCheckTimeout
	}
	body := poolHealthMonitorBody{
		HealthCheckProtocol: protocol,
		HealthyThreshold:    healthyThreshold,
		UnhealthyThreshold:  unhealthyThreshold,
		Interval:            interval,
		Timeout:             timeout,
	}
	if isHTTPHealthCheck(protocol) {
		body.HealthCheckPath = path
		body.HealthCheckMethod = method
		body.HTTPVersion = httpVersion
		body.DomainName = domainName
		body.SuccessCode = successCode
	}
	return body
}

// CreatePool creates a pool with its health monitor on a load balancer. It
// waits, within the pre-write bound, until the load balancer is not busy
// (ErrBusy, nothing sent, past that bound), then sends the create.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// pool may have been created, and the caller checks with list-pools and
// matches Name exactly before creating it again. A response with no uuid is
// the same kind of error.
//
// Without NoWait, CreatePool then waits, within the child bound (5-second
// poll, 10-minute bound), for the pool's progressStatus to reach CREATED
// while the load balancer is no longer busy. If the pool or the load
// balancer reaches ERROR, the returned error wraps ErrFailed; if the bound
// runs out, or a read or a sleep fails, it wraps ErrNotSettled. Either way
// the Output is never nil: its Pool is the last one a read returned, or, if
// none did, the fields the create itself sent. NoWait skips this wait and
// returns that same fallback at once.
func (c *Client) CreatePool(ctx context.Context, in *CreatePoolInput) (*CreatePoolOutput, error) {
	const op = "loadbalancer.CreatePool"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := checkPoolHTTPFields(op, in.HealthCheckProtocol, in.HealthCheckPath, in.HealthCheckMethod, in.HealthCheckHTTPVersion, in.HealthCheckDomainName, in.HealthCheckSuccessCode); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := c.waitLoadBalancerPreWriteReady(ctx, op, in.LoadBalancerID); err != nil {
		return nil, err
	}

	algorithm := in.Algorithm
	if algorithm == "" {
		algorithm = defaultAlgorithm
	}
	body := createPoolBody{
		Name:          in.Name,
		Protocol:      in.Protocol,
		Algorithm:     algorithm,
		Stickiness:    in.Stickiness,
		TLSEncryption: in.TLSEncryption,
		HealthMonitor: buildPoolHealthMonitorBody(in.HealthCheckProtocol, in.HealthCheckPath, in.HealthCheckMethod, in.HealthCheckHTTPVersion, in.HealthCheckDomainName, in.HealthCheckSuccessCode, in.HealthyThreshold, in.UnhealthyThreshold, in.HealthCheckInterval, in.HealthCheckTimeout),
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		UUID string `json:"uuid"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "pools"}, nil),
		Body:      body,
		OK:        httpStatusOKCreate,
	}
	var status int
	sendErr := sendWithBusyResend(ctx,
		func(ctx context.Context) error { return c.waitLoadBalancerPreWriteReady(ctx, op, in.LoadBalancerID) },
		func() error {
			var err error
			status, err = c.c.DoJSONStatus(ctx, req, &resp)
			return err
		},
	)
	if sendErr != nil {
		return nil, wrapAmbiguousCreateErr(op, "list-pools", sendErr)
	}
	if resp.UUID == "" {
		return nil, errCreateResponseNoID(op, status, "list-pools")
	}

	fallback := Pool{UUID: resp.UUID, Name: in.Name, Protocol: in.Protocol, LoadBalanceMethod: algorithm}
	if in.NoWait {
		return &CreatePoolOutput{Pool: fallback}, nil
	}

	var pool *Pool
	waitErr := c.waitChildSettled(ctx, op, in.LoadBalancerID, "pool", resp.UUID, func(ctx context.Context) (string, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: in.LoadBalancerID, PoolID: resp.UUID})
		if err != nil {
			if core.IsNotFound(err) {
				return "", nil
			}
			return "", err
		}
		pool = &out.Pool
		return pool.ProgressStatus, nil
	})
	if pool == nil {
		pool = &fallback
	}
	return &CreatePoolOutput{Pool: *pool}, waitErr
}

// UpdatePoolInput changes a pool's algorithm, health monitor, stickiness, or
// TLS encryption; a nil field keeps its current value. The health check
// protocol cannot change after create, so there is no field for it here.
type UpdatePoolInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`

	Algorithm     *string
	Stickiness    *bool
	TLSEncryption *bool

	HealthCheckPath        *string
	HealthCheckMethod      *string
	HealthCheckHTTPVersion *string
	HealthCheckDomainName  *string
	HealthCheckSuccessCode *string
	HealthyThreshold       *int
	UnhealthyThreshold     *int
	HealthCheckInterval    *int
	HealthCheckTimeout     *int

	NoWait bool
}

type UpdatePoolOutput struct {
	Pool Pool
}

// updatePoolAnySet reports whether in sets at least one field, so
// UpdatePool can refuse a no-op update before any request.
func updatePoolAnySet(in *UpdatePoolInput) bool {
	return in.Algorithm != nil || in.Stickiness != nil || in.TLSEncryption != nil ||
		in.HealthCheckPath != nil || in.HealthCheckMethod != nil || in.HealthCheckHTTPVersion != nil ||
		in.HealthCheckDomainName != nil || in.HealthCheckSuccessCode != nil ||
		in.HealthyThreshold != nil || in.UnhealthyThreshold != nil ||
		in.HealthCheckInterval != nil || in.HealthCheckTimeout != nil
}

// stringOr returns *p when p is non-nil, else fallback.
func stringOr(p *string, fallback string) string {
	if p != nil {
		return *p
	}
	return fallback
}

// intOr returns *p when p is non-nil, else fallback.
func intOr(p *int, fallback int) int {
	if p != nil {
		return *p
	}
	return fallback
}

// UpdatePool changes a pool. At least one field must be set, checked before
// any request (core.ErrInvalidInput). It waits, within the pre-write bound,
// until the load balancer and the pool are both not busy (ErrBusy, nothing
// sent, past that bound), reads the pool and its health monitor, applies
// every set field, and sends the full body with the read values for the
// rest: an update with only Algorithm set still resends the health monitor
// exactly as read, and vice versa. The HTTP health check fields (see
// CreatePool's doc comment) are refused when the pool's own
// HealthCheckProtocol, fixed at create and read back from the health
// monitor, is not HTTP or HTTPS.
//
// The PUT keeps the transport's normal retries: resending the same full
// body is safe. Without NoWait, UpdatePool then waits exactly as CreatePool
// does; its Output follows the same rule.
func (c *Client) UpdatePool(ctx context.Context, in *UpdatePoolInput) (*UpdatePoolOutput, error) {
	const op = "loadbalancer.UpdatePool"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PoolID", in.PoolID); err != nil {
		return nil, err
	}
	if !updatePoolAnySet(in) {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// The busy wait reads only the pool, not its health monitor: fetching
	// the monitor on every poll while the pool stays busy would waste a
	// request per iteration for no benefit, since it is read again, once,
	// right after the wait succeeds.
	pool, err := waitPreWriteReady(c, ctx, op, in.LoadBalancerID, "pool", in.PoolID, func(ctx context.Context) (*Pool, string, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
		if err != nil {
			return nil, "", err
		}
		return &out.Pool, out.Pool.ProgressStatus, nil
	})
	if err != nil {
		return nil, err
	}
	hm, err := c.GetPoolHealthMonitor(ctx, &GetPoolHealthMonitorInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
	if err != nil {
		return nil, err
	}
	monitor := &hm.HealthMonitor

	if err := checkPoolHTTPFields(op, monitor.HealthCheckProtocol,
		stringOr(in.HealthCheckPath, ""), stringOr(in.HealthCheckMethod, ""), stringOr(in.HealthCheckHTTPVersion, ""),
		stringOr(in.HealthCheckDomainName, ""), stringOr(in.HealthCheckSuccessCode, "")); err != nil {
		// Only the fields the caller is setting are checked here: a field
		// left nil keeps the monitor's own read value, which already
		// satisfied this rule when it was written.
		if in.HealthCheckPath != nil || in.HealthCheckMethod != nil || in.HealthCheckHTTPVersion != nil ||
			in.HealthCheckDomainName != nil || in.HealthCheckSuccessCode != nil {
			return nil, err
		}
	}

	algorithm := stringOr(in.Algorithm, pool.LoadBalanceMethod)
	// Stickiness and TLSEncryption are sent only when set (design), even on
	// an update: an unset field that read false is left out of the body
	// rather than resent as an explicit false, since a Layer 4 pool may
	// reject either key outright. A read of true is carried forward so an
	// update that touches other fields never silently turns either off.
	stickiness := in.Stickiness
	if stickiness == nil && pool.Stickiness {
		stickiness = &pool.Stickiness
	}
	tlsEncryption := in.TLSEncryption
	if tlsEncryption == nil && pool.TLSEncryption {
		tlsEncryption = &pool.TLSEncryption
	}

	readPath := deref(monitor.HealthCheckPath)
	readMethod := deref(monitor.HealthCheckMethod)
	readHTTPVersion := deref(monitor.HTTPVersion)
	readDomainName := deref(monitor.DomainName)
	readSuccessCode := deref(monitor.SuccessCode)

	body := createPoolBody{
		Algorithm:     algorithm,
		Stickiness:    stickiness,
		TLSEncryption: tlsEncryption,
		HealthMonitor: poolHealthMonitorBody{
			HealthyThreshold:   intOr(in.HealthyThreshold, monitor.HealthyThreshold),
			UnhealthyThreshold: intOr(in.UnhealthyThreshold, monitor.UnhealthyThreshold),
			Interval:           intOr(in.HealthCheckInterval, monitor.Interval),
			Timeout:            intOr(in.HealthCheckTimeout, monitor.Timeout),
		},
	}
	if isHTTPHealthCheck(monitor.HealthCheckProtocol) {
		body.HealthMonitor.HealthCheckPath = stringOr(in.HealthCheckPath, readPath)
		body.HealthMonitor.HealthCheckMethod = stringOr(in.HealthCheckMethod, readMethod)
		body.HealthMonitor.HTTPVersion = stringOr(in.HealthCheckHTTPVersion, readHTTPVersion)
		body.HealthMonitor.DomainName = stringOr(in.HealthCheckDomainName, readDomainName)
		body.HealthMonitor.SuccessCode = stringOr(in.HealthCheckSuccessCode, readSuccessCode)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "pools", in.PoolID}, nil),
		Body:      body,
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, poolBusyWaiter(c, op, in.LoadBalancerID, in.PoolID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, err
	}

	if in.NoWait {
		fallback := *pool
		fallback.LoadBalanceMethod = algorithm
		return &UpdatePoolOutput{Pool: fallback}, nil
	}

	var settled *Pool
	waitErr := c.waitChildSettled(ctx, op, in.LoadBalancerID, "pool", in.PoolID, func(ctx context.Context) (string, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
		if err != nil {
			return "", err
		}
		settled = &out.Pool
		return settled.ProgressStatus, nil
	})
	if settled == nil {
		settled = pool
	}
	return &UpdatePoolOutput{Pool: *settled}, waitErr
}

// deref returns *p, or "" when p is nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// poolBusyWaiter returns sendWithBusyResend's waitBusy function for a write
// already targeting an existing pool: waiting again for the load balancer
// and the pool to both go idle, the same check UpdatePool and DeletePool
// already ran once before their first send.
func poolBusyWaiter(c *Client, op, lbID, poolID string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := waitPreWriteReady(c, ctx, op, lbID, "pool", poolID, func(ctx context.Context) (*Pool, string, error) {
			out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: lbID, PoolID: poolID})
			if err != nil {
				return nil, "", err
			}
			return &out.Pool, out.Pool.ProgressStatus, nil
		})
		return err
	}
}

// DeletePoolInput identifies the pool to delete.
type DeletePoolInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`

	NoWait bool
}

type DeletePoolOutput struct{}

// DeletePool deletes a pool. It lists the load balancer's listeners first
// and returns ErrInUse, sending nothing, when one names PoolID as its
// DefaultPoolID: the server refuses that delete outright, and this avoids
// the round trip. It does not scan for a policy that redirects to the pool;
// the server's own refusal covers that case (see below).
//
// It then waits, within the pre-write bound, until the load balancer and
// the pool are both not busy (ErrBusy, nothing sent, past that bound), and
// sends the DELETE. An API error whose message contains "is used in
// listener" (case-insensitive), whatever its status, also wraps ErrInUse.
//
// DELETE is idempotent and keeps the transport's normal retries; a retry
// that finds the pool already gone returns core.ErrNotFound. Without
// NoWait, DeletePool then waits, within the child bound, for the pool to
// 404 while the load balancer is no longer busy. If the load balancer
// reaches ERROR, the returned error wraps ErrFailed; if the bound runs out,
// or a read or a sleep fails, it wraps ErrNotSettled, whose message says a
// rerun is safe: DeletePool always reads first.
func (c *Client) DeletePool(ctx context.Context, in *DeletePoolInput) (*DeletePoolOutput, error) {
	const op = "loadbalancer.DeletePool"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PoolID", in.PoolID); err != nil {
		return nil, err
	}

	listeners, err := c.ListListeners(ctx, &ListListenersInput{LoadBalancerID: in.LoadBalancerID})
	if err != nil {
		return nil, err
	}
	for _, l := range listeners.Items {
		if l.DefaultPoolID == in.PoolID {
			return nil, fmt.Errorf("%w: %s: pool %s is used in listener %s as its default pool", ErrInUse, op, in.PoolID, l.UUID)
		}
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if _, err := waitPreWriteReady(c, ctx, op, in.LoadBalancerID, "pool", in.PoolID, func(ctx context.Context) (struct{}, string, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
		if err != nil {
			return struct{}{}, "", err
		}
		return struct{}{}, out.Pool.ProgressStatus, nil
	}); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "pools", in.PoolID}, nil),
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, poolBusyWaiter(c, op, in.LoadBalancerID, in.PoolID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, wrapPoolInUse(op, in.PoolID, err)
	}

	if in.NoWait {
		return &DeletePoolOutput{}, nil
	}
	err = c.waitChildDeleted(ctx, op, in.LoadBalancerID, "pool", in.PoolID, func(ctx context.Context) (string, bool, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
		if err != nil {
			if core.IsNotFound(err) {
				return "", true, nil
			}
			return "", false, err
		}
		return out.Pool.ProgressStatus, false, nil
	})
	return &DeletePoolOutput{}, err
}

// wrapPoolInUse rewraps err with ErrInUse when it is a *core.APIError whose
// message contains "is used in listener" (case-insensitive), whatever its
// status. Any other error passes through unchanged.
func wrapPoolInUse(op, poolID string, err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Message), "is used in listener") {
		return fmt.Errorf("%w: %s: pool %s: %w", ErrInUse, op, poolID, apiErr)
	}
	return err
}
