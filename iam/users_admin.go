package iam

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type ResetUserPasswordInput struct {
	UserID         string `vngcloud:"required"`
	Password       vngcloud.Secret
	PasswordLength int
}

// ResetUserPasswordOutput retains only a locally generated password, including
// when acceptance is unsettled or the send outcome is uncertain.
type ResetUserPasswordOutput struct{ Password vngcloud.Secret }

type resetUserPasswordBody struct {
	Password string `json:"password"`
}

// ResetUserPassword refuses self, non-IAM, and protected targets, then sends
// once. Persist a generated Password even when a partial output has an error.
// No confirmation, sign-in, rollback, or automatic recovery follows the send.
func (c *Client) ResetUserPassword(ctx context.Context, in *ResetUserPasswordInput) (*ResetUserPasswordOutput, error) {
	const op = "iam.ResetUserPassword"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	if in.Password != "" {
		if in.PasswordLength != 0 {
			return nil, fmt.Errorf("%w: %s: PasswordLength requires generation", core.ErrInvalidInput, op)
		}
		if err := checkPassword(in.Password.Reveal()); err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
	} else if _, err := passwordLength(in.PasswordLength); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	var credentials []string
	defer func() { clear(credentials) }()
	opts := userReadOptions{Redact: []string{in.Password.Reveal()}, WithholdMessage: userWriteWithheld, ClassifyError: userAdministrationErrorCode, SentCredentials: &credentials}
	if err := c.guardUserAdministration(ctx, op, in.UserID, opts); err != nil {
		return nil, err
	}
	password := in.Password
	var generated vngcloud.Secret
	if password == "" {
		p, err := generatePassword(ctx, in.PasswordLength, rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		defer clear(p)
		generated = vngcloud.Secret(string(p))
		password = generated
		opts.Redact = []string{password.Reveal()}
	}
	if opts.containsCredential(in.UserID) {
		return nil, fmt.Errorf("iam: %s: unsafe user identity", op)
	}
	var sent []string
	defer func() { clear(sent) }()
	notSent := false
	req := opts.request(transport.Request{Operation: op, Method: http.MethodPost,
		URL:  c.accountsURL([]string{"iam-users", in.UserID, "reset-password"}, nil),
		Body: resetUserPasswordBody{Password: password.Reveal()}, OK: []int{204}, Once: true,
		NotSent: &notSent, KeepObservedStatus: true, MaxBody: userWriteMaxBody}, &sent)
	status, err := c.c.DoJSONStatus(ctx, req, nil)
	opts.collect(sent)
	if notSent || err != nil && status >= 400 && status < 500 {
		return nil, err
	}
	out := &ResetUserPasswordOutput{Password: generated}
	if status == 204 {
		if err != nil {
			return out, fmt.Errorf("%s: %w: %w", op, ErrNotSettled, err)
		}
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("%s: %w: %w", op, ErrUserWriteUncertain, err)
	}
	return out, fmt.Errorf("%s: %w", op, ErrUserWriteUncertain)
}

func (c *Client) guardUserAdministration(ctx context.Context, op, id string, opts userReadOptions) error {
	_, err := c.guardUserAdministrationTarget(ctx, op, id, opts)
	return err
}

func (c *Client) guardUserAdministrationTarget(ctx context.Context, op, id string, opts userReadOptions) (*UserDetail, error) {
	user, err := c.guardUserWriteTarget(ctx, op, id, opts)
	if err != nil {
		return nil, err
	}
	caller, err := c.guardCallerIdentity(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: read caller identity: %w", op, err)
	}
	actions, err := c.guardWriteActionNames(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: read write actions: %w", op, err)
	}
	reason, err := c.userIsProtected(ctx, op, id, caller, actions, map[string]*Policy{}, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: read target protection: %w", op, err)
	}
	switch reason {
	case protectedBySelf:
		return nil, fmt.Errorf("%s: %w", op, ErrSelfChange)
	case protectedByPrivilege:
		return nil, fmt.Errorf("%s: %w", op, ErrPrivilegedChange)
	}
	return user, nil
}

// Unexpected success and redirect statuses also require a non-empty code,
// otherwise the transport retains the peer's arbitrary error code.
func userAdministrationErrorCode(status int, _ string) string {
	if code := core.ResolvedCode(status, ""); code != "" {
		return code
	}
	return "RequestFailed"
}
