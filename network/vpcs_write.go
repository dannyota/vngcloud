package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Status values GetVPC and the VPC waits observe. A status this SDK does
// not recognize keeps a create or delete wait polling rather than treating
// it as settled.
const (
	vpcStatusActive = "ACTIVE"
	vpcStatusError  = "ERROR"

	// vpcDNSStatusEnabled, vpcDNSStatusEnabling, and vpcDNSStatusDisabled
	// are the dnsStatus values EnableVPCPrivateDNS understands. Any other
	// value fails closed with ErrUnexpectedStatus rather than guessing
	// whether the server would accept the enable call.
	vpcDNSStatusEnabled  = "ENABLED"
	vpcDNSStatusEnabling = "ENABLING"
	vpcDNSStatusDisabled = "DISABLED"
)

// vpcPollInterval, vpcCreateBound, and vpcDeleteBound are CreateVPC and
// DeleteVPC's post-write wait timing. privateDNSPollInterval and
// privateDNSBound are EnableVPCPrivateDNS's, much longer since the probe
// saw ENABLING take over 5 minutes to reach ENABLED.
const (
	vpcPollInterval = 2 * time.Second
	vpcCreateBound  = 3 * time.Minute
	vpcDeleteBound  = 3 * time.Minute

	privateDNSPollInterval = 10 * time.Second
	privateDNSBound        = 10 * time.Minute
)

var (
	// ErrInUse means a VPC, subnet, or route table write was refused because
	// a pre-write read showed the resource still holds or is held by
	// something that must be removed first: a VPC or subnet with servers,
	// volumes, subnets, interfaces, or virtual IPs still attached (see
	// below), or a route table still named by a subnet's routeTableUuid
	// (route_tables_write.go), or because the server's own refusal named
	// the resource in use. In the first case nothing was sent; in the
	// second, the request reached the server.
	ErrInUse = errors.New("network: resource in use")

	// ErrUnexpectedStatus means EnableVPCPrivateDNS read a VPC dnsStatus
	// this SDK does not know how to act on. Nothing was sent.
	ErrUnexpectedStatus = errors.New("network: unexpected status")
)

// vpcWriteResponse is CreateVPC's response shape: the VPC wrapped in a
// "data" field, confirmed live. GetVPC returns the same fields at the top
// level instead. UpdateVPC and EnableVPCPrivateDNS decode no response body
// of their own; both confirm their write with a follow-up GetVPC instead.
// CreateVPC decodes into this private type and maps it with toVPC, rather
// than the bare VPC model, so a field type that differs from the read model
// can never fail the decode (see CreateSubnet's subnetResponse).
type vpcWriteResponse struct {
	Data vpcWriteData `json:"data"`
}

// vpcWriteData mirrors VPC's fields for CreateVPC's response.
type vpcWriteData struct {
	UUID           string   `json:"id"`
	Status         string   `json:"status"`
	ElasticIPs     []string `json:"elasticIps"`
	Name           string   `json:"displayName"`
	CreatedAt      string   `json:"createdAt"`
	CIDR           string   `json:"cidr"`
	DHCPOptionName string   `json:"dhcpOptionName"`
	DHCPOptionID   string   `json:"dhcpOptionId"`
	RouteTableName string   `json:"routeTableName"`
	RouteTableID   string   `json:"routeTableId"`
	Zone           Zone     `json:"zone"`
	DNSStatus      string   `json:"dnsStatus"`
	DNSID          string   `json:"dnsId"`
	MTU            int      `json:"mtu"`
	ServerCount    int      `json:"serverCount"`
	VolumeCount    int      `json:"volumeCount"`
}

// toVPC converts d to VPC. The two share an identical field layout today;
// a future field that must diverge breaks this conversion at compile time,
// forcing an explicit field-by-field mapping then, rather than a silent
// decode mismatch now.
func (d vpcWriteData) toVPC() VPC {
	return VPC(d)
}

