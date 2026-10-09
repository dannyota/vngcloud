package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreateListenerInput's Protocol values.
const (
	ProtocolHTTP  = "HTTP"
	ProtocolHTTPS = "HTTPS"
	ProtocolTCP   = "TCP"
	ProtocolUDP   = "UDP"
)

// Timeout defaults CreateListener sends when the matching field is left at
// 0, in seconds.
const (
	defaultTimeoutClient     = 50
	defaultTimeoutMember     = 50
	defaultTimeoutConnection = 5
)

// checkAllowedCIDR returns core.ErrInvalidInput unless cidr parses as an
// IPv4 prefix with no host bits set.
func checkAllowedCIDR(op, cidr string) error {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("%w: %s: AllowedCIDRs entry must be an IPv4 CIDR prefix such as 10.0.0.0/24, got %q", core.ErrInvalidInput, op, cidr)
	}
	if prefix != prefix.Masked() {
		return fmt.Errorf("%w: %s: AllowedCIDRs entry must have no host bits set; got %q, want %q", core.ErrInvalidInput, op, cidr, prefix.Masked())
	}
	return nil
}

// checkAllowedCIDRs checks every entry of cidrs with checkAllowedCIDR and
// refuses an empty list.
func checkAllowedCIDRs(op string, cidrs []string) error {
	if len(cidrs) == 0 {
		return fmt.Errorf("%w: %s requires at least one AllowedCIDRs entry", core.ErrInvalidInput, op)
	}
	for _, cidr := range cidrs {
		if err := checkAllowedCIDR(op, cidr); err != nil {
			return err
		}
	}
	return nil
}

// checkListenerCertificateFields returns core.ErrInvalidInput when protocol
// is ProtocolHTTPS but defaultCertificateID is empty, or when protocol is
// anything else but any of the three certificate fields is set: a
// certificate never attaches where the server has no use for it.
func checkListenerCertificateFields(op, protocol string, certificateIDs []string, defaultCertificateID, clientCertificateID string) error {
	if protocol == ProtocolHTTPS {
		if defaultCertificateID == "" {
			return fmt.Errorf("%w: %s: DefaultCertificateID is required when Protocol is %s", core.ErrInvalidInput, op, ProtocolHTTPS)
		}
		return nil
	}
	if len(certificateIDs) > 0 || defaultCertificateID != "" || clientCertificateID != "" {
		return fmt.Errorf("%w: %s: CertificateIDs, DefaultCertificateID, and ClientCertificateID are only valid when Protocol is %s, got %q",
			core.ErrInvalidInput, op, ProtocolHTTPS, protocol)
	}
	return nil
}

// checkListenerCertificateIDs runs core.CheckPathID on every non-empty
// certificate id: each names a resource, per the design.
func checkListenerCertificateIDs(op string, certificateIDs []string, defaultCertificateID, clientCertificateID string) error {
	for _, id := range certificateIDs {
		if err := core.CheckPathID(op, "CertificateIDs", id); err != nil {
			return err
		}
	}
	if defaultCertificateID != "" {
		if err := core.CheckPathID(op, "DefaultCertificateID", defaultCertificateID); err != nil {
			return err
		}
	}
	if clientCertificateID != "" {
		if err := core.CheckPathID(op, "ClientCertificateID", clientCertificateID); err != nil {
			return err
		}
	}
	return nil
}

