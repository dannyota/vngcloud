package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type EnableUserMFAInput struct {
	UserID string `vngcloud:"required"`
	Method string `vngcloud:"required"`
}

type DisableUserMFAInput struct {
	UserID string `vngcloud:"required"`
	Method string `vngcloud:"required"`
}

// EnableUserMFAOutput reports observed acceptance and newly confirmed state.
// Changed means a mutation returned 204, even when confirmation fails.
type EnableUserMFAOutput struct {
	Method  string
	Enabled *bool
	Changed bool
}

// DisableUserMFAOutput reports observed acceptance and newly confirmed state.
// Enabled is nil when no trustworthy post-operation state is available.
type DisableUserMFAOutput struct {
	Method  string
	Enabled *bool
	Changed bool
}

// EnableUserMFA activates an existing verified Google factor. It sends at most
// once and confirms once; it never enrolls a factor or verifies user possession.
func (c *Client) EnableUserMFA(ctx context.Context, in *EnableUserMFAInput) (*EnableUserMFAOutput, error) {
	const op = "iam.EnableUserMFA"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	out, err := c.changeUserMFA(ctx, op, in.UserID, in.Method, true)
	if out == nil {
		return nil, err
	}
	return &EnableUserMFAOutput{Method: out.Method, Enabled: out.Enabled, Changed: out.Changed}, err
}

// DisableUserMFA deactivates an existing Google factor after all target guards.
// A deliberate repeat reads state again; no automatic replay or rollback runs.
func (c *Client) DisableUserMFA(ctx context.Context, in *DisableUserMFAInput) (*DisableUserMFAOutput, error) {
	const op = "iam.DisableUserMFA"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	return c.changeUserMFA(ctx, op, in.UserID, in.Method, false)
}

func (c *Client) changeUserMFA(ctx context.Context, op, id, method string, enable bool) (*DisableUserMFAOutput, error) {
	if err := core.CheckPathID(op, "UserID", id); err != nil {
		return nil, err
	}
	if method != "google" {
		return nil, fmt.Errorf("%w: %s: Method must be google", core.ErrInvalidInput, op)
	}
	var credentials []string
	defer func() { clear(credentials) }()
	opts := userReadOptions{WithholdMessage: userWriteWithheld, ClassifyError: userAdministrationErrorCode, SentCredentials: &credentials}
	user, err := c.guardUserAdministrationTarget(ctx, op, id, opts)
	if err != nil {
		return nil, err
	}
	if opts.containsCredential(id) || opts.containsResponseCredential(user) {
		return nil, userMFAUnavailable(op)
	}
	factor, err := userMFAState(op, id, user)
	if err != nil {
		return nil, err
	}
	if enable && !*factor.Verified {
		return nil, userMFAUnavailable(op)
	}
	if *factor.Enabled == enable {
		return &DisableUserMFAOutput{Method: method, Enabled: factor.Enabled}, nil
	}
	suffix := "deactivate-google"
	if enable {
		suffix = "activate-google"
	}
	var sent []string
	defer func() { clear(sent) }()
	notSent := false
	req := opts.request(transport.Request{Operation: op, Method: http.MethodPost,
		URL: c.accountsURL([]string{"iam-users", id, suffix}, nil), OK: []int{204},
		Once: true, NotSent: &notSent, KeepObservedStatus: true, MaxBody: userWriteMaxBody}, &sent)
	status, sendErr := c.c.DoJSONStatus(ctx, req, nil)
	opts.collect(sent)
	if notSent || status >= 400 && status < 500 {
		return nil, sendErr
	}
	if status != 204 {
		if sendErr != nil {
			return nil, fmt.Errorf("%s: %w: %w", op, ErrUserWriteUncertain, sendErr)
		}
		return nil, fmt.Errorf("%s: %w", op, ErrUserWriteUncertain)
	}
	out := &DisableUserMFAOutput{Method: method, Changed: true}
	if opts.containsCredential(id) || opts.containsResponseCredential(user) {
		return out, fmt.Errorf("%s: %w: %w", op, ErrNotSettled, errors.Join(sendErr, userMFAUnavailable(op)))
	}
	confirmed, confirmErr := c.getUser(ctx, op, id, opts)
	if confirmErr == nil {
		var state *MFAFactor
		state, confirmErr = userMFAState(op, id, confirmed)
		if confirmErr == nil {
			out.Enabled = state.Enabled
			if *state.Enabled != enable || enable && !*state.Verified {
				confirmErr = userMFAUnavailable(op)
			}
		}
	}
	if sendErr != nil || confirmErr != nil {
		return out, fmt.Errorf("%s: %w: %w", op, ErrNotSettled, errors.Join(sendErr, confirmErr))
	}
	return out, nil
}

func userMFAState(op, id string, user *UserDetail) (*MFAFactor, error) {
	if user == nil || core.CheckPathID(op, "UserID", user.ID) != nil || !strings.EqualFold(user.ID, id) || user.From != "iam" || user.MFA == nil {
		return nil, userMFAUnavailable(op)
	}
	factor := user.MFA.GoogleAuthenticator
	if factor == nil || factor.Verified == nil || factor.Enabled == nil {
		return nil, userMFAUnavailable(op)
	}
	return factor, nil
}

func userMFAUnavailable(op string) error {
	return &core.APIError{Operation: op, Code: "RequestFailed", Message: "MFA state is unavailable or unverified"}
}
