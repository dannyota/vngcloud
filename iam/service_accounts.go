package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListServiceAccountsInput pages the account's service accounts, optionally
// filtered by Name. A non-positive Size sends core.DefaultPageSize, and
// Page 0 is the first page.
type ListServiceAccountsInput struct {
	Name string
	Page int
	Size int
}

type ListServiceAccountsOutput = core.List[ServiceAccount]

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
	var resp struct {
		Data []ServiceAccount `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.accountsURL([]string{"service-accounts"}, q),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListServiceAccountsOutput{Items: resp.Data}, nil
}

type GetServiceAccountInput struct {
	ServiceAccountID string `vngcloud:"required"`
}

type GetServiceAccountOutput struct {
	ServiceAccount ServiceAccount
}

func (c *Client) GetServiceAccount(ctx context.Context, in *GetServiceAccountInput) (*GetServiceAccountOutput, error) {
	const op = "iam.GetServiceAccount"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}

	var sa ServiceAccount
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.accountsURL([]string{"service-accounts", in.ServiceAccountID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &sa); err != nil {
		return nil, err
	}
	return &GetServiceAccountOutput{ServiceAccount: sa}, nil
}