// joinCIDRs and splitCIDRs convert AllowedCIDRs between the SDK's []string
// and the wire's single comma-separated string.
func joinCIDRs(cidrs []string) string { return strings.Join(cidrs, ",") }
func splitCIDRs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// listenerWriteBody is CreateListener and UpdateListener's shared body:
// everything but the name, protocol, and port a create alone carries,
// since an update cannot change any of the three. It never carries
// blockedCidrs, defaultAction, alpnProtocols, or tlsSecurityPolicy: the
// listener read returns none of them, so a value this SDK cannot read back
// would be silently wiped by the next update; see the design's non-goals.
type listenerWriteBody struct {
	TimeoutClient               int                    `json:"timeoutClient"`
	TimeoutMember               int                    `json:"timeoutMember"`
	TimeoutConnection           int                    `json:"timeoutConnection"`
	DefaultPoolID               string                 `json:"defaultPoolId,omitempty"`
	AllowedCIDRs                string                 `json:"allowedCidrs"`
	CertificateAuthorities      []string               `json:"certificateAuthorities,omitempty"`
	DefaultCertificateAuthority string                 `json:"defaultCertificateAuthority,omitempty"`
	ClientCertificate           string                 `json:"clientCertificate,omitempty"`
	InsertHeaders               []ListenerInsertHeader `json:"insertHeaders,omitempty"`
}

// createListenerBody is CreateListener's request body.
type createListenerBody struct {
	Name     string `json:"listenerName"`
	Protocol string `json:"listenerProtocol"`
	Port     int    `json:"listenerProtocolPort"`
	listenerWriteBody
}

// CreateListenerInput creates a listener. AllowedCIDRs is required with no
// default, unlike VNG Cloud's SDK, which sends 0.0.0.0/0: an open listener
// on an internet load balancer serves the whole internet, so the caller
// names its own CIDR list. Each entry must parse as an IPv4 CIDR prefix
// with no host bits set; the SDK joins them with commas on the wire.
//
// A TimeoutClient, TimeoutMember, or TimeoutConnection of 0 sends the
// server's own default (50, 50, and 5 seconds).
//
// Protocol ProtocolHTTPS requires DefaultCertificateID; any other Protocol
// refuses CertificateIDs, DefaultCertificateID, and ClientCertificateID
// all being set, core.ErrInvalidInput before any request. The SDK never
// reads the certificate itself; the server checks that it exists.
type CreateListenerInput struct {
	LoadBalancerID string `vngcloud:"required"`
	Name           string `vngcloud:"required"`
	// Protocol is one of ProtocolHTTP, ProtocolHTTPS, ProtocolTCP, or
	// ProtocolUDP.
	Protocol     string   `vngcloud:"required"`
	Port         int      `vngcloud:"required"`
	AllowedCIDRs []string `vngcloud:"required"`

	DefaultPoolID     string
	TimeoutClient     int
	TimeoutMember     int
	TimeoutConnection int

	CertificateIDs       []string
	DefaultCertificateID string
	ClientCertificateID  string

	InsertHeaders []ListenerInsertHeader

	NoWait bool
}

type CreateListenerOutput struct {
	Listener Listener
}

// timeoutOrDefault returns timeout, or fallback when timeout is 0.
func timeoutOrDefault(timeout, fallback int) int {
	if timeout == 0 {
		return fallback
	}
	return timeout
}

