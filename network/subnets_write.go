package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Status values GetSubnet and the subnet waits observe. A status this SDK
// does not recognize keeps a create wait polling rather than treating it as
// settled.
const (
	subnetStatusActive  = "ACTIVE"
	subnetStatusError   = "ERROR"
	subnetStatusDeleted = "DELETED"
)

// subnetPollInterval, subnetCreateBound, and subnetDeleteBound are
// CreateSubnet and DeleteSubnet's post-write wait timing.
const (
	subnetPollInterval = 2 * time.Second
	subnetCreateBound  = 3 * time.Minute
	subnetDeleteBound  = 3 * time.Minute
)

// subnetWriteResponse is CreateSubnet and UpdateSubnet's response shape:
// the subnet wrapped in a "data" field, confirmed live. GetSubnet returns
// the same fields at the top level instead. Both decode into
// subnetResponse, the same private type GetSubnet and ListSubnetsByVPC
// already use, and map it with its own toSubnet, so a field type that
// differs from the read model can never fail the decode.
type subnetWriteResponse struct {
	Data subnetResponse `json:"data"`
}

// CreateSubnetInput creates a subnet in a VPC.
//
// CIDR must parse with net/netip.ParsePrefix, be IPv4, and have no host
// bits; see CreateVPCInput. The SDK does not check that CIDR lies inside
// its VPC's own CIDR; the server does.
//
// ZoneID names a zone enabled for the account; the SDK picks no default,
// since a guess would place the subnet, and any server later created in
// it, in a zone the caller did not choose. list-zones (CLI) or the
// equivalent portal read finds an enabled zone.
type CreateSubnetInput struct {
	VPCID  string `vngcloud:"required"`
	ZoneID string `vngcloud:"required"`
	Name   string `vngcloud:"required"`
	CIDR   string `vngcloud:"required"`

	NoWait bool
}

type CreateSubnetOutput struct {
	Subnet Subnet
}

// subnetCreateBody is CreateSubnet's request body. The API also takes tags
// and secondarySubnetRequests; the SDK sends neither.
type subnetCreateBody struct {
	Name   string `json:"name"`
	CIDR   string `json:"cidr"`
	ZoneID string `json:"zoneId"`
}

// CreateSubnet creates a subnet in VPCID.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the subnet may exist, and the caller lists the
// VPC's subnets with ListSubnetsByVPC and matches Name exactly before
// creating it again, rather than retrying blind.
//
// Without NoWait, CreateSubnet then waits for the new subnet to reach
// ACTIVE, polling GetSubnet every 2 seconds for up to 3 minutes of elapsed
// time, tolerating a 404. If the subnet reaches ERROR instead, or the
// wait's bound runs out, or a read or a sleep in that wait fails, the
// returned error wraps ErrFailed or ErrNotSettled and the Output still
// holds the subnet: the last one a read returned, or, if none did, the one
// the create response itself carried.
func (c *Client) CreateSubnet(ctx context.Context, in *CreateSubnetInput) (*CreateSubnetOutput, error) {
	const op = "network.CreateSubnet"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	if err := checkIPv4NoHostBits(op, "CIDR", in.CIDR); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp subnetWriteResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID, "subnets"}, nil),
		Body:      subnetCreateBody{Name: in.Name, CIDR: in.CIDR, ZoneID: in.ZoneID},
		OK:        []int{200},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousSubnetCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the subnet may exist, list the VPC's subnets and match the name exactly before creating it again"}
	}
	subnet := resp.Data.toSubnet()
	if in.NoWait {
		return &CreateSubnetOutput{Subnet: subnet}, nil
	}

	settled, waitErr := c.waitSubnetActive(ctx, op, in.VPCID, subnet.UUID)
	if settled == nil {
		settled = &subnet
	}
	return &CreateSubnetOutput{Subnet: *settled}, waitErr
}

// wrapAmbiguousSubnetCreateErr wraps err, from the create POST op just
// sent, with a hint to list the VPC's subnets before creating again, unless
// err is already a 4xx *core.APIError. Unlike VPCs and security groups,
// subnets have no list filter by name; the caller scans ListSubnetsByVPC's
// full result instead.
func wrapAmbiguousSubnetCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list the VPC's subnets and match the name exactly before creating it again: %w", op, err)
}

// UpdateSubnetInput renames a subnet.
type UpdateSubnetInput struct {
	VPCID    string `vngcloud:"required"`
	SubnetID string `vngcloud:"required"`
	Name     string `vngcloud:"required"`
}

type UpdateSubnetOutput struct {
	Subnet Subnet
}

// subnetRenameBody is UpdateSubnet's request body.
type subnetRenameBody struct {
	Name string `json:"name"`
}

