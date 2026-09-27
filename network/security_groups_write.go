package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Status values GetSecurityGroup and the group wait observe. A status this
// SDK does not recognize, such as CREATING, keeps the wait polling rather
// than treating it as settled.
const (
	securityGroupStatusActive = "ACTIVE"
	securityGroupStatusError  = "ERROR"
)

var (
	// ErrSecurityGroupInUse means a security group delete was refused
	// because the group has servers attached, found by a pre-delete read
	// with ListServersBySecurityGroup, or because the server's own refusal
	// named the group in use for some other reason. In the first case
	// nothing was sent; in the second, the DELETE reached the server.
	ErrSecurityGroupInUse = errors.New("network: security group in use")

	// ErrSystemGroup means a security group update or delete targeted a
	// project's system group. Nothing was sent.
	ErrSystemGroup = errors.New("network: system security group")

	// ErrNotSettled means a write was sent, and may have reached the
	// server, but no read confirmed its result. The write must not be
	// repeated; the returned Output still holds the resource the SDK last
	// read, so the caller keeps its id to check again later.
	ErrNotSettled = errors.New("network: write accepted but not settled")

	// ErrFailed means a created security group reached ERROR. The returned
	// Output still holds the resource the SDK last read.
	ErrFailed = errors.New("network: write failed on the server")
)

// securityGroupWriteBody is the request body CreateSecurityGroup's POST and
// UpdateSecurityGroup's PUT both send. The API also takes tags and zoneId;
// the SDK sends neither.
type securityGroupWriteBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// createSecurityGroupResponse is Create's response, which does not match
// the SecurityGroup read model: id comes back as an integer, the group's
// own id as uuid, and its name as secgroupName. Decoding straight into
// SecurityGroup would fail on the integer id, so CreateSecurityGroup
// decodes into this private type and maps it instead.
type createSecurityGroupResponse struct {
	ID           int    `json:"id"`
	UUID         string `json:"uuid"`
	SecgroupName string `json:"secgroupName"`
}

// CreateSecurityGroupInput creates a security group. The new group holds
// the server's default egress rules (allow all outbound traffic) and no
// ingress rule, so it admits no inbound traffic until a rule allows it.
//
// Without NoWait, CreateSecurityGroup waits for the group to reach ACTIVE
// before returning; see waitSecurityGroupActive. NoWait skips that wait and
// returns the mapped create response instead.
type CreateSecurityGroupInput struct {
	Name string `vngcloud:"required"`

	Description string
	NoWait      bool
}

type CreateSecurityGroupOutput struct {
	SecurityGroup SecurityGroup
}

// CreateSecurityGroup creates a security group. Group names are unique per
// project; a repeat name fails with the server's own message.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the group may exist, and the caller lists security
// groups by Name and matches it exactly (the list's own name filter matches
// by substring, not exactly) before creating it again, rather than
// retrying blind.
//
// Without NoWait, CreateSecurityGroup then waits for the new group to
// reach ACTIVE. If the group reaches ERROR instead, or the wait's bound
// runs out, or a read or a sleep in that wait fails, such as from a
// canceled ctx, the returned error wraps ErrFailed or ErrNotSettled and the
// Output still holds the group: the last one a read returned, or, if none
// did, the group the create response itself carried. Either way the Output
// is never nil and the caller keeps the new group's id.
func (c *Client) CreateSecurityGroup(ctx context.Context, in *CreateSecurityGroupInput) (*CreateSecurityGroupOutput, error) {
	const op = "network.CreateSecurityGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp createSecurityGroupResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "secgroups"}, nil),
		Body:      securityGroupWriteBody{Name: in.Name, Description: in.Description},
		OK:        []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousSecurityGroupCreateErr(op, err)
	}
	if resp.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	group := SecurityGroup{ID: resp.UUID, Name: resp.SecgroupName, Description: in.Description}
	if in.NoWait {
		return &CreateSecurityGroupOutput{SecurityGroup: group}, nil
	}

	settled, waitErr := c.waitSecurityGroupActive(ctx, op, resp.UUID)
	if settled == nil {
		// The create already succeeded; no read after it ever came back, so
		// fall back to the create response itself, which at least carries
		// the new group's id, rather than losing it to a nil Output.
		settled = &group
	}
	return &CreateSecurityGroupOutput{SecurityGroup: *settled}, waitErr
}

// wrapAmbiguousSecurityGroupCreateErr wraps err, from the create POST op
// just sent, with a hint to list security groups before creating again,
// unless err is already a 4xx *core.APIError: a 4xx means the server
// rejected the request outright, so nothing was created and the exact same
// call is safe to retry. Any other error, a 5xx or a failure before any
// response ever came back, leaves whether the group was created unknown.
func wrapAmbiguousSecurityGroupCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list security groups and match the name exactly before creating it again: %w", op, err)
}

// UpdateSecurityGroupInput changes a group's Name, Description, or both,
// leaving any field left nil unchanged. At least one of Name and
// Description must be set. The API replaces both fields on every update, so
// UpdateSecurityGroup reads the group first and resends whichever field the
// caller leaves nil unchanged; the last writer wins, since the API has no
// condition field.
type UpdateSecurityGroupInput struct {
	SecurityGroupID string `vngcloud:"required"`

	Name        *string
	Description *string
}