// checkIPv4NoHostBits returns an error wrapping core.ErrInvalidInput unless
// value parses with net/netip.ParsePrefix, is IPv4, and has no host bits
// set beyond its prefix length (10.20.1.0/16 is refused; 10.20.0.0/16 is
// not). The exact prefix length and which blocks are private stay on the
// server, per the design's rule that the SDK checks only input shape.
func checkIPv4NoHostBits(op, field, value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return fmt.Errorf("%w: %s: %s must be an IPv4 CIDR prefix such as 10.20.0.0/24, got %q",
			core.ErrInvalidInput, op, field, value)
	}
	if !prefix.Addr().Is4() {
		return fmt.Errorf("%w: %s: %s must be IPv4, got %q", core.ErrInvalidInput, op, field, value)
	}
	if prefix != prefix.Masked() {
		return fmt.Errorf("%w: %s: %s must have no host bits set; got %q, want %q",
			core.ErrInvalidInput, op, field, value, prefix.Masked())
	}
	return nil
}

// is4xxAPIError reports whether err is a *core.APIError whose StatusCode is
// 4xx, meaning the server rejected the request outright and never acted on
// it.
func is4xxAPIError(err error) bool {
	var apiErr *core.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// CreateVPCInput creates a VPC.
//
// CIDR must parse with net/netip.ParsePrefix, be IPv4, and have no host
// bits: 10.20.1.0/16 is refused because bits beyond the prefix length are
// set. The exact prefix length and which blocks are private stay on the
// server.
//
// CreateVPC never sends zoneId. Live, the server ignores it and places
// every VPC in the region's first zone, even one disabled for the account;
// the zone that matters is a subnet's own ZoneID. An input the server
// ignores would tell the caller it chose a zone when it did not.
type CreateVPCInput struct {
	Name string `vngcloud:"required"`
	CIDR string `vngcloud:"required"`

	NoWait bool
}

type CreateVPCOutput struct {
	VPC VPC
}

// vpcCreateBody is CreateVPC's request body. The API also takes tags and
// zoneId; the SDK sends neither.
type vpcCreateBody struct {
	Name string `json:"name"`
	CIDR string `json:"cidr"`
}

// CreateVPC creates a VPC.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the VPC may exist, and the caller lists VPCs and
// matches Name exactly (the list's own name filter may match by substring)
// before creating it again, rather than retrying blind.
//
// Without NoWait, CreateVPC then waits for the new VPC to reach ACTIVE,
// polling GetVPC every 2 seconds for up to 3 minutes of elapsed time,
// tolerating a 404 (a VPC just created may not be readable at once). If
// the VPC reaches ERROR instead, or the wait's bound runs out, or a read or
// a sleep in that wait fails, such as from a canceled ctx, the returned
// error wraps ErrFailed or ErrNotSettled and the Output still holds the
// VPC: the last one a read returned, or, if none did, the one the create
// response itself carried. Either way the Output is never nil and the
// caller keeps the new VPC's id.
func (c *Client) CreateVPC(ctx context.Context, in *CreateVPCInput) (*CreateVPCOutput, error) {
	const op = "network.CreateVPC"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkIPv4NoHostBits(op, "CIDR", in.CIDR); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp vpcWriteResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "networks"}, nil),
		Body:      vpcCreateBody{Name: in.Name, CIDR: in.CIDR},
		OK:        []int{200},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousVPCCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the VPC may exist, list vpcs and match the name exactly before creating it again"}
	}
	vpc := resp.Data.toVPC()
	if in.NoWait {
		return &CreateVPCOutput{VPC: vpc}, nil
	}

	settled, waitErr := c.waitVPCActive(ctx, op, vpc.UUID)
	if settled == nil {
		settled = &vpc
	}
	return &CreateVPCOutput{VPC: *settled}, waitErr
}

// wrapAmbiguousVPCCreateErr wraps err, from the create POST op just sent,
// with a hint to list VPCs before creating again, unless err is already a
// 4xx *core.APIError: a 4xx means the server rejected the request outright,
// so nothing was created and the exact same call is safe to retry. Any
// other error leaves whether the VPC was created unknown.
func wrapAmbiguousVPCCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list vpcs and match the name exactly before creating it again: %w", op, err)
}

// UpdateVPCInput renames a VPC. The API replaces the whole name on every
// PATCH, but a VPC has only this one editable field, so, unlike
// UpdateSecurityGroup, there is no other field to preserve.
type UpdateVPCInput struct {
	VPCID string `vngcloud:"required"`
	Name  string `vngcloud:"required"`
}

type UpdateVPCOutput struct {
	VPC VPC
}

