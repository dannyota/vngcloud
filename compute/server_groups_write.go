package compute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

var (
	// ErrServerGroupInUse means a server group delete was refused because
	// the group has servers attached, found by a pre-delete list scan, or
	// because the server's own refusal named the group in use for some
	// other reason. In the first case nothing was sent; in the second, the
	// DELETE reached the server.
	ErrServerGroupInUse = errors.New("compute: server group in use")

	// ErrNotSettled means UpdateServerGroup's confirm read after a
	// successful PUT failed to come back. The write already reached the
	// server, but unlike a create, the update is safe to run again: it is
	// a read-merge PUT that always resends both fields, so a repeat
	// converges on the same result. The returned Output holds the fields
	// the PUT itself sent, so the caller keeps the group's id to check
	// again later.
	ErrNotSettled = errors.New("compute: write accepted but not settled")
)

// serverGroupWriteBody is the request body UpdateServerGroup's PUT sends.
// ServerGroupID is always the path UUID: a PUT without it fails with 400
// "serverGroupId: must not be null;", confirmed live. The API also takes
// tags and zoneId; the SDK sends neither.
type serverGroupWriteBody struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	ServerGroupID string `json:"serverGroupId"`
}

// createServerGroupBody is the request body CreateServerGroup's POST sends.
type createServerGroupBody struct {
	Name        string `json:"name"`
	PolicyID    string `json:"policyId"`
	Description string `json:"description"`
}

// CreateServerGroupInput creates a server group under one policy, which
// cannot change after create. ListServerGroupPolicies lists the available
// policies. The SDK checks only that PolicyID is present and matches the
// same safe-id pattern a path id must; the server checks that the policy
// exists.
type CreateServerGroupInput struct {
	Name     string `vngcloud:"required"`
	PolicyID string `vngcloud:"required"`

	Description string
}

type CreateServerGroupOutput struct {
	ServerGroup ServerGroup
}

// CreateServerGroup creates a server group. Server group names are unique
// per project; a repeat name fails with the server's own message
// ("name must be unique").
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// group may exist, and the caller lists server groups by Name and matches it
// exactly (list-server-groups' own name filter matches by substring, not
// exactly) before creating it again, rather than retrying blind.
//
// Confirmed live, the create response already carries the full group in the
// same shape GetServerGroup reads, so CreateServerGroup decodes it directly
// into ServerGroup. There is no wait: the group is usable at once.
func (c *Client) CreateServerGroup(ctx context.Context, in *CreateServerGroupInput) (*CreateServerGroupOutput, error) {
	const op = "compute.CreateServerGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data ServerGroup `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.computeURL("v2", []string{projectID, "serverGroups"}, nil),
		Body:      createServerGroupBody{Name: in.Name, PolicyID: in.PolicyID, Description: in.Description},
		OK:        []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousServerGroupCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the group may exist, list server groups and match the name exactly before creating it again"}
	}
	return &CreateServerGroupOutput{ServerGroup: resp.Data}, nil
}

// wrapAmbiguousServerGroupCreateErr wraps err, from the create POST op just
// sent, with a hint to list server groups before creating again, unless err
// is already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so nothing was created and the exact same call is safe
// to retry. Any other error, a 5xx or a failure before any response ever
// came back, leaves whether the group was created unknown.
func wrapAmbiguousServerGroupCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list server groups and match the name exactly before creating it again: %w", op, err)
}

// UpdateServerGroupInput changes a group's Name, Description, or both,
// leaving any field left nil unchanged. At least one of Name and
// Description must be set, and a Name that is set must not be the empty
// string. A group's policy cannot change after create, so there is no
// PolicyID field here. The API replaces both fields on every update, so
// UpdateServerGroup reads the group first and resends whichever field the
// caller leaves nil unchanged; the last writer wins, since the API has no
// condition field.
type UpdateServerGroupInput struct {
	ServerGroupID string `vngcloud:"required"`

	Name        *string
	Description *string
}

type UpdateServerGroupOutput struct {
	ServerGroup ServerGroup
}

