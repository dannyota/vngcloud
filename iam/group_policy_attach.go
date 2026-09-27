package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// AttachGroupPolicyInput names the policy and the group to attach it to. The
// guard refuses, with no request sent, when the policy is privileged or the
// target is protected; see group_guard.go.
type AttachGroupPolicyInput struct {
	PolicyID string `vngcloud:"required"`
	GroupID  string `vngcloud:"required"`
}

type AttachGroupPolicyOutput struct{}

// AttachGroupPolicy attaches PolicyID to GroupID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// the change would affect the caller's own rights (GroupID is a group the
// caller belongs to), or ErrPrivilegedChange if the policy is privileged or
// the target group otherwise holds one.
//
// It is a plain POST, retried by the transport only after a 429 or a failed
// dial (ADR 0002 rule 2): a repeat is visible as the server's own Conflict.
// After any other failure, list-policy-attachments shows whether the attach
// landed.
func (c *Client) AttachGroupPolicy(ctx context.Context, in *AttachGroupPolicyInput) (*AttachGroupPolicyOutput, error) {
	const op = "iam.AttachGroupPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}
	if err := c.guardGroupPolicyAttach(ctx, op, in.PolicyID, in.GroupID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "groups", in.GroupID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &AttachGroupPolicyOutput{}, nil
}

// DetachGroupPolicyInput names the policy and the group to detach it from.
// The guard refuses, with no request sent, when the policy is privileged or
// the target is protected; see group_guard.go.
type DetachGroupPolicyInput struct {
	PolicyID string `vngcloud:"required"`
	GroupID  string `vngcloud:"required"`
}

type DetachGroupPolicyOutput struct{}

// DetachGroupPolicy detaches PolicyID from GroupID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// the change would affect the caller's own rights, or ErrPrivilegedChange if
// the policy is privileged or the target group holds one. The design refuses
// a privileged policy's detach the same as its attach.
//
// DELETE is idempotent and keeps the transport's own retries; a retried
// detach that finds nothing attached returns NotFound.
func (c *Client) DetachGroupPolicy(ctx context.Context, in *DetachGroupPolicyInput) (*DetachGroupPolicyOutput, error) {
	const op = "iam.DetachGroupPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}
	if err := c.guardGroupPolicyAttach(ctx, op, in.PolicyID, in.GroupID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "groups", in.GroupID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DetachGroupPolicyOutput{}, nil
}
