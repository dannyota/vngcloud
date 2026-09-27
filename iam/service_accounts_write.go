package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreateServiceAccountInput names the new service account. It has no guard:
// creating a service account changes no one's rights.
type CreateServiceAccountInput struct {
	Name        string `vngcloud:"required"`
	Description string
}

// CreateServiceAccountOutput's ClientSecret is a vngcloud.Secret, never a
// plain string: printing, logging, or JSON-encoding the Output gives
// "[redacted]" for this field, and Reveal is the only way to read the value
// back out. ClientSecret is empty when the create response holds none.
type CreateServiceAccountOutput struct {
	ServiceAccount ServiceAccount
	ClientSecret   vngcloud.Secret
}

type createServiceAccountBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// createServiceAccountResponse decodes only the two fields CreateServiceAccount
// needs from the create response, whose full shape the design leaves
// undocumented: the new ID, to read the created service account back, and
// the client secret, shown only this once.
type createServiceAccountResponse struct {
	ID           string `json:"id"`
	ClientSecret string `json:"clientSecret"`
}

// CreateServiceAccount creates a service account and returns its client
// secret alongside it, read back with GetServiceAccount after the create.
// If that read fails, the returned error says the service account was
// created and names its ID.
//
// The request sets transport.Request.Sensitive, so the response never
// reaches the configured response-capture hook, and a decode failure never
// quotes the response body either.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// service account may exist, and the caller lists service accounts by Name
// before creating it again, rather than retrying blind; a service account
// found that way has already lost its client secret and should have its
// secret reset.
func (c *Client) CreateServiceAccount(ctx context.Context, in *CreateServiceAccountInput) (*CreateServiceAccountOutput, error) {
	const op = "iam.CreateServiceAccount"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var resp createServiceAccountResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.accountsURL([]string{"service-accounts"}, nil),
		Body:      createServiceAccountBody{Name: in.Name, Description: in.Description},
		OK:        []int{201},
		Sensitive: true,
	}
	if _, err := c.c.DoJSONStatus(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousServiceAccountCreateErr(op, in.Name, err)
	}
	if resp.ID == "" {
		return nil, &core.APIError{Operation: op, Message: "create response had no id; a service account may exist, check with list-service-accounts --name"}
	}

	got, err := c.GetServiceAccount(ctx, &GetServiceAccountInput{ServiceAccountID: resp.ID})
	if err != nil {
		return nil, fmt.Errorf("%s: service account %s was created but the read to confirm it failed: %w", op, resp.ID, err)
	}
	return &CreateServiceAccountOutput{
		ServiceAccount: got.ServiceAccount,
		ClientSecret:   vngcloud.Secret(resp.ClientSecret),
	}, nil
}

// wrapAmbiguousServiceAccountCreateErr wraps err from CreateServiceAccount
// when it failed ambiguously: a 5xx or a network error, where whether the
// request reached the server is unknown. It is never called for a 4xx
// *core.APIError, which means the request was rejected outright and nothing
// was created. name is the exact value the call sent. A nil err stays nil.
func wrapAmbiguousServiceAccountCreateErr(op, name string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; check with list-service-accounts --name %q, then match exactly; a service account found that way has lost its client secret and should have its secret reset: %w", op, name, err)
}

// UpdateServiceAccountInput sends only its non-nil fields. ServiceAccountID
// must not be the caller and must not hold a privileged policy; see
// guard.go.
type UpdateServiceAccountInput struct {
	ServiceAccountID    string `vngcloud:"required"`
	Description         *string
	AccessTokenLifeSpan *int
}

type UpdateServiceAccountOutput struct {
	ServiceAccount ServiceAccount
}

type updateServiceAccountBody struct {
	Description         *string `json:"description,omitempty"`
	AccessTokenLifeSpan *int    `json:"accessTokenLifeSpan,omitempty"`
}

