package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListPoliciesInput pages the account's policies, optionally filtered by
// Name. A non-positive Size sends core.DefaultPageSize, and Page 0 is the
// first page.
type ListPoliciesInput struct {
	Name string
	Page int
	Size int
}

type ListPoliciesOutput = core.List[PolicySummary]

func (c *Client) ListPolicies(ctx context.Context, in *ListPoliciesInput) (*ListPoliciesOutput, error) {
	const op = "iam.ListPolicies"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListPoliciesInput{}
	}

	q := pageQuery(in.Page, in.Size)
	if in.Name != "" {
		q.Set("name", in.Name)
	}
	var resp struct {
		Data []PolicySummary `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"policies"}, q),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListPoliciesOutput{Items: resp.Data}, nil
}

type GetPolicyInput struct {
	PolicyID string `vngcloud:"required"`
}

type GetPolicyOutput struct {
	Policy Policy
}

func (c *Client) GetPolicy(ctx context.Context, in *GetPolicyInput) (*GetPolicyOutput, error) {
	const op = "iam.GetPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}

	var policy Policy
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"policies", in.PolicyID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &policy); err != nil {
		return nil, err
	}
	return &GetPolicyOutput{Policy: policy}, nil
}

// ListPolicyAttachmentsInput identifies the policy whose attachments to
// list.
type ListPolicyAttachmentsInput struct {
	PolicyID string `vngcloud:"required"`
}

// ListPolicyAttachmentsOutput lists every principal PolicyID is attached
// to: Groups by summary, and users and service accounts by ID only, which
// is all the API returns for those two.
type ListPolicyAttachmentsOutput struct {
	Groups            []GroupSummary
	UserIDs           []string
	ServiceAccountIDs []string
}

// ListPolicyAttachments makes the three policies/{id}/... reads: groups,
// iam-users, and service-accounts.
func (c *Client) ListPolicyAttachments(ctx context.Context, in *ListPolicyAttachmentsInput) (*ListPolicyAttachmentsOutput, error) {
	const op = "iam.ListPolicyAttachments"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}

	groups := []GroupSummary{}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "groups"}, nil),
		OK:        []int{200},
	}, &groups); err != nil {
		return nil, err
	}

	userIDs := []string{}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "iam-users"}, nil),
		OK:        []int{200},
	}, &userIDs); err != nil {
		return nil, err
	}

	serviceAccountIDs := []string{}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"policies", in.PolicyID, "service-accounts"}, nil),
		OK:        []int{200},
	}, &serviceAccountIDs); err != nil {
		return nil, err
	}

	return &ListPolicyAttachmentsOutput{
		Groups:            groups,
		UserIDs:           userIDs,
		ServiceAccountIDs: serviceAccountIDs,
	}, nil
}