// vpcRenameBody is UpdateVPC's request body.
type vpcRenameBody struct {
	Name string `json:"name"`
}

// UpdateVPC renames a VPC. The PATCH is marked idempotent: sending the same
// name twice is harmless, so it keeps the transport's normal retries.
//
// The PATCH's own response shape is not verified, so UpdateVPC reads the
// VPC once more afterward and returns that read as the Output. If that
// second read fails, the write has already succeeded: the returned error
// wraps ErrNotSettled and the Output falls back to the id and name the
// PATCH itself sent.
func (c *Client) UpdateVPC(ctx context.Context, in *UpdateVPCInput) (*UpdateVPCOutput, error) {
	const op = "network.UpdateVPC"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPatch,
		URL:        c.networkURL([]string{projectID, "networks", in.VPCID}, nil),
		Body:       vpcRenameBody{Name: in.Name},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	fallback := VPC{UUID: in.VPCID, Name: in.Name}
	updated, err := c.GetVPC(ctx, &GetVPCInput{VPCID: in.VPCID})
	if err != nil {
		return &UpdateVPCOutput{VPC: fallback}, fmt.Errorf("%w: %s: VPC %s: %w", ErrNotSettled, op, in.VPCID, err)
	}
	return &UpdateVPCOutput{VPC: updated.VPC}, nil
}

// DeleteVPCInput identifies the VPC to delete.
type DeleteVPCInput struct {
	VPCID string `vngcloud:"required"`

	NoWait bool
}

type DeleteVPCOutput struct{}

// DeleteVPC deletes a VPC and its ACLs and route tables. It reads the VPC
// and its subnets first and sends nothing when that read shows any
// server, volume, or subnet still attached (ErrInUse): subnets carry
// workloads and must be deleted first, and ACLs and route tables hold no
// traffic once the subnets are gone, so they are left for the server to
// remove along with the VPC.
//
// The server's own refusal is the final guard: a 400 whose message
// contains "contains the subnet" also wraps ErrInUse, since the server
// keeps refusing for minutes after a subnet's delete leaves the VPC's
// subnet list, and a rerun once that window passes is safe.
//
// DELETE is idempotent and keeps the transport's normal retries. Without
// NoWait, DeleteVPC then waits for a 404 on GetVPC, polling every 2 seconds
// for up to 3 minutes of elapsed time. If the VPC reaches ERROR instead, or
// the bound runs out, or a read or sleep fails, the returned error wraps
// ErrFailed or ErrNotSettled; a rerun is safe either way, since DeleteVPC
// always reads first.
func (c *Client) DeleteVPC(ctx context.Context, in *DeleteVPCInput) (*DeleteVPCOutput, error) {
	const op = "network.DeleteVPC"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}

	current, err := c.GetVPC(ctx, &GetVPCInput{VPCID: in.VPCID})
	if err != nil {
		return nil, err
	}
	if current.VPC.ServerCount > 0 || current.VPC.VolumeCount > 0 {
		return nil, fmt.Errorf("%w: %s: VPC %s has %d server(s) and %d volume(s); delete them first",
			ErrInUse, op, in.VPCID, current.VPC.ServerCount, current.VPC.VolumeCount)
	}
	subnets, err := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: in.VPCID})
	if err != nil {
		return nil, err
	}
	if len(subnets.Items) > 0 {
		return nil, fmt.Errorf("%w: %s: VPC %s has %d subnet(s); delete them first", ErrInUse, op, in.VPCID, len(subnets.Items))
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, wrapVPCContainsSubnet(op, in.VPCID, err)
	}
	if in.NoWait {
		return &DeleteVPCOutput{}, nil
	}
	if err := c.waitVPCDeleted(ctx, op, in.VPCID); err != nil {
		return &DeleteVPCOutput{}, err
	}
	return &DeleteVPCOutput{}, nil
}

// wrapVPCContainsSubnet rewraps err with ErrInUse when it is a
// *core.APIError whose message contains "contains the subnet"
// (case-insensitive): the server keeps refusing a VPC delete for minutes
// after the last subnet's delete leaves the VPC's subnet list, so the
// message tells the caller a rerun once that window passes is safe. Any
// other error passes through unchanged.
func wrapVPCContainsSubnet(op, vpcID string, err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Message), "contains the subnet") {
		return fmt.Errorf("%w: %s: VPC %s: %w; a subnet deleted in the last 15 minutes can still block this delete, a rerun is safe",
			ErrInUse, op, vpcID, apiErr)
	}
	return err
}