// UpdateServiceAccount changes Description or AccessTokenLifeSpan, sending
// only the fields the caller set, and reads the account back afterward to
// build the Output.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// ServiceAccountID is the caller, or ErrPrivilegedChange if it holds a
// privileged policy.
//
// The request sets Idempotent: it sends the update's full intended state,
// so the transport's own retry after a 5xx is safe to repeat.
func (c *Client) UpdateServiceAccount(ctx context.Context, in *UpdateServiceAccountInput) (*UpdateServiceAccountOutput, error) {
	const op = "iam.UpdateServiceAccount"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	if err := c.guardServiceAccountWrite(ctx, op, in.ServiceAccountID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPatch,
		URL:        c.accountsURL([]string{"service-accounts", in.ServiceAccountID}, nil),
		Body:       updateServiceAccountBody{Description: in.Description, AccessTokenLifeSpan: in.AccessTokenLifeSpan},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	got, err := c.GetServiceAccount(ctx, &GetServiceAccountInput{ServiceAccountID: in.ServiceAccountID})
	if err != nil {
		return nil, fmt.Errorf("%s: service account %s was updated but the read to confirm it failed: %w", op, in.ServiceAccountID, err)
	}
	return &UpdateServiceAccountOutput{ServiceAccount: got.ServiceAccount}, nil
}

// DeleteServiceAccountInput identifies the service account to delete. It
// must not be the caller and must not hold a privileged policy; see
// guard.go.
type DeleteServiceAccountInput struct {
	ServiceAccountID string `vngcloud:"required"`
}

type DeleteServiceAccountOutput struct{}

// DeleteServiceAccount deletes a service account. The guard runs first and
// sends nothing when it refuses: ErrSelfChange if ServiceAccountID is the
// caller, or ErrPrivilegedChange if it holds a privileged policy. DELETE is
// idempotent and keeps the transport's own retries.
func (c *Client) DeleteServiceAccount(ctx context.Context, in *DeleteServiceAccountInput) (*DeleteServiceAccountOutput, error) {
	const op = "iam.DeleteServiceAccount"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	if err := c.guardServiceAccountWrite(ctx, op, in.ServiceAccountID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.accountsURL([]string{"service-accounts", in.ServiceAccountID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteServiceAccountOutput{}, nil
}

// ResetServiceAccountSecretInput identifies the service account whose
// client secret to reset. It must not be the caller and must not hold a
// privileged policy; see guard.go.
type ResetServiceAccountSecretInput struct {
	ServiceAccountID string `vngcloud:"required"`
}

// ResetServiceAccountSecretOutput's ClientSecret is a vngcloud.Secret; see
// CreateServiceAccountOutput's doc comment.
type ResetServiceAccountSecretOutput struct {
	ClientSecret vngcloud.Secret
}

type resetServiceAccountSecretResponse struct {
	ClientSecret string `json:"clientSecret"`
}

// ResetServiceAccountSecret replaces a service account's client secret and
// returns the new value. The old secret stops working immediately; there is
// no way to recover it.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// ServiceAccountID is the caller, or ErrPrivilegedChange if it holds a
// privileged policy.
//
// The request sets transport.Request.Sensitive, so the response never
// reaches the configured response-capture hook, and a decode failure never
// quotes the response body either.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError,
// whether the reset reached the server is unknown, and there is no read
// that shows whether a secret changed, so the caller should treat the
// previous secret as no longer trustworthy and reset again if it still
// works.
func (c *Client) ResetServiceAccountSecret(ctx context.Context, in *ResetServiceAccountSecretInput) (*ResetServiceAccountSecretOutput, error) {
	const op = "iam.ResetServiceAccountSecret"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	if err := c.guardServiceAccountWrite(ctx, op, in.ServiceAccountID); err != nil {
		return nil, err
	}

	var resp resetServiceAccountSecretResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.accountsURL([]string{"service-accounts", in.ServiceAccountID, "reset-secret"}, nil),
		OK:        []int{200},
		Sensitive: true,
	}
	if _, err := c.c.DoJSONStatus(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousResetSecretErr(op, in.ServiceAccountID, err)
	}
	return &ResetServiceAccountSecretOutput{ClientSecret: vngcloud.Secret(resp.ClientSecret)}, nil
}

// wrapAmbiguousResetSecretErr wraps err from ResetServiceAccountSecret when
// it failed ambiguously: a 5xx or a network error, where whether the reset
// reached the server is unknown. It is never called for a 4xx
// *core.APIError, which means the request was rejected outright and the
// secret was not changed. A nil err stays nil.
func wrapAmbiguousResetSecretErr(op, serviceAccountID string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: reset may have already reached the server for service account %s; there is no read that shows whether the secret changed, so treat the previous secret as revoked: %w", op, serviceAccountID, err)
}