type UpdateSecurityGroupOutput struct {
	SecurityGroup SecurityGroup
}

// UpdateSecurityGroup changes a group's name, description, or both. It
// reads the group first with GetSecurityGroup and refuses one whose read
// shows System, with ErrSystemGroup, before sending anything: renaming or
// redescribing a project's system group is never intended. It then resends
// every field the caller left nil unchanged.
//
// The PUT's own response shape is not verified, so UpdateSecurityGroup
// reads the group once more afterward and returns that read as the Output.
// If that second read fails, the write has already succeeded: the returned
// error wraps ErrNotSettled and the Output falls back to the fields the PUT
// itself sent.
func (c *Client) UpdateSecurityGroup(ctx context.Context, in *UpdateSecurityGroupInput) (*UpdateSecurityGroupOutput, error) {
	const op = "network.UpdateSecurityGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SecurityGroupID", in.SecurityGroupID); err != nil {
		return nil, err
	}
	if in.Name == nil && in.Description == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	current, err := c.GetSecurityGroup(ctx, &GetSecurityGroupInput{SecurityGroupID: in.SecurityGroupID})
	if err != nil {
		return nil, err
	}
	if current.SecurityGroup.System {
		return nil, fmt.Errorf("%w: %s: security group %s is a system group", ErrSystemGroup, op, in.SecurityGroupID)
	}

	name := current.SecurityGroup.Name
	if in.Name != nil {
		name = *in.Name
	}
	description := current.SecurityGroup.Description
	if in.Description != nil {
		description = *in.Description
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID}, nil),
		Body:      securityGroupWriteBody{Name: name, Description: description},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	// The PUT above already succeeded, so from here on every error means
	// the write may have landed and must not be sent again; fallback is
	// what the caller falls back to when the confirm read below fails,
	// built from the fields the PUT itself sent.
	fallback := SecurityGroup{ID: in.SecurityGroupID, Name: name, Description: description}
	updated, err := c.GetSecurityGroup(ctx, &GetSecurityGroupInput{SecurityGroupID: in.SecurityGroupID})
	if err != nil {
		return &UpdateSecurityGroupOutput{SecurityGroup: fallback}, fmt.Errorf("%w: %s: security group %s: %w", ErrNotSettled, op, in.SecurityGroupID, err)
	}
	return &UpdateSecurityGroupOutput{SecurityGroup: updated.SecurityGroup}, nil
}

// DeleteSecurityGroupInput identifies the group to delete.
type DeleteSecurityGroupInput struct {
	SecurityGroupID string `vngcloud:"required"`
}

type DeleteSecurityGroupOutput struct{}

// DeleteSecurityGroup deletes a security group. It reads the group first
// with GetSecurityGroup and sends nothing when that read shows the group is
// a system group (ErrSystemGroup) or has any server attached, checked with
// ListServersBySecurityGroup (ErrSecurityGroupInUse).
//
// A group can be in use by more than servers, so the server's own refusal
// is the final guard: an error whose message contains
// "securitygroupinuse" (case-insensitive), at any status, also wraps
// ErrSecurityGroupInUse.
//
// The delete itself is taken as synchronous: the Output is {} once the 204
// response comes back. DELETE is idempotent and keeps the transport's
// normal retries; a retry that finds the group already gone returns
// NotFound.
func (c *Client) DeleteSecurityGroup(ctx context.Context, in *DeleteSecurityGroupInput) (*DeleteSecurityGroupOutput, error) {
	const op = "network.DeleteSecurityGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SecurityGroupID", in.SecurityGroupID); err != nil {
		return nil, err
	}

	group, err := c.GetSecurityGroup(ctx, &GetSecurityGroupInput{SecurityGroupID: in.SecurityGroupID})
	if err != nil {
		return nil, err
	}
	if group.SecurityGroup.System {
		return nil, fmt.Errorf("%w: %s: security group %s is a system group", ErrSystemGroup, op, in.SecurityGroupID)
	}
	servers, err := c.ListServersBySecurityGroup(ctx, &ListServersBySecurityGroupInput{SecurityGroupID: in.SecurityGroupID})
	if err != nil {
		return nil, err
	}
	if len(servers.Items) > 0 {
		return nil, fmt.Errorf("%w: %s: security group %s has %d server(s) attached", ErrSecurityGroupInUse, op, in.SecurityGroupID, len(servers.Items))
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, wrapSecurityGroupInUse(err)
	}
	return &DeleteSecurityGroupOutput{}, nil
}

// wrapSecurityGroupInUse rewraps err with ErrSecurityGroupInUse when it is
// a *core.APIError whose message contains "securitygroupinuse"
// (case-insensitive), whatever its status: DeleteSecurityGroup's own
// pre-read guard above already catches a group with servers, but a group
// can be in use by more than servers, and the server's own refusal is the
// final guard for that. Any other error passes through unchanged.
func wrapSecurityGroupInUse(err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Message), "securitygroupinuse") {
		return fmt.Errorf("%w: %w", ErrSecurityGroupInUse, apiErr)
	}
	return err
}
