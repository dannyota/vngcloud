package iam

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListServiceAccountsInput pages the account's service accounts, optionally
// filtered by Name. A non-positive Size sends core.DefaultPageSize, and
// Page 0 is the first page.
type ListServiceAccountsInput struct {
	// Name is sent as the name query parameter when nonempty.
	Name string
	// Page starts at zero; negative values select zero.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListServiceAccountsOutput holds ServiceAccount items and the returned nullable page metadata.
type ListServiceAccountsOutput = pageList[ServiceAccount]

// ListServiceAccounts reads service accounts.
func (c *Client) ListServiceAccounts(ctx context.Context, in *ListServiceAccountsInput) (*ListServiceAccountsOutput, error) {
	const op = "iam.ListServiceAccounts"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListServiceAccountsInput{}
	}

	q := pageQuery(in.Page, in.Size)
	if in.Name != "" {
		q.Set("name", in.Name)
	}
	var resp pageResponse[ServiceAccount]
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.accountsURL([]string{"service-accounts"}, q),
		OK:        []int{200},
	}
	var sent []string
	defer func() { clear(sent) }()
	opts := guardUserReadOptions(userReadOptions{SentCredentials: &sent})
	req.StrictJSON = true
	req.MaxBody = guardListMaxBody
	if err := c.guardReadJSON(ctx, "service accounts", req, &resp, opts); err != nil {
		return nil, err
	}
	return resp.output(), nil
}

// GetServiceAccountInput requires ServiceAccountID.
type GetServiceAccountInput struct {
	// ServiceAccountID is required; use ASCII letters, digits, or hyphens.
	ServiceAccountID string `vngcloud:"required"`
}

// GetServiceAccountOutput holds the result of GetServiceAccount.
type GetServiceAccountOutput struct {
	ServiceAccount ServiceAccount
}

// GetServiceAccount reads service account.
func (c *Client) GetServiceAccount(ctx context.Context, in *GetServiceAccountInput) (*GetServiceAccountOutput, error) {
	return c.getServiceAccount(ctx, in, userReadOptions{})
}

func (c *Client) getServiceAccount(ctx context.Context, in *GetServiceAccountInput, opts userReadOptions) (*GetServiceAccountOutput, error) {
	const op = "iam.GetServiceAccount"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}

	var credentials []string
	defer func() { clear(credentials) }()
	if opts.SentCredentials == nil {
		opts.SentCredentials = &credentials
	}
	opts = guardUserReadOptions(opts)
	if opts.containsCredential(in.ServiceAccountID) {
		return nil, fmt.Errorf("%s: unsafe service account identity", op)
	}
	type fields ServiceAccount
	var sa fields
	resp := struct {
		CreatedAt epochMillis `json:"createdAt"`
		LastUse   epochMillis `json:"lastUse"`
		*fields
	}{fields: &sa}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.accountsURL([]string{"service-accounts", in.ServiceAccountID}, nil),
		OK:        []int{200},
	}
	req.StrictJSON = true
	req.MaxBody = guardObjectMaxBody
	if err := c.guardReadJSON(ctx, "service account", req, &resp, opts); err != nil {
		return nil, err
	}
	if core.CheckPathID(op, "ServiceAccountID", sa.ID) != nil || !strings.EqualFold(sa.ID, in.ServiceAccountID) {
		return nil, fmt.Errorf("%s: service account identity is unproven", op)
	}
	sa.CreatedAt = int64(resp.CreatedAt)
	sa.LastUse = int64(resp.LastUse)
	out := &GetServiceAccountOutput{ServiceAccount: ServiceAccount(sa)}
	if err := guardCachedResponse(op, out, opts); err != nil {
		return nil, err
	}
	return out, nil
}
