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
	// Name is required.
	Name string `vngcloud:"required"`
	// Description is sent as description in the create body.
	Description string
}

// CreateServiceAccountOutput holds ClientSecret as a vngcloud.Secret, never a
// plain string: printing, logging, or JSON-encoding the Output gives
// "[redacted]" for this field, and Reveal is the only way to read the value
// back out. ClientSecret is empty when the create response holds none, in
// which case CreateServiceAccount also returns ErrNoSecret alongside this
// Output rather than nil.
type CreateServiceAccountOutput struct {
	ServiceAccount ServiceAccount
	ClientSecret   vngcloud.Secret
	cleanup        *serviceAccountCleanup
}

type createServiceAccountBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type createServiceAccountResponse struct {
	ID           string `json:"id"`
	ClientSecret string `json:"clientSecret"`
}

// CreateServiceAccount sends once and retains every issued ClientSecret in
// partial output. ErrCreateUnconfirmed means confirmation or identity failed;
// an empty ServiceAccount.ID grants no cleanup authority. Persist the secret
// before handling the error. Dependent reads withhold credential reflections.
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
		Once:      true,
	}
	var sent []string
	defer func() { clear(sent) }()
	req.SentCredentials = &sent
	req.WithholdMessage = userWriteWithheld
	req.ClassifyError = userWriteErrorCode
	req.StrictJSON = true
	req.MaxBody = guardObjectMaxBody
	if _, err := c.c.DoJSONStatus(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousServiceAccountCreateErr(op, in.Name, err)
	}
	secret := vngcloud.Secret(resp.ClientSecret)
	opts := guardUserReadOptions(userReadOptions{Redact: []string{resp.ClientSecret}, SentCredentials: &sent})
	out := &CreateServiceAccountOutput{ClientSecret: secret}
	defer func() { out.cleanup = newServiceAccountCleanup(out.ServiceAccount.ID, opts) }()
	if resp.ID == "" || core.CheckPathID(op, "ServiceAccountID", resp.ID) != nil || opts.containsCredential(resp.ID) {
		err := fmt.Errorf("%s: %w: create identity is unproven; retain the secret and reconcile with list-service-accounts before any cleanup", op, ErrCreateUnconfirmed)
		if secret == "" {
			err = fmt.Errorf("%w: %w", err, ErrNoSecret)
		}
		return out, err
	}
	got, err := c.getServiceAccount(ctx, &GetServiceAccountInput{ServiceAccountID: resp.ID}, opts)
	if opts.containsCredential(resp.ID) {
		return out, fmt.Errorf("%s: %w: create identity is unsafe; reconcile ownership before cleanup", op, ErrCreateUnconfirmed)
	}
	if err != nil {
		out.ServiceAccount = ServiceAccount{ID: resp.ID}
		return out, fmt.Errorf("%s: service account %s was created but the read to confirm it failed: %w: %w", op, resp.ID, ErrCreateUnconfirmed, err)
	}
	out.ServiceAccount = got.ServiceAccount
	if secret == "" {
		return out, fmt.Errorf("%s: %w: create response had no client secret; run reset-service-account-secret to get one", op, ErrNoSecret)
	}
	return out, nil
}

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
	// ServiceAccountID is required; use ASCII letters, digits, or hyphens.
	ServiceAccountID string `vngcloud:"required"`
	// Description is sent only when non-nil.
	Description *string
	// AccessTokenLifeSpan is sent only when non-nil.
	AccessTokenLifeSpan *int
}

// UpdateServiceAccountOutput holds the result of UpdateServiceAccount.
type UpdateServiceAccountOutput struct {
	ServiceAccount ServiceAccount
}

type updateServiceAccountBody struct {
	Description         *string `json:"description,omitempty"`
	AccessTokenLifeSpan *int    `json:"accessTokenLifeSpan,omitempty"`
}

// UpdateServiceAccount checks protection, updates the supplied fields, and
// confirms the requested identity. An accepted update whose response handling
// or confirmation fails retains a safe ID with ErrNotSettled.
func (c *Client) UpdateServiceAccount(ctx context.Context, in *UpdateServiceAccountInput) (*UpdateServiceAccountOutput, error) {
	const op = "iam.UpdateServiceAccount"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	var credentials []string
	defer func() { clear(credentials) }()
	opts := guardUserReadOptions(userReadOptions{SentCredentials: &credentials})
	if err := c.guardServiceAccountWrite(ctx, op, in.ServiceAccountID, opts); err != nil {
		return nil, err
	}

	if err := guardRequestOperands(op, []string{in.ServiceAccountID}, opts); err != nil {
		return nil, err
	}
	var sent []string
	defer func() { clear(sent) }()

	notSent := false
	req := transport.Request{
		NotSent:            &notSent,
		KeepObservedStatus: true,
		Operation:          op,
		Method:             http.MethodPatch,
		URL:                c.accountsURL([]string{"service-accounts", in.ServiceAccountID}, nil),
		Body:               updateServiceAccountBody{Description: in.Description, AccessTokenLifeSpan: in.AccessTokenLifeSpan},
		OK:                 []int{200},
		Idempotent:         true,
	}
	status, err := c.c.DoJSONStatus(ctx, opts.request(req, &sent), nil)
	opts.collect(sent)
	if err != nil {
		if !notSent && status == http.StatusOK {
			return &UpdateServiceAccountOutput{ServiceAccount: serviceAccountFallback(in.ServiceAccountID, opts)}, fmt.Errorf("%s: %w: accepted update response failed: %w", op, ErrNotSettled, err)
		}
		return nil, err
	}

	got, err := c.getServiceAccount(ctx, &GetServiceAccountInput{ServiceAccountID: in.ServiceAccountID}, opts)
	if err != nil {
		return &UpdateServiceAccountOutput{ServiceAccount: serviceAccountFallback(in.ServiceAccountID, opts)},
			fmt.Errorf("%s: %w: service account update was accepted but confirmation failed: %w", op, ErrNotSettled, err)
	}
	return &UpdateServiceAccountOutput{ServiceAccount: got.ServiceAccount}, nil
}

