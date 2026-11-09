package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListUserPoliciesInput pages UserID's attached policies. A non-positive
// Size sends core.DefaultPageSize, and Page 0 is the first page.
type ListUserPoliciesInput struct {
	// UserID is required; use ASCII letters, digits, or hyphens.
	UserID string `vngcloud:"required"`
	// Page starts at zero; negative values select zero.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListUserPoliciesOutput holds PolicySummary items.
type ListUserPoliciesOutput = core.List[PolicySummary]

// ListUserPolicies reads user policies.
func (c *Client) ListUserPolicies(ctx context.Context, in *ListUserPoliciesInput) (*ListUserPoliciesOutput, error) {
	const op = "iam.ListUserPolicies"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}

	var resp struct {
		Data []PolicySummary `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"user-attachments", "iam-users", in.UserID, "policies"}, pageQuery(in.Page, in.Size)),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListUserPoliciesOutput{Items: resp.Data}, nil
}

// ListUserGroupsInput identifies the user whose groups to list. This call
// returns a bare array of full Group objects, unlike ListGroups, which
// returns summary rows only.
type ListUserGroupsInput struct {
	// UserID is required; use ASCII letters, digits, or hyphens.
	UserID string `vngcloud:"required"`
}

// ListUserGroupsOutput holds Group items.
type ListUserGroupsOutput = core.List[Group]

// ListUserGroups reads user groups.
func (c *Client) ListUserGroups(ctx context.Context, in *ListUserGroupsInput) (*ListUserGroupsOutput, error) {
	const op = "iam.ListUserGroups"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}

	groups := []Group{}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"user-attachments", "iam-users", in.UserID, "groups"}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &groups); err != nil {
		return nil, err
	}
	return &ListUserGroupsOutput{Items: groups}, nil
}

// ListServiceAccountPoliciesInput pages ServiceAccountID's attached
// policies. A non-positive Size sends core.DefaultPageSize, and Page 0 is
// the first page.
type ListServiceAccountPoliciesInput struct {
	// ServiceAccountID is required; use ASCII letters, digits, or hyphens.
	ServiceAccountID string `vngcloud:"required"`
	// Page starts at zero; negative values select zero.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListServiceAccountPoliciesOutput holds PolicySummary items.
type ListServiceAccountPoliciesOutput = core.List[PolicySummary]

// ListServiceAccountPolicies reads service account policies.
func (c *Client) ListServiceAccountPolicies(ctx context.Context, in *ListServiceAccountPoliciesInput) (*ListServiceAccountPoliciesOutput, error) {
	const op = "iam.ListServiceAccountPolicies"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}

	var resp struct {
		Data []PolicySummary `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"user-attachments", "service-accounts", in.ServiceAccountID, "policies"}, pageQuery(in.Page, in.Size)),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListServiceAccountPoliciesOutput{Items: resp.Data}, nil
}
