package iam

import (
	"context"
	"fmt"
	"net/http"
	"unicode"
	"unicode/utf8"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const (
	userObjectMaxBody = 256 << 10
	userListMaxBody   = 4 << 20
)

type GetUserInput struct {
	UserID string `vngcloud:"required"`
}

type GetUserOutput struct {
	User UserDetail
}

// userDetailWire keeps identity fields visible to strict JSON validation.
type userDetailWire struct {
	ID        string      `json:"id"`
	Username  string      `json:"username"`
	CreatedAt epochMillis `json:"createdAt"`
	Enabled   bool        `json:"enabled"`
	From      string      `json:"from"`
	LastUse   epochMillis `json:"lastUse"`
}

// userReadOptions carries operation-local privacy controls for confirmation
// reads. SentCredentials accumulates credentials from separate per-call slices.
// Callers must not share these options across concurrent operations.
type userReadOptions struct {
	Redact          []string
	WithholdMessage string
	ClassifyError   func(int, string) string
	SentCredentials *[]string
}

func (o userReadOptions) request(req transport.Request, sent *[]string) transport.Request {
	req.Sensitive = true
	req.Redact = append([]string(nil), o.Redact...)
	if o.SentCredentials != nil {
		req.Redact = append(req.Redact, (*o.SentCredentials)...)
	}
	req.WithholdMessage = o.WithholdMessage
	req.ClassifyError = o.ClassifyError
	req.SentCredentials = sent
	return req
}

func (o userReadOptions) collect(sent []string) {
	if o.SentCredentials != nil {
		*o.SentCredentials = append(*o.SentCredentials, sent...)
	}
}

// GetUser reads one IAM user's details. LastUse is zero before first sign-in.
func (c *Client) GetUser(ctx context.Context, in *GetUserInput) (*GetUserOutput, error) {
	const op = "iam.GetUser"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	var sent []string
	defer func() { clear(sent) }()
	opts := userReadOptions{SentCredentials: &sent}
	user, err := c.getUser(ctx, op, in.UserID, opts)
	if err != nil {
		return nil, err
	}
	if opts.containsCredential(user.ID, user.Username) {
		return nil, fmt.Errorf("iam: %s: unsafe user identity", op)
	}
	return &GetUserOutput{User: *user}, nil
}

func (c *Client) getUser(ctx context.Context, op, id string, opts userReadOptions) (*UserDetail, error) {
	var resp *userDetailWire
	var sent []string
	req := opts.request(transport.Request{
		Operation: op, Method: http.MethodGet,
		URL: c.accountsURL([]string{"iam-users", id}, nil), OK: []int{200},
		StrictJSON: true, MaxBody: userObjectMaxBody,
	}, &sent)
	err := c.c.DoJSON(ctx, req, &resp)
	opts.collect(sent)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("iam: %s: missing user object", op)
	}
	return &UserDetail{
		ID: resp.ID, Username: resp.Username, CreatedAt: int64(resp.CreatedAt),
		Enabled: resp.Enabled, From: resp.From, LastUse: int64(resp.LastUse),
	}, nil
}

func checkUserName(op, field, value string) error {
	if !utf8.ValidString(value) || len(value) > 4096 {
		return fmt.Errorf("%w: %s: %s must be valid UTF-8 and at most 4096 bytes", core.ErrInvalidInput, op, field)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: %s: %s must not contain whitespace or controls", core.ErrInvalidInput, op, field)
		}
	}
	return nil
}