// waitVPCActive is CreateVPC's post-create wait unless NoWait is set: it
// reads vpcID with GetVPC until its Status reaches vpcStatusActive or
// vpcStatusError; any other status, CREATING or one this SDK does not
// recognize, keeps it polling. A 404 during the wait also keeps polling
// rather than failing at once, since a VPC just created may not be
// readable yet; any other read failure stops the wait and is returned as
// is.
//
// It returns the last VPC a read returned alongside the outcome: nil error
// once ACTIVE, an error wrapping ErrFailed on ERROR, or an error wrapping
// ErrNotSettled once the bound runs out or a read or a sleep fails. The
// returned VPC is nil only when no read ever succeeded.
func (c *Client) waitVPCActive(ctx context.Context, op, vpcID string) (*VPC, error) {
	var vpc *VPC
	err := poll(ctx, c.now, c.sleep, vpcPollInterval, vpcCreateBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVPC(ctx, &GetVPCInput{VPCID: vpcID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			vpc = &out.VPC
			switch vpc.Status {
			case vpcStatusActive:
				return true, nil
			case vpcStatusError:
				return true, fmt.Errorf("%w: %s: VPC %s is ERROR", ErrFailed, op, vpcID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: VPC %s did not reach ACTIVE within %s; the VPC exists and this create must not be repeated",
				ErrNotSettled, op, vpcID, vpcCreateBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: VPC %s: %w", ErrNotSettled, op, vpcID, err)
	}
	return vpc, err
}

// waitVPCDeleted is DeleteVPC's post-delete wait unless NoWait is set: it
// reads vpcID with GetVPC until that read reports NotFound (settled) or the
// VPC's Status is vpcStatusError; any other read keeps it polling.
func (c *Client) waitVPCDeleted(ctx context.Context, op, vpcID string) error {
	err := poll(ctx, c.now, c.sleep, vpcPollInterval, vpcDeleteBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVPC(ctx, &GetVPCInput{VPCID: vpcID})
			if err != nil {
				if core.IsNotFound(err) {
					return true, nil
				}
				return true, err
			}
			if out.VPC.Status == vpcStatusError {
				return true, fmt.Errorf("%w: %s: VPC %s is ERROR", ErrFailed, op, vpcID)
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: VPC %s did not reach 404 within %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, vpcID, vpcDeleteBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: VPC %s: %w", ErrNotSettled, op, vpcID, err)
	}
	return err
}

// EnableVPCPrivateDNSInput identifies the VPC to enable Private DNS on.
// There is no matching disable call; see EnableVPCPrivateDNS's doc comment.
type EnableVPCPrivateDNSInput struct {
	VPCID string `vngcloud:"required"`

	NoWait bool
}

// EnableVPCPrivateDNSOutput is the VPC after the call, and whether the call
// itself changed anything. Changed is false only when the VPC's dnsStatus
// was already ENABLED.
type EnableVPCPrivateDNSOutput struct {
	VPC     VPC
	Changed bool
}

// EnableVPCPrivateDNS drives VPCID's Private DNS to ENABLED. It never
// disables Private DNS: the API has no call for that, and the SDK never
// enables it as a side effect of any other write.
//
// It reads the VPC first. dnsStatus ENABLED returns at once with Changed
// false, sending nothing. ENABLING sends nothing and goes straight to the
// wait below, since some earlier call already started it. DISABLED sends
// the enable PATCH at most once (transport.Request.Once): a resend would
// act on a status read that only grows staler, per ADR 0003. Any other
// dnsStatus fails closed with ErrUnexpectedStatus, sending nothing.
//
// A PATCH failure that is a 4xx *core.APIError proves the server never
// acted and is returned as is. Any other failure, including a 5xx or a
// network error after the dial succeeded, may have reached the server, so
// the returned error wraps ErrNotSettled instead; the recovery is to run
// EnableVPCPrivateDNS again, since it always reads first.
//
// Without NoWait, EnableVPCPrivateDNS then waits for dnsStatus ENABLED,
// polling GetVPC every 10 seconds for up to 10 minutes of elapsed time: the
// probe saw ENABLING take over 5 minutes. If a later read during that wait
// shows any dnsStatus other than ENABLING or ENABLED, the wait stops at
// once with ErrUnexpectedStatus rather than polling toward the bound. Once
// the wait proceeds this far, Changed is true, whether this call sent the
// PATCH or an earlier one did.
func (c *Client) EnableVPCPrivateDNS(ctx context.Context, in *EnableVPCPrivateDNSInput) (*EnableVPCPrivateDNSOutput, error) {
	const op = "network.EnableVPCPrivateDNS"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}

	current, err := c.GetVPC(ctx, &GetVPCInput{VPCID: in.VPCID})
	if err != nil {
		return nil, err
	}

	switch current.VPC.DNSStatus {
	case vpcDNSStatusEnabled:
		return &EnableVPCPrivateDNSOutput{VPC: current.VPC, Changed: false}, nil
	case vpcDNSStatusEnabling:
		// Nothing to send; an earlier call already started it.
	case vpcDNSStatusDisabled:
		if err := c.sendEnableVPCPrivateDNS(ctx, op, in.VPCID); err != nil {
			if is4xxAPIError(err) {
				return nil, err
			}
			return &EnableVPCPrivateDNSOutput{VPC: current.VPC}, fmt.Errorf("%w: %s: VPC %s: %w", ErrNotSettled, op, in.VPCID, err)
		}
	default:
		return nil, fmt.Errorf("%w: %s: VPC %s dnsStatus is %q", ErrUnexpectedStatus, op, in.VPCID, current.VPC.DNSStatus)
	}

	if in.NoWait {
		return &EnableVPCPrivateDNSOutput{VPC: current.VPC, Changed: true}, nil
	}

	settled, waitErr := c.waitVPCPrivateDNSEnabled(ctx, op, in.VPCID)
	if settled == nil {
		settled = &current.VPC
	}
	return &EnableVPCPrivateDNSOutput{VPC: *settled, Changed: true}, waitErr
}

// sendEnableVPCPrivateDNS sends the enableDns PATCH at most once
// (transport.Request.Once), per ADR 0003: any resend would act on the
// dnsStatus read EnableVPCPrivateDNS already took, which only grows staler
// with each attempt.
func (c *Client) sendEnableVPCPrivateDNS(ctx context.Context, op, vpcID string) error {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPatch,
		URL:       c.networkURL([]string{projectID, "networks", vpcID, "enableDns"}, nil),
		OK:        []int{200},
		Once:      true,
	}
	return c.c.DoJSON(ctx, req, nil)
}

// waitVPCPrivateDNSEnabled is EnableVPCPrivateDNS's post-enable wait unless
// NoWait is set: it reads vpcID with GetVPC until its DNSStatus reaches
// vpcDNSStatusEnabled; vpcDNSStatusEnabling keeps it polling. Any other
// DNSStatus stops the wait at once with an error wrapping
// ErrUnexpectedStatus, rather than polling toward a bound timeout on a
// status this SDK cannot interpret. Any other read error also stops the
// wait and is returned as is; only the bound running out or such a read
// failure produces ErrNotSettled.
func (c *Client) waitVPCPrivateDNSEnabled(ctx context.Context, op, vpcID string) (*VPC, error) {
	var vpc *VPC
	err := poll(ctx, c.now, c.sleep, privateDNSPollInterval, privateDNSBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVPC(ctx, &GetVPCInput{VPCID: vpcID})
			if err != nil {
				return true, err
			}
			vpc = &out.VPC
			switch vpc.DNSStatus {
			case vpcDNSStatusEnabled:
				return true, nil
			case vpcDNSStatusEnabling:
				return false, nil
			default:
				return true, fmt.Errorf("%w: %s: VPC %s dnsStatus is %q", ErrUnexpectedStatus, op, vpcID, vpc.DNSStatus)
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: VPC %s dnsStatus did not reach ENABLED within %s; the enable was sent, rerun EnableVPCPrivateDNS to check again",
				ErrNotSettled, op, vpcID, privateDNSBound)
		},
	)
	if err != nil && !errors.Is(err, ErrNotSettled) && !errors.Is(err, ErrUnexpectedStatus) {
		err = fmt.Errorf("%w: %s: VPC %s: %w", ErrNotSettled, op, vpcID, err)
	}
	return vpc, err
}
