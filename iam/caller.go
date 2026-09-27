package iam

import (
	"context"
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
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.accountsURL([]string{"auth", "userinfo"}, nil),
		OK:        []int{200},
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
// core.DefaultPageSize, and Page 0 is the first page.
type ListUsersInput struct {
	Page int
	Size int
}

type ListUsersOutput = core.List[User]

// ListUsers lists the account's IAM users. It exists so a caller can find a
// user ID; the design excludes IAM user writes entirely (they change who
// can sign in).
func (c *Client) ListUsers(ctx context.Context, in *ListUsersInput) (*ListUsersOutput, error) {
	const op = "iam.ListUsers"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in == nil {
		in = &ListUsersInput{}
	}

	var resp struct {
		Data []User `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.accountsURL([]string{"iam-users"}, pageQuery(in.Page, in.Size)),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListUsersOutput{Items: resp.Data}, nil
}
