package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// AttachServiceAccountPolicyInput names the policy and the service account
// to attach it to. The guard refuses, with no request sent, when the policy
// is privileged or the target is protected; see guard.go.
type AttachServiceAccountPolicyInput struct {
	PolicyID         string `vngcloud:"required"`
	ServiceAccountID string `vngcloud:"required"`
}

type AttachServiceAccountPolicyOutput struct{}

// AttachServiceAccountPolicy attaches PolicyID to ServiceAccountID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// the caller is a service account, or ErrPrivilegedChange if the policy is
// privileged or the target service account already holds one.
//
// It is a plain POST, retried by the transport only after a 429 or a failed
// dial (ADR 0002 rule 2): a repeat is visible as the server's own Conflict,
// so there is no Once or ambiguous-failure wrapping to make here, unlike
// CreatePolicy. After any other failure, list-policy-attachments shows
// whether the attach landed.
func (c *Client) AttachServiceAccountPolicy(ctx context.Context, in *AttachServiceAccountPolicyInput) (*AttachServiceAccountPolicyOutput, error) {
	const op = "iam.AttachServiceAccountPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	if err := c.guardServiceAccountPolicyAttach(ctx, op, in.PolicyID, in.ServiceAccountID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "service-accounts", in.ServiceAccountID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &AttachServiceAccountPolicyOutput{}, nil
}

// DetachServiceAccountPolicyInput names the policy and the service account
// to detach it from. The guard refuses, with no request sent, when the
// policy is privileged or the target is protected; see guard.go.
type DetachServiceAccountPolicyInput struct {
	PolicyID         string `vngcloud:"required"`
	ServiceAccountID string `vngcloud:"required"`
}

type DetachServiceAccountPolicyOutput struct{}

// DetachServiceAccountPolicy detaches PolicyID from ServiceAccountID.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// the caller is a service account, or ErrPrivilegedChange if the policy is
// privileged or the target service account holds one. The design refuses a
// privileged policy's detach the same as its attach.
//
// DELETE is idempotent and keeps the transport's own retries; a retried
// detach that finds nothing attached returns NotFound.
func (c *Client) DetachServiceAccountPolicy(ctx context.Context, in *DetachServiceAccountPolicyInput) (*DetachServiceAccountPolicyOutput, error) {
	const op = "iam.DetachServiceAccountPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	if err := c.guardServiceAccountPolicyAttach(ctx, op, in.PolicyID, in.ServiceAccountID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "service-accounts", in.ServiceAccountID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DetachServiceAccountPolicyOutput{}, nil
}
