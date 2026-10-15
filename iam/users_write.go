package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const userWriteMaxBody = 64 << 10
const userWriteWithheld = "IAM user request failed; response withheld"

type CreateUserInput struct {
	Username       string `vngcloud:"required"`
	Password       vngcloud.Secret
	PasswordLength int
}

// CreateUserOutput retains only generated passwords. CreatedID comes from the
// create response; CandidateID proves current state only, never ownership.
type CreateUserOutput struct {
	User        UserDetail
	Password    vngcloud.Secret
	CreatedID   string
	CandidateID string
}

type createUserBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type createUserResponse struct {
	ID json.RawMessage `json:"id"`
}

func userWriteErrorCode(status int, _ string) string { return core.ResolvedCode(status, "") }

// CreateUser sends once and confirms by reading. A non-nil partial output
// retains a generated password on uncertain or accepted-but-unconfirmed
// outcomes. Establish ownership before trusting a name-only candidate.
func (c *Client) CreateUser(ctx context.Context, in *CreateUserInput) (*CreateUserOutput, error) {
	const op = "iam.CreateUser"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkUserName(op, "Username", in.Username); err != nil {
		return nil, err
	}
	generated, err := createPassword(ctx, in)
	if err != nil {
		return nil, err
	}
	password := in.Password
	if password == "" {
		password = generated
	}
	var credentials []string
	defer func() { clear(credentials) }()
	opts := userReadOptions{Redact: []string{password.Reveal()}, WithholdMessage: userWriteWithheld, ClassifyError: userWriteErrorCode, SentCredentials: &credentials}
	var resp *createUserResponse
	var sent []string
	defer func() { clear(sent) }()
	notSent, empty := false, false
	req := opts.request(transport.Request{
		Operation: op, Method: http.MethodPost, URL: c.accountsURL([]string{"iam-users"}, nil),
		Body: createUserBody{Username: in.Username, Password: password.Reveal()}, OK: []int{201},
		Once: true, NotSent: &notSent, StrictJSON: true, AllowEmptyBody: true, BodyWasEmpty: &empty,
		KeepObservedStatus: true, MaxBody: userWriteMaxBody,
	}, &sent)
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	opts.collect(sent)
	if err != nil && (notSent || errors.Is(err, core.ErrInvalidConfig) || status >= 400 && status < 500) {
		return nil, err
	}
	out := &CreateUserOutput{User: UserDetail{Username: transport.RedactValues(in.Username, append(opts.Redact, credentials...)...)}, Password: generated}
	defer func() {
		out.User.Username = transport.RedactValues(out.User.Username, append(opts.Redact, credentials...)...)
		if opts.containsCredential(out.CreatedID) {
			out.CreatedID = ""
		}
		if opts.containsCredential(out.CandidateID) {
			out.CandidateID = ""
		}
		if opts.containsCredential(out.User.ID, out.User.From) {
			out.User = UserDetail{Username: out.User.Username}
		}
	}()
	if err != nil || status != 201 {
		return out, userCreateUncertain(err)
	}
	if !empty && resp == nil {
		return out, userCreateUncertain(nil)
	}
	var id string
	if resp != nil && len(resp.ID) > 0 {
		if err := json.Unmarshal(resp.ID, &id); err != nil || string(resp.ID) == "null" {
			return out, userCreateUncertain(nil)
		}
	}
	if id != "" {
		if opts.containsCredential(id) || core.CheckPathID(op, "UserID", id) != nil {
			return out, userCreateUncertain(nil)
		}
		out.CreatedID = id
		out.User.ID = id
		user, err := c.confirmUser(ctx, id, in.Username, opts)
		if err != nil {
			return out, fmt.Errorf("%s: %w: %w", op, ErrCreateUnconfirmed, err)
		}
		out.User = *user
		return out, nil
	}
	// Only decoded objects without an ID or the transport's empty-body evidence
	// reach this lookup. Decode failures already returned above.
	if empty || resp != nil {
		user, err := c.lookupUser(ctx, in.Username, opts)
		if err == nil && user != nil {
			out.CandidateID = user.ID
		}
		return out, userCreateUncertain(err)
	}
	return out, userCreateUncertain(nil)
}

func userCreateUncertain(cause error) error {
	const message = "iam.CreateUser: password ownership is unproven; establish ownership with other administrators before trusting the password, resetting or deleting a candidate"
	if cause != nil {
		return fmt.Errorf("%s: %w: %w", message, ErrUserCreateUncertain, cause)
	}
	return fmt.Errorf("%s: %w", message, ErrUserCreateUncertain)
}

type DeleteUserInput struct {
	UserID string `vngcloud:"required"`
}
type DeleteUserOutput struct{}

// DeleteUser refuses self and non-IAM targets before deleting. It retains
// idempotent transport retries and returns 404 and 409 as errors.
func (c *Client) DeleteUser(ctx context.Context, in *DeleteUserInput) (*DeleteUserOutput, error) {
	const op = "iam.DeleteUser"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}
	if err := c.guardUserWrite(ctx, op, in.UserID); err != nil {
		return nil, err
	}
	req := transport.Request{Operation: op, Method: http.MethodDelete, URL: c.accountsURL([]string{"iam-users", in.UserID}, nil), OK: []int{204}, MaxBody: userWriteMaxBody, NoRedirect: true}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteUserOutput{}, nil
}