// UpdateServerGroup changes a group's name, description, or both. It reads
// the group first with GetServerGroup and resends every field the caller
// left nil unchanged.
//
// The PUT's own response shape is not verified, so UpdateServerGroup reads
// the group once more afterward and returns that read as the Output. If
// that second read fails, the write has already succeeded: the returned
// error wraps ErrNotSettled and the Output falls back to the fields the PUT
// itself sent.
func (c *Client) UpdateServerGroup(ctx context.Context, in *UpdateServerGroupInput) (*UpdateServerGroupOutput, error) {
	const op = "compute.UpdateServerGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerGroupID", in.ServerGroupID); err != nil {
		return nil, err
	}
	if in.Name == nil && in.Description == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}
	if in.Name != nil && *in.Name == "" {
		return nil, fmt.Errorf("%w: %s: Name must not be empty", core.ErrInvalidInput, op)
	}

	current, err := c.GetServerGroup(ctx, &GetServerGroupInput{ServerGroupID: in.ServerGroupID})
	if err != nil {
		return nil, err
	}

	name := current.ServerGroup.Name
	if in.Name != nil {
		name = *in.Name
	}
	description := current.ServerGroup.Description
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
		URL:       c.computeURL("v2", []string{projectID, "serverGroups", in.ServerGroupID}, nil),
		Body:      serverGroupWriteBody{Name: name, Description: description, ServerGroupID: in.ServerGroupID},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	// The PUT above already succeeded, so from here on every error means
	// the write may have landed and must not be sent again; fallback is
	// what the caller falls back to when the confirm read below fails,
	// built from the fields the PUT itself sent.
	fallback := ServerGroup{UUID: in.ServerGroupID, Name: name, Description: description}
	updated, err := c.GetServerGroup(ctx, &GetServerGroupInput{ServerGroupID: in.ServerGroupID})
	if err != nil {
		return &UpdateServerGroupOutput{ServerGroup: fallback}, fmt.Errorf("%w: %s: server group %s: %w", ErrNotSettled, op, in.ServerGroupID, err)
	}
	return &UpdateServerGroupOutput{ServerGroup: updated.ServerGroup}, nil
}

type DeleteServerGroupInput struct {
	ServerGroupID string `vngcloud:"required"`
}

type DeleteServerGroupOutput struct{}

// DeleteServerGroup deletes a server group. GetServerGroup's response never
// carries the group's servers, so DeleteServerGroup instead scans
// ListServerGroups for a match on ServerGroupID and, when that match holds
// any server, sends nothing and returns ErrServerGroupInUse. A group
// DeleteServerGroup cannot find in the list is sent to the server unchecked.
//
// The server's own refusal is still the final guard for a group in use for
// some other reason: an error whose message contains "server group is in
// use" (case-insensitive), at any status, also wraps ErrServerGroupInUse.
//
// The delete itself is taken as synchronous: the Output is {} once the 204
// response comes back. DELETE keeps the transport's normal retries.
func (c *Client) DeleteServerGroup(ctx context.Context, in *DeleteServerGroupInput) (*DeleteServerGroupOutput, error) {
	const op = "compute.DeleteServerGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerGroupID", in.ServerGroupID); err != nil {
		return nil, err
	}

	groups, err := c.ListServerGroups(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, group := range groups.Items {
		if group.UUID == in.ServerGroupID && len(group.Servers) > 0 {
			return nil, fmt.Errorf("%w: %s: server group %s has %d server(s) attached", ErrServerGroupInUse, op, in.ServerGroupID, len(group.Servers))
		}
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.computeURL("v2", []string{projectID, "serverGroups", in.ServerGroupID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, wrapServerGroupInUse(err)
	}
	return &DeleteServerGroupOutput{}, nil
}

// wrapServerGroupInUse rewraps err with ErrServerGroupInUse when it is a
// *core.APIError whose message contains "server group is in use"
// (case-insensitive), whatever its status: DeleteServerGroup's own
// pre-delete list scan above already catches a group with servers, but the
// server's own refusal is the final guard for a group in use for some other
// reason. Any other error passes through unchanged.
func wrapServerGroupInUse(err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && strings.Contains(strings.ToLower(apiErr.Message), "server group is in use") {
		return fmt.Errorf("%w: %w", ErrServerGroupInUse, apiErr)
	}
	return err
}
