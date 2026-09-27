package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreateGroupInput names the new group. It has no guard: creating a group
// with no initial members or policies changes no one's rights.
type CreateGroupInput struct {
	Name        string `vngcloud:"required"`
	Description string
}

type CreateGroupOutput struct {
	Group Group
}

// createGroupBody always sends Mode "iam" and never IamUsers or Policies, so
// a group's membership and attached policies change only through
// AddUserToGroup, RemoveUserFromGroup, AttachGroupPolicy, and
// DetachGroupPolicy.
type createGroupBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

type createGroupResponse struct {
	ID string `json:"id"`
}

// CreateGroup creates an "iam"-mode group with no members or policies, and
// reads it back with GetGroup.
//
// If that confirm read fails, CreateGroup still returns a non-nil Output,
// its Group holding only the new ID (every other field left zero), and
// wraps ErrNotSettled rather than returning nil: the group was really
// created either way, and the caller can look it up again with GetGroup or
// list-groups.
//
// It sets transport.Request.Once: a resend after a 401 or a followed
// redirect would create a second group, so the request is sent at most
// once. After any error that is not a 4xx *core.APIError, the group may
// exist regardless, and the caller lists groups before creating it again,
// rather than retrying blind.
func (c *Client) CreateGroup(ctx context.Context, in *CreateGroupInput) (*CreateGroupOutput, error) {
	const op = "iam.CreateGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var resp createGroupResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.policiesURL([]string{"groups"}, nil),
		Body:      createGroupBody{Name: in.Name, Description: in.Description, Mode: "iam"},
		OK:        []int{201},
		Once:      true,
	}
	if _, err := c.c.DoJSONStatus(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousGroupCreateErr(op, err)
	}
	if resp.ID == "" {
		return nil, &core.APIError{Operation: op, Message: "create response had no id; a group may exist, check with list-groups"}
	}

	got, err := c.GetGroup(ctx, &GetGroupInput{GroupID: resp.ID})
	if err != nil {
		out := &CreateGroupOutput{Group: Group{ID: resp.ID}}
		return out, fmt.Errorf("%s: group %s was created but the read to confirm it failed: %w: %w", op, resp.ID, ErrNotSettled, err)
	}
	return &CreateGroupOutput{Group: got.Group}, nil
}

// wrapAmbiguousGroupCreateErr wraps err from CreateGroup when it failed
// ambiguously: a 5xx or a network error, where whether the request reached
// the server is unknown. It is never called for a 4xx *core.APIError, which
// means the request was rejected outright and nothing was created. A nil err
// stays nil.
func wrapAmbiguousGroupCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; check with list-groups before creating it again: %w", op, err)
}

// UpdateGroupInput's Name is read from the group's current state when left
// nil, because the PATCH requires a name; Description is sent only when
// non-nil. It has no guard beyond read-only: changing a group's name or
// description changes no one's rights.
type UpdateGroupInput struct {
	GroupID     string `vngcloud:"required"`
	Name        *string
	Description *string
}

type UpdateGroupOutput struct {
	Group Group
}

type updateGroupBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// UpdateGroup changes a group's name or description with a PATCH, reading
// the group first to fill Name when the caller leaves it nil, and reads the
// group back afterward to build the Output.
//
// If the confirm read after the PATCH fails, UpdateGroup still returns a
// non-nil Output, its Group holding only GroupID (every other field left
// zero), and wraps ErrNotSettled rather than returning nil: the update
// already reached the server either way.
//
// The request sets Idempotent: it sends the update's full intended state,
// so the transport's own retry after a 5xx is safe to repeat.
func (c *Client) UpdateGroup(ctx context.Context, in *UpdateGroupInput) (*UpdateGroupOutput, error) {
	const op = "iam.UpdateGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}

	var name string
	if in.Name != nil {
		name = *in.Name
	} else {
		got, err := c.GetGroup(ctx, &GetGroupInput{GroupID: in.GroupID})
		if err != nil {
			return nil, err
		}
		name = got.Group.Name
	}

	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPatch,
		URL:        c.policiesURL([]string{"groups", in.GroupID}, nil),
		Body:       updateGroupBody{Name: name, Description: in.Description},
		OK:         []int{204},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	got, err := c.GetGroup(ctx, &GetGroupInput{GroupID: in.GroupID})
	if err != nil {
		out := &UpdateGroupOutput{Group: Group{ID: in.GroupID}}
		return out, fmt.Errorf("%s: group %s was updated but the read to confirm it failed: %w: %w", op, in.GroupID, ErrNotSettled, err)
	}
	return &UpdateGroupOutput{Group: got.Group}, nil
}

// DeleteGroupInput identifies the group to delete. It must have no member
// and no attached policy; see group_guard.go.
type DeleteGroupInput struct {
	GroupID string `vngcloud:"required"`
}

type DeleteGroupOutput struct{}

// DeleteGroup deletes a group. The guard runs first and sends nothing when
// it refuses: ErrInUse if the group has a member or an attached policy,
// protected or not, since a delete would silently remove rights from every
// member. DELETE is idempotent and keeps the transport's own retries.
func (c *Client) DeleteGroup(ctx context.Context, in *DeleteGroupInput) (*DeleteGroupOutput, error) {
	const op = "iam.DeleteGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}
	if err := c.guardDeleteGroup(ctx, op, in.GroupID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.policiesURL([]string{"groups", in.GroupID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteGroupOutput{}, nil
}