// CreateListener creates a listener on a load balancer. It waits, within
// the pre-write bound, until the load balancer is not busy (ErrBusy,
// nothing sent, past that bound), then sends the create.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// listener may have been created, and the caller checks with
// list-listeners and matches Name exactly before creating it again. A
// response with no uuid is the same kind of error.
//
// Without NoWait, CreateListener then waits, within the child bound, for
// the listener's progressStatus to reach CREATED while the load balancer is
// no longer busy, the same wait CreatePool's doc comment describes; its
// Output follows the same fallback rule.
func (c *Client) CreateListener(ctx context.Context, in *CreateListenerInput) (*CreateListenerOutput, error) {
	const op = "loadbalancer.CreateListener"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if in.DefaultPoolID != "" {
		if err := core.CheckPathID(op, "DefaultPoolID", in.DefaultPoolID); err != nil {
			return nil, err
		}
	}
	if err := checkAllowedCIDRs(op, in.AllowedCIDRs); err != nil {
		return nil, err
	}
	if err := checkListenerCertificateFields(op, in.Protocol, in.CertificateIDs, in.DefaultCertificateID, in.ClientCertificateID); err != nil {
		return nil, err
	}
	if err := checkListenerCertificateIDs(op, in.CertificateIDs, in.DefaultCertificateID, in.ClientCertificateID); err != nil {
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

	body := createListenerBody{
		Name:     in.Name,
		Protocol: in.Protocol,
		Port:     in.Port,
		listenerWriteBody: listenerWriteBody{
			TimeoutClient:               timeoutOrDefault(in.TimeoutClient, defaultTimeoutClient),
			TimeoutMember:               timeoutOrDefault(in.TimeoutMember, defaultTimeoutMember),
			TimeoutConnection:           timeoutOrDefault(in.TimeoutConnection, defaultTimeoutConnection),
			DefaultPoolID:               in.DefaultPoolID,
			AllowedCIDRs:                joinCIDRs(in.AllowedCIDRs),
			CertificateAuthorities:      in.CertificateIDs,
			DefaultCertificateAuthority: in.DefaultCertificateID,
			ClientCertificate:           in.ClientCertificateID,
			InsertHeaders:               in.InsertHeaders,
		},
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
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "listeners"}, nil),
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
		return nil, wrapAmbiguousCreateErr(op, "list-listeners", sendErr)
	}
	if resp.UUID == "" {
		return nil, errCreateResponseNoID(op, status, "list-listeners")
	}

	fallback := Listener{UUID: resp.UUID, Name: in.Name, Protocol: in.Protocol, ProtocolPort: in.Port}
	if in.NoWait {
		return &CreateListenerOutput{Listener: fallback}, nil
	}

	var listener *Listener
	waitErr := c.waitChildSettled(ctx, op, in.LoadBalancerID, "listener", resp.UUID, func(ctx context.Context) (string, error) {
		out, err := c.GetListener(ctx, &GetListenerInput{LoadBalancerID: in.LoadBalancerID, ListenerID: resp.UUID})
		if err != nil {
			if core.IsNotFound(err) {
				return "", nil
			}
			return "", err
		}
		listener = &out.Listener
		return listener.ProgressStatus, nil
	})
	if listener == nil {
		listener = &fallback
	}
	return &CreateListenerOutput{Listener: *listener}, waitErr
}

