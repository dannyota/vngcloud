package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// AttachUserPolicyInput names the policy and the IAM user to attach it to.
// The guard refuses, with no request sent, when the policy is privileged or
// the target is protected; see group_guard.go.
type AttachUserPolicyInput struct {
	PolicyID string `vngcloud:"required"`
	UserID   string `vngcloud:"required"`
}

type AttachUserPolicyOutput struct{}

// AttachUserPolicy attaches PolicyID directly to UserID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// UserID is the caller, or ErrPrivilegedChange if the policy is privileged
// or the target user otherwise holds one, directly or through a group.
//
// It is a plain POST, retried by the transport only after a 429 or a failed
// dial (ADR 0002 rule 2): a repeat is visible as the server's own Conflict.
// After any other failure, list-user-policies shows whether the attach
// landed.
func (c *Client) AttachUserPolicy(ctx context.Context, in *AttachUserPolicyInput) (*AttachUserPolicyOutput, error) {
	const op = "iam.AttachUserPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	if err := c.guardUserPolicyAttach(ctx, op, in.PolicyID, in.UserID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "iam-users", in.UserID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &AttachUserPolicyOutput{}, nil
}

// DetachUserPolicyInput names the policy and the IAM user to detach it from.
// The guard refuses, with no request sent, when the policy is privileged or
// the target is protected; see group_guard.go.
type DetachUserPolicyInput struct {
	PolicyID string `vngcloud:"required"`
	UserID   string `vngcloud:"required"`
}

type DetachUserPolicyOutput struct{}

// DetachUserPolicy detaches PolicyID from UserID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// UserID is the caller, or ErrPrivilegedChange if the policy is privileged
// or the target user holds one. The design refuses a privileged policy's
// detach the same as its attach.
//
// DELETE is idempotent and keeps the transport's own retries; a retried
// detach that finds nothing attached returns NotFound.
func (c *Client) DetachUserPolicy(ctx context.Context, in *DetachUserPolicyInput) (*DetachUserPolicyOutput, error) {
	const op = "iam.DetachUserPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	if err := c.guardUserPolicyAttach(ctx, op, in.PolicyID, in.UserID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "iam-users", in.UserID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DetachUserPolicyOutput{}, nil
}