// DeleteServiceAccountInput identifies the service account to delete. It
// must not be the caller and must not hold a privileged policy; see
// guard.go.
type DeleteServiceAccountInput struct {
	// ServiceAccountID is required; use ASCII letters, digits, or hyphens.
	ServiceAccountID string `vngcloud:"required"`
	cleanup          *serviceAccountCleanup
}

// DeleteServiceAccountOutput carries no result fields.
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
	var credentials []string
	if in.cleanup != nil {
		credentials = append([]string(nil), in.cleanup.credentials...)
	}
	defer func() { clear(credentials) }()
	opts := guardUserReadOptions(userReadOptions{SentCredentials: &credentials})
	if err := guardRequestOperands(op, []string{in.ServiceAccountID}, opts); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	if err := c.guardServiceAccountWrite(ctx, op, in.ServiceAccountID, opts); err != nil {
		return nil, err
	}

	if err := guardRequestOperands(op, []string{in.ServiceAccountID}, opts); err != nil {
		return nil, err
	}
	var sent []string
	defer func() { clear(sent) }()

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.accountsURL([]string{"service-accounts", in.ServiceAccountID}, nil),
		OK:        []int{204},
	}
	err := c.c.DoJSON(ctx, opts.request(req, &sent), nil)
	opts.collect(sent)
	if err != nil {
		return nil, err
	}
	return &DeleteServiceAccountOutput{}, nil
}

// ResetServiceAccountSecretInput identifies the service account whose
// client secret to reset. It must not be the caller and must not hold a
// privileged policy; see guard.go.
type ResetServiceAccountSecretInput struct {
	// ServiceAccountID is required; use ASCII letters, digits, or hyphens.
	ServiceAccountID string `vngcloud:"required"`
}

// ResetServiceAccountSecretOutput holds ClientSecret as a vngcloud.Secret; see
// CreateServiceAccountOutput's doc comment. ClientSecret is empty only when
// ResetServiceAccountSecret also returns ErrNoSecret: the reset itself still
// succeeded.
type ResetServiceAccountSecretOutput struct {
	ClientSecret vngcloud.Secret
}

type resetServiceAccountSecretResponse struct {
	ClientSecret string `json:"clientSecret"`
}

// ResetServiceAccountSecret replaces a service account's client secret and
// returns the new value. The old secret stops working immediately; there is
// no way to recover it. If the response reports success but carries no
// client secret, ResetServiceAccountSecret returns ErrNoSecret: the reset
// most likely already rotated the secret without returning it, so the old
// secret must be treated as revoked, and the caller should reset again to
// get a usable value.
//
// The guard runs first and sends nothing when it refuses: ErrSelfChange if
// ServiceAccountID is the caller, or ErrPrivilegedChange if it holds a
// privileged policy.
//
// The request sets transport.Request.Sensitive, so the response never
// reaches the configured response-capture hook, and a decode failure never
// quotes the response body either.
//
// It sets transport.Request.Once: a resend after a 401 or a followed
// redirect would rotate the secret a second time, discarding the first
// rotation's value before the caller ever saw it, so the request is sent at
// most once. After any error that is not a 4xx *core.APIError, whether the
// reset reached the server is unknown, and there is no read that shows
// whether a secret changed, so the caller should treat the previous secret
// as no longer trustworthy and reset again if it still works.
func (c *Client) ResetServiceAccountSecret(ctx context.Context, in *ResetServiceAccountSecretInput) (*ResetServiceAccountSecretOutput, error) {
	const op = "iam.ResetServiceAccountSecret"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	var credentials []string
	defer func() { clear(credentials) }()
	opts := guardUserReadOptions(userReadOptions{SentCredentials: &credentials})
	if err := c.guardServiceAccountWrite(ctx, op, in.ServiceAccountID, opts); err != nil {
		return nil, err
	}

	if err := guardRequestOperands(op, []string{in.ServiceAccountID}, opts); err != nil {
		return nil, err
	}
	var sent []string
	defer func() { clear(sent) }()

	var resp resetServiceAccountSecretResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.accountsURL([]string{"service-accounts", in.ServiceAccountID, "reset-secret"}, nil),
		OK:        []int{200},
		Sensitive: true,
		Once:      true,
	}
	_, err := c.c.DoJSONStatus(ctx, opts.request(req, &sent), &resp)
	opts.collect(sent)
	if err != nil {
		return nil, wrapAmbiguousResetSecretErr(op, err)
	}
	if resp.ClientSecret == "" {
		return &ResetServiceAccountSecretOutput{}, fmt.Errorf("%s: %w: the secret was probably rotated by this call but not returned; reset again to get a usable value", op, ErrNoSecret)
	}
	return &ResetServiceAccountSecretOutput{ClientSecret: vngcloud.Secret(resp.ClientSecret)}, nil
}

func wrapAmbiguousResetSecretErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: reset may have reached the server; no read shows whether the secret changed, so treat the previous secret as revoked: %w", op, err)
}

func serviceAccountFallback(id string, opts userReadOptions) ServiceAccount {
	if opts.containsCredential(id) {
		return ServiceAccount{}
	}
	return ServiceAccount{ID: id}
}