// UpdateListenerInput changes a listener; a nil field keeps its current
// value. Name, Protocol, and Port cannot change after create.
type UpdateListenerInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`

	DefaultPoolID     *string
	TimeoutClient     *int
	TimeoutMember     *int
	TimeoutConnection *int
	AllowedCIDRs      *[]string

	CertificateIDs       *[]string
	DefaultCertificateID *string
	ClientCertificateID  *string

	InsertHeaders *[]ListenerInsertHeader

	NoWait bool
}

type UpdateListenerOutput struct {
	Listener Listener
}

// updateListenerAnySet reports whether in sets at least one field.
func updateListenerAnySet(in *UpdateListenerInput) bool {
	return in.DefaultPoolID != nil || in.TimeoutClient != nil || in.TimeoutMember != nil || in.TimeoutConnection != nil ||
		in.AllowedCIDRs != nil || in.CertificateIDs != nil || in.DefaultCertificateID != nil || in.ClientCertificateID != nil ||
		in.InsertHeaders != nil
}

// UpdateListener changes a listener. At least one field must be set,
// checked before any request (core.ErrInvalidInput). It waits, within the
// pre-write bound, until the load balancer and the listener are both not
// busy (ErrBusy, nothing sent, past that bound), reads the listener, applies
// every set field, and sends the full body with the read values for the
// rest. The merged certificate fields are checked against the listener's
// own (unchangeable) Protocol exactly as CreateListener checks them.
//
// The PUT keeps the transport's normal retries: resending the same full
// body is safe. Without NoWait, UpdateListener waits exactly as
// CreateListener does; its Output is a fresh read once settled. On a wait
// failure it is instead the last read the wait itself completed, which may
// already show the update applied even though the wait never confirmed the
// load balancer settled, or, if no read ever completed, the listener as it
// read before the update, not the fields this call sent.
func (c *Client) UpdateListener(ctx context.Context, in *UpdateListenerInput) (*UpdateListenerOutput, error) {
	const op = "loadbalancer.UpdateListener"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ListenerID", in.ListenerID); err != nil {
		return nil, err
	}
	if !updateListenerAnySet(in) {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}
	if in.DefaultPoolID != nil && *in.DefaultPoolID != "" {
		if err := core.CheckPathID(op, "DefaultPoolID", *in.DefaultPoolID); err != nil {
			return nil, err
		}
	}
	if in.AllowedCIDRs != nil {
		if err := checkAllowedCIDRs(op, *in.AllowedCIDRs); err != nil {
			return nil, err
		}
	}

	var requestedCertificateIDs []string
	if in.CertificateIDs != nil {
		requestedCertificateIDs = *in.CertificateIDs
	}
	if err := checkListenerCertificateIDs(op, requestedCertificateIDs, deref(in.DefaultCertificateID), deref(in.ClientCertificateID)); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	listener, err := waitPreWriteReady(c, ctx, op, in.LoadBalancerID, "listener", in.ListenerID, func(ctx context.Context) (*Listener, string, error) {
		out, err := c.GetListener(ctx, &GetListenerInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID})
		if err != nil {
			return nil, "", err
		}
		return &out.Listener, out.Listener.ProgressStatus, nil
	})
	if err != nil {
		return nil, err
	}

	defaultPoolID := stringOr(in.DefaultPoolID, listener.DefaultPoolID)
	certificateIDs := listener.CertificateAuthorities
	if in.CertificateIDs != nil {
		certificateIDs = *in.CertificateIDs
	}
	defaultCertificateID := deref(listener.DefaultCertificateAuthority)
	if in.DefaultCertificateID != nil {
		defaultCertificateID = *in.DefaultCertificateID
	}
	clientCertificateID := deref(listener.ClientCertificateAuthentication)
	if in.ClientCertificateID != nil {
		clientCertificateID = *in.ClientCertificateID
	}
	if err := checkListenerCertificateFields(op, listener.Protocol, certificateIDs, defaultCertificateID, clientCertificateID); err != nil {
		return nil, err
	}
	if err := checkListenerCertificateIDs(op, certificateIDs, defaultCertificateID, clientCertificateID); err != nil {
		return nil, err
	}

	cidrs := splitCIDRs(listener.AllowedCIDRs)
	if in.AllowedCIDRs != nil {
		cidrs = *in.AllowedCIDRs
	}
	if len(cidrs) == 0 {
		// checkAllowedCIDRs already refuses a caller-set empty slice; this
		// catches the other way cidrs can end up empty, an unset
		// AllowedCIDRs combined with a listener whose own read comes back
		// with none. The PUT is a full replace, so sending allowedCidrs
		// empty would either open the listener to everyone or close it to
		// everyone, neither ever intended by a caller who left the field
		// alone.
		return nil, fmt.Errorf("%w: %s: the listener's own AllowedCIDRs read back empty; set AllowedCIDRs explicitly", core.ErrInvalidInput, op)
	}
	insertHeaders := listener.InsertHeaders
	if in.InsertHeaders != nil {
		insertHeaders = *in.InsertHeaders
	}

	body := listenerWriteBody{
		TimeoutClient:               intOr(in.TimeoutClient, listener.TimeoutClient),
		TimeoutMember:               intOr(in.TimeoutMember, listener.TimeoutMember),
		TimeoutConnection:           intOr(in.TimeoutConnection, listener.TimeoutConnection),
		DefaultPoolID:               defaultPoolID,
		AllowedCIDRs:                joinCIDRs(cidrs),
		CertificateAuthorities:      certificateIDs,
		DefaultCertificateAuthority: defaultCertificateID,
		ClientCertificate:           clientCertificateID,
		InsertHeaders:               insertHeaders,
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "listeners", in.ListenerID}, nil),
		Body:      body,
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, listenerBusyWaiter(c, op, in.LoadBalancerID, in.ListenerID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, err
	}

	if in.NoWait {
		fallback := *listener
		fallback.DefaultPoolID = defaultPoolID
		fallback.TimeoutClient, fallback.TimeoutMember, fallback.TimeoutConnection = body.TimeoutClient, body.TimeoutMember, body.TimeoutConnection
		fallback.AllowedCIDRs = body.AllowedCIDRs
		return &UpdateListenerOutput{Listener: fallback}, nil
	}

	var settled *Listener
	waitErr := c.waitChildSettled(ctx, op, in.LoadBalancerID, "listener", in.ListenerID, func(ctx context.Context) (string, error) {
		out, err := c.GetListener(ctx, &GetListenerInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID})
		if err != nil {
			return "", err
		}
		settled = &out.Listener
		return settled.ProgressStatus, nil
	})
	if settled == nil {
		settled = listener
	}
	return &UpdateListenerOutput{Listener: *settled}, waitErr
}

// DeleteListenerInput identifies the listener to delete.
type DeleteListenerInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`

	NoWait bool
}

