package iam

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// GetCallerIdentityInput takes no fields.
type GetCallerIdentityInput struct{}

// GetCallerIdentityOutput identifies the token's principal. UserType is
// "root-user", "iam-user", "user-sa", or "service-sa"; an unrecognized value
// fails every guarded write (see guard.go). The root email and two-factor
// fields the API returns are dropped: neither is account data the SDK needs
// to keep.
type GetCallerIdentityOutput struct {
	UserID    string
	UserType  string
	Username  string
	AccountID int64
}

// userInfoResponse is auth/userinfo's wire shape. rootEmail and
// twoFactorAuth are left out on purpose; encoding/json ignores JSON fields a
// destination struct does not declare.
type userInfoResponse struct {
	UserID    string `json:"userId"`
	UserType  string `json:"userType"`
	Username  string `json:"username"`
	AccountID int64  `json:"accountId"`
}

// GetCallerIdentity reads the token's own principal: an IAM user or a
// service account. The guards in guard.go call this once per Client and
// reuse the result.
func (c *Client) GetCallerIdentity(ctx context.Context, in *GetCallerIdentityInput) (*GetCallerIdentityOutput, error) {
	const op = "iam.GetCallerIdentity"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var resp userInfoResponse
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodGet,
		URL:        c.accountsURL([]string{"auth", "userinfo"}, nil),
		OK:         []int{200},
		StrictJSON: true,
		MaxBody:    guardObjectMaxBody,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &GetCallerIdentityOutput{
		UserID:    resp.UserID,
		UserType:  resp.UserType,
		Username:  resp.Username,
		AccountID: resp.AccountID,
	}, nil
}

// ListUsersInput pages the account's IAM users. A non-positive Size sends
// core.DefaultPageSize, and Page 0 is the first page. Name is an optional
// server-side filter, sent without trimming or case normalization.
type ListUsersInput struct {
	Page int
	Size int
	Name string
}

type ListUsersOutput = core.List[User]

// ListUsers lists one page of the account's IAM users. The server has been
// observed to match Name as a case-insensitive substring.
func (c *Client) ListUsers(ctx context.Context, in *ListUsersInput) (*ListUsersOutput, error) {
	const op = "iam.ListUsers"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListUsersInput{}
	}

	if err := checkUserName(op, "Name", in.Name); err != nil {
		return nil, err
	}
	q := pageQuery(in.Page, in.Size)
	if in.Name != "" {
		q.Set("name", in.Name)
	}

	var sent []string
	defer func() { clear(sent) }()
	var resp struct {
		Data []User `json:"data"`
	}
	req := transport.Request{
		Operation:       op,
		Method:          http.MethodGet,
		URL:             c.accountsURL([]string{"iam-users"}, q),
		OK:              []int{200},
		MaxBody:         userListMaxBody,
		StrictJSON:      in.Name != "",
		SentCredentials: &sent,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	opts := userReadOptions{SentCredentials: &sent}
	for _, row := range resp.Data {
		if opts.containsCredential(row.ID, row.Username) {
			return nil, fmt.Errorf("iam: %s: unsafe user identity", op)
		}
	}
	return &ListUsersOutput{Items: resp.Data}, nil
}
