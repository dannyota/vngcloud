package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// AddUserToGroupInput names the group and the IAM user to add to it. The
// guard refuses, with no request sent, when the user or the group is
// protected; see group_guard.go.
type AddUserToGroupInput struct {
	GroupID string `vngcloud:"required"`
	UserID  string `vngcloud:"required"`
}

type AddUserToGroupOutput struct{}

// AddUserToGroup adds UserID as a member of GroupID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// the change would affect the caller's own rights (UserID is the caller, or
// GroupID is a group the caller already belongs to), or ErrPrivilegedChange
// if the user or the group otherwise holds a privileged policy.
//
// It is a plain POST, retried by the transport only after a 429 or a failed
// dial (ADR 0002 rule 2): a repeat is visible as the server's own Conflict.
// After any other failure, get-group shows whether the add landed.
func (c *Client) AddUserToGroup(ctx context.Context, in *AddUserToGroupInput) (*AddUserToGroupOutput, error) {
	const op = "iam.AddUserToGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	if err := c.guardGroupMembershipWrite(ctx, op, in.GroupID, in.UserID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.policiesURL([]string{"groups", in.GroupID, "iam-users", in.UserID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &AddUserToGroupOutput{}, nil
}

// RemoveUserFromGroupInput names the group and the IAM user to remove from
// it. The guard refuses, with no request sent, when the user or the group
// is protected; see group_guard.go.
type RemoveUserFromGroupInput struct {
	GroupID string `vngcloud:"required"`
	UserID  string `vngcloud:"required"`
}

type RemoveUserFromGroupOutput struct{}

// RemoveUserFromGroup removes UserID as a member of GroupID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// the change would affect the caller's own rights, or ErrPrivilegedChange if
// the user or the group otherwise holds a privileged policy. The design
// refuses a protected user or group the same way for a remove as for an add.
//
// DELETE is idempotent and keeps the transport's own retries; a retried
// remove that finds nothing attached returns NotFound.
func (c *Client) RemoveUserFromGroup(ctx context.Context, in *RemoveUserFromGroupInput) (*RemoveUserFromGroupOutput, error) {
	const op = "iam.RemoveUserFromGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	if err := c.guardGroupMembershipWrite(ctx, op, in.GroupID, in.UserID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.policiesURL([]string{"groups", in.GroupID, "iam-users", in.UserID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &RemoveUserFromGroupOutput{}, nil
}