type DeleteListenerOutput struct{}

// DeleteListener deletes a listener. It waits, within the pre-write bound,
// until the load balancer and the listener are both not busy (ErrBusy,
// nothing sent, past that bound), then sends the DELETE.
//
// DELETE is idempotent and keeps the transport's normal retries; a retry
// that finds the listener already gone returns core.ErrNotFound. Without
// NoWait, DeleteListener then waits, within the child bound, for the
// listener to 404 while the load balancer is no longer busy, the same wait
// DeletePool's doc comment describes.
func (c *Client) DeleteListener(ctx context.Context, in *DeleteListenerInput) (*DeleteListenerOutput, error) {
	const op = "loadbalancer.DeleteListener"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ListenerID", in.ListenerID); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if _, err := waitPreWriteReady(c, ctx, op, in.LoadBalancerID, "listener", in.ListenerID, func(ctx context.Context) (struct{}, string, error) {
		out, err := c.GetListener(ctx, &GetListenerInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID})
		if err != nil {
			return struct{}{}, "", err
		}
		return struct{}{}, out.Listener.ProgressStatus, nil
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
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "listeners", in.ListenerID}, nil),
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, listenerBusyWaiter(c, op, in.LoadBalancerID, in.ListenerID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &DeleteListenerOutput{}, nil
	}
	err = c.waitChildDeleted(ctx, op, in.LoadBalancerID, "listener", in.ListenerID, func(ctx context.Context) (string, bool, error) {
		out, err := c.GetListener(ctx, &GetListenerInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID})
		if err != nil {
			if core.IsNotFound(err) {
				return "", true, nil
			}
			return "", false, err
		}
		return out.Listener.ProgressStatus, false, nil
	})
	return &DeleteListenerOutput{}, err
}

// listenerBusyWaiter returns sendWithBusyResend's waitBusy function for a
// write already targeting an existing listener: waiting again for the load
// balancer and the listener to both go idle, the same check UpdateListener
// and DeleteListener already ran once before their first send.
func listenerBusyWaiter(c *Client, op, lbID, listenerID string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := waitPreWriteReady(c, ctx, op, lbID, "listener", listenerID, func(ctx context.Context) (*Listener, string, error) {
			out, err := c.GetListener(ctx, &GetListenerInput{LoadBalancerID: lbID, ListenerID: listenerID})
			if err != nil {
				return nil, "", err
			}
			return &out.Listener, out.Listener.ProgressStatus, nil
		})
		return err
	}
}