// UpdateSubnet renames a subnet. It reads the subnet first and refuses one
// that has any SecondarySubnets, with core.ErrInvalidInput, sending
// nothing: the rename PATCH's body has no secondarySubnetRequests field,
// and whether omitting it would drop the subnet's secondary subnets is not
// yet confirmed live.
//
// The PATCH is marked idempotent: sending the same name twice is harmless,
// so it keeps the transport's normal retries. Its own response shape is
// not verified, so UpdateSubnet reads the subnet once more afterward and
// returns that read as the Output. If that second read fails, the write
// has already succeeded: the returned error wraps ErrNotSettled and the
// Output falls back to the fields the pre-read carried, with Name updated.
func (c *Client) UpdateSubnet(ctx context.Context, in *UpdateSubnetInput) (*UpdateSubnetOutput, error) {
	const op = "network.UpdateSubnet"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SubnetID", in.SubnetID); err != nil {
		return nil, err
	}

	current, err := c.GetSubnet(ctx, &GetSubnetInput{VPCID: in.VPCID, SubnetID: in.SubnetID})
	if err != nil {
		return nil, err
	}
	if len(current.Subnet.SecondarySubnets) > 0 {
		return nil, fmt.Errorf("%w: %s: subnet %s has %d secondary subnet(s); rename is refused until the rename body's effect on them is confirmed live",
			core.ErrInvalidInput, op, in.SubnetID, len(current.Subnet.SecondarySubnets))
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPatch,
		URL:        c.networkURL([]string{projectID, "networks", in.VPCID, "subnets", in.SubnetID}, nil),
		Body:       subnetRenameBody{Name: in.Name},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	fallback := current.Subnet
	fallback.Name = in.Name
	updated, err := c.GetSubnet(ctx, &GetSubnetInput{VPCID: in.VPCID, SubnetID: in.SubnetID})
	if err != nil {
		return &UpdateSubnetOutput{Subnet: fallback}, fmt.Errorf("%w: %s: subnet %s: %w", ErrNotSettled, op, in.SubnetID, err)
	}
	return &UpdateSubnetOutput{Subnet: updated.Subnet}, nil
}

// DeleteSubnetInput identifies the subnet to delete, within the VPC it must
// belong to.
type DeleteSubnetInput struct {
	VPCID    string `vngcloud:"required"`
	SubnetID string `vngcloud:"required"`

	NoWait bool
}

type DeleteSubnetOutput struct{}

// DeleteSubnet deletes a subnet. It reads the subnet first with GetSubnet
// and returns core.ErrNotFound, sending nothing, when that read shows
// status DELETED: GetSubnet keeps returning a deleted subnet for minutes
// after ListSubnetsByVPC has already dropped it, so this SDK treats
// DELETED as gone rather than sending a DELETE that the server would 500.
//
// It then sends nothing and returns ErrInUse when ListServersBySubnet,
// ListNetworkInterfaces, or ListVirtualIPAddresses shows any item in the
// subnet; the last two are filtered by subnet id in the SDK, since neither
// list takes a subnet filter of its own. The server's own refusal is the
// final guard for any other use.
//
// A repeat DELETE on an already-deleted subnet returns 500, so after a 5xx
// or network error on the DELETE, DeleteSubnet lists the VPC's subnets: an
// absent subnet means the delete took effect and the call succeeds;
// otherwise the original error returns.
//
// Without NoWait, DeleteSubnet then waits for the subnet to leave
// ListSubnetsByVPC's result, polling every 2 seconds for up to 3 minutes of
// elapsed time. If the listed subnet's Status is ERROR instead, or the
// bound runs out, the returned error wraps ErrFailed or ErrNotSettled; a
// rerun is safe either way, since DeleteSubnet always reads first.
func (c *Client) DeleteSubnet(ctx context.Context, in *DeleteSubnetInput) (*DeleteSubnetOutput, error) {
	const op = "network.DeleteSubnet"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SubnetID", in.SubnetID); err != nil {
		return nil, err
	}

	current, err := c.GetSubnet(ctx, &GetSubnetInput{VPCID: in.VPCID, SubnetID: in.SubnetID})
	if err != nil {
		return nil, err
	}
	if current.Subnet.Status == subnetStatusDeleted {
		return nil, fmt.Errorf("%w: %s: subnet %s is already DELETED", core.ErrNotFound, op, in.SubnetID)
	}

	if err := checkSubnetNotInUse(ctx, c, op, in.SubnetID); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID, "subnets", in.SubnetID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, c.confirmSubnetDeleteOn5xx(ctx, in.VPCID, in.SubnetID, err)
	}
	if in.NoWait {
		return &DeleteSubnetOutput{}, nil
	}
	if err := c.waitSubnetDeleted(ctx, op, in.VPCID, in.SubnetID); err != nil {
		return &DeleteSubnetOutput{}, err
	}
	return &DeleteSubnetOutput{}, nil
}

// checkSubnetNotInUse returns ErrInUse, sending nothing else, when
// ListServersBySubnet, ListNetworkInterfaces, or ListVirtualIPAddresses
// shows any item attached to subnetID.
func checkSubnetNotInUse(ctx context.Context, c *Client, op, subnetID string) error {
	servers, err := c.ListServersBySubnet(ctx, &ListServersBySubnetInput{SubnetID: subnetID})
	if err != nil {
		return err
	}
	if len(servers.Items) > 0 {
		return fmt.Errorf("%w: %s: subnet %s has %d server(s) attached", ErrInUse, op, subnetID, len(servers.Items))
	}

	interfaces, err := c.ListNetworkInterfaces(ctx, nil)
	if err != nil {
		return err
	}
	ifaceCount := 0
	for _, iface := range interfaces.Items {
		if iface.SubnetID == subnetID || iface.SubnetUUID == subnetID {
			ifaceCount++
		}
	}
	if ifaceCount > 0 {
		return fmt.Errorf("%w: %s: subnet %s has %d network interface(s) attached", ErrInUse, op, subnetID, ifaceCount)
	}

	virtualIPs, err := c.ListVirtualIPAddresses(ctx, nil)
	if err != nil {
		return err
	}
	vipCount := 0
	for _, vip := range virtualIPs.Items {
		if vip.SubnetID == subnetID {
			vipCount++
		}
	}
	if vipCount > 0 {
		return fmt.Errorf("%w: %s: subnet %s has %d virtual IP(s) attached", ErrInUse, op, subnetID, vipCount)
	}
	return nil
}

// confirmSubnetDeleteOn5xx returns nil for a 5xx or network error on the
// subnet DELETE when subnetID is then absent from vpcID's subnet list
// (the delete took effect despite the error), and err unchanged otherwise,
// including for any error that is not a 5xx or network error at all.
func (c *Client) confirmSubnetDeleteOn5xx(ctx context.Context, vpcID, subnetID string, err error) error {
	if !is5xxOrNetworkError(err) {
		return err
	}
	subnets, listErr := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: vpcID})
	if listErr != nil {
		return err
	}
	for _, s := range subnets.Items {
		if s.UUID == subnetID {
			return err
		}
	}
	return nil
}

// is5xxOrNetworkError reports whether err is a *core.APIError with
// StatusCode 0 (no response ever came back) or 500 and above.
func is5xxOrNetworkError(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == 0 || apiErr.StatusCode >= 500
}

// waitSubnetActive is CreateSubnet's post-create wait unless NoWait is set:
// it reads subnetID under vpcID with GetSubnet until its Status reaches
// subnetStatusActive or subnetStatusError; any other status keeps it
// polling, and a 404 during the wait also keeps polling.
func (c *Client) waitSubnetActive(ctx context.Context, op, vpcID, subnetID string) (*Subnet, error) {
	var subnet *Subnet
	err := poll(ctx, c.now, c.sleep, subnetPollInterval, subnetCreateBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetSubnet(ctx, &GetSubnetInput{VPCID: vpcID, SubnetID: subnetID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			subnet = &out.Subnet
			switch subnet.Status {
			case subnetStatusActive:
				return true, nil
			case subnetStatusError:
				return true, fmt.Errorf("%w: %s: subnet %s is ERROR", ErrFailed, op, subnetID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: subnet %s did not reach ACTIVE within %s; the subnet exists and this create must not be repeated",
				ErrNotSettled, op, subnetID, subnetCreateBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: subnet %s: %w", ErrNotSettled, op, subnetID, err)
	}
	return subnet, err
}

// waitSubnetDeleted is DeleteSubnet's post-delete wait unless NoWait is
// set: it lists vpcID's subnets with ListSubnetsByVPC until subnetID is
// absent (settled), failing on ErrFailed if the listed entry's Status is
// subnetStatusError instead. GetSubnet is not used here: it keeps
// returning the subnet with status DELETED for minutes after
// ListSubnetsByVPC has already dropped it.
func (c *Client) waitSubnetDeleted(ctx context.Context, op, vpcID, subnetID string) error {
	err := poll(ctx, c.now, c.sleep, subnetPollInterval, subnetDeleteBound,
		func(ctx context.Context) (bool, error) {
			subnets, err := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: vpcID})
			if err != nil {
				return true, err
			}
			for _, s := range subnets.Items {
				if s.UUID != subnetID {
					continue
				}
				if s.Status == subnetStatusError {
					return true, fmt.Errorf("%w: %s: subnet %s is ERROR", ErrFailed, op, subnetID)
				}
				return false, nil
			}
			return true, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: subnet %s still listed under VPC %s after %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, subnetID, vpcID, subnetDeleteBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: subnet %s: %w", ErrNotSettled, op, subnetID, err)
	}
	return err
}

// ListServersBySubnetInput identifies the subnet to list servers in.
type ListServersBySubnetInput struct {
	SubnetID string `vngcloud:"required"`
}
type ListServersBySubnetOutput struct {
	Items []compute.Server
}

// ListServersBySubnet lists the servers with an interface in SubnetID.
func (c *Client) ListServersBySubnet(ctx context.Context, in *ListServersBySubnetInput) (*ListServersBySubnetOutput, error) {
	const op = "network.ListServersBySubnet"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SubnetID", in.SubnetID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []compute.Server `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.networkURL([]string{projectID, "servers", "subnets", in.SubnetID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListServersBySubnetOutput{Items: resp.Data}, nil
}
