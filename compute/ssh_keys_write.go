package compute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// sshKeyPrivateKeyMarker is the text every PEM private key header contains
// ("-----BEGIN ... PRIVATE KEY-----"). ImportSSHKey refuses a PublicKey
// holding it, so a caller who passes a private key file by mistake never
// sends it.
const sshKeyPrivateKeyMarker = "PRIVATE KEY"

// sshKeyWriteResponse is the "data" envelope ImportSSHKey and CreateSSHKey
// both decode into: an id, name, public key, status, and created timestamp
// shared by every SSH key write, plus a private key ImportSSHKey never
// receives and CreateSSHKey always does. Neither call decodes straight into
// SSHKey, so a missing id or private key is caught here before either value
// reaches a caller, and CreateSSHKey's private key never touches a field
// SSHKey exposes.
type sshKeyWriteResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PubKey     string `json:"pubKey"`
	PrivateKey string `json:"privateKey"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
}

func (r sshKeyWriteResponse) toSSHKey() SSHKey {
	return SSHKey{
		ID:        r.ID,
		Name:      r.Name,
		PublicKey: r.PubKey,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
	}
}

// wrapAmbiguousSSHKeyErr wraps err from an SSH key create or import POST
// that failed ambiguously: a 5xx or a network error, where whether the key
// reached the server is unknown. It is never called for a 4xx
// *core.APIError, which means the request was rejected outright and nothing
// was created. name is the exact value the call sent, so the hint tells the
// caller how to check what happened instead of retrying blind; a created
// key found that way has already lost its private key and should be
// deleted, which hasSecret adds to the hint only for that call. A nil err
// stays nil.
func wrapAmbiguousSSHKeyErr(op, name string, hasSecret bool, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	hint := fmt.Sprintf("check with list-ssh-keys --name %q, then match exactly", name)
	if hasSecret {
		hint += "; a key found that way has lost its private key and should be deleted"
	}
	return fmt.Errorf("%s: create may have already reached the server; %s: %w", op, hint, err)
}

type importSSHKeyBody struct {
	Name   string `json:"name"`
	PubKey string `json:"pubKey"`
}

// ImportSSHKeyInput imports an existing public key under Name. PublicKey
// must be one line after trimming leading and trailing whitespace, and must
// not contain the text "PRIVATE KEY"; both checks run before any request,
// and neither error ever quotes PublicKey's value. The server accepts RSA
// public keys ("ssh-rsa") only; ed25519 ("ssh-ed25519") and ECDSA
// ("ecdsa-sha2-nistp256") keys are refused with 400 "Invalid public key".
// Key type and size are otherwise not checked here; the server decides.
type ImportSSHKeyInput struct {
	Name      string `vngcloud:"required"`
	PublicKey string `vngcloud:"required"`
}

type ImportSSHKeyOutput struct {
	SSHKey SSHKey
}

// ImportSSHKey imports a public key made elsewhere, such as by ssh-keygen,
// so the matching private key never reaches GreenNode; CreateSSHKey, in
// contrast, has GreenNode generate and see the private key.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// key may exist, and the caller lists keys by Name before importing it
// again, rather than retrying blind.
func (c *Client) ImportSSHKey(ctx context.Context, in *ImportSSHKeyInput) (*ImportSSHKeyOutput, error) {
	const op = "compute.ImportSSHKey"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	publicKey := strings.TrimSpace(in.PublicKey)
	if strings.ContainsAny(publicKey, "\n\r") {
		return nil, fmt.Errorf("%w: %s requires PublicKey to be one line", core.ErrInvalidInput, op)
	}
	if strings.Contains(publicKey, sshKeyPrivateKeyMarker) {
		return nil, fmt.Errorf("%w: %s: PublicKey looks like a private key, not a public one", core.ErrInvalidInput, op)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data sshKeyWriteResponse `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.computeURL("v2", []string{projectID, "sshKeys", "import"}, nil),
		Body:      importSSHKeyBody{Name: in.Name, PubKey: publicKey},
		OK:        []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousSSHKeyErr(op, in.Name, false, err)
	}
	if resp.Data.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "import response had no id"}
	}
	return &ImportSSHKeyOutput{SSHKey: resp.Data.toSSHKey()}, nil
}

type createSSHKeyBody struct {
	Name string `json:"name"`
}

type CreateSSHKeyInput struct {
	Name string `vngcloud:"required"`
}

// CreateSSHKeyOutput's PrivateKey is a vngcloud.Secret, never a plain
// string: printing, logging, or JSON-encoding the Output gives "[redacted]"
// for this field, and Reveal is the only way to read the value back out.
// SSHKey itself never carries a private key.
type CreateSSHKeyOutput struct {
	SSHKey     SSHKey
	PrivateKey vngcloud.Secret
}

// CreateSSHKey has GreenNode generate a key pair and return the private
// key once; ImportSSHKey is the alternative that keeps a private key made
// elsewhere off GreenNode entirely, and is the one the wiki recommends. The
// returned private key is an OpenSSH private key, PEM-encoded starting
// "-----BEGIN OPENSSH PRIVATE KEY-----", not a PKCS#1 or PKCS#8 key.
//
// The request sets transport.Request.Sensitive, so the response never
// reaches the configured response-capture hook, and a decode failure never
// quotes the response body either.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// key may exist, and the caller lists keys by Name before creating it
// again, rather than retrying blind; a key found that way has already lost
// its private key and should be deleted. A 201 response missing an id is
// itself an *core.APIError with the same list-and-match hint, since it
// leaves the same question open; a 201 with an id but no private key names
// that id in the error instead, so the caller deletes it directly rather
// than searching for it.
func (c *Client) CreateSSHKey(ctx context.Context, in *CreateSSHKeyInput) (*CreateSSHKeyOutput, error) {
	const op = "compute.CreateSSHKey"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data sshKeyWriteResponse `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.computeURL("v2", []string{projectID, "sshKeys"}, nil),
		Body:      createSSHKeyBody{Name: in.Name},
		OK:        []int{201},
		Sensitive: true,
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousSSHKeyErr(op, in.Name, true, err)
	}
	if resp.Data.ID == "" || resp.Data.PrivateKey == "" {
		msg := "create response had no id or no private key; a key may exist, check with list-ssh-keys"
		if resp.Data.ID != "" {
			msg = fmt.Sprintf("create response for ssh key %s had no private key; its private key is lost, delete that key directly", resp.Data.ID)
		}
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: status,
			Message:    msg,
		}
	}
	return &CreateSSHKeyOutput{
		SSHKey:     resp.Data.toSSHKey(),
		PrivateKey: vngcloud.Secret(resp.Data.PrivateKey),
	}, nil
}

// sshKeyDeleteNotFoundPhrase is the lowercased text GreenNode's delete
// endpoint sends in a 400 body for a key that no longer exists ("Cannot get
// ssh key with id <id>"), unlike every other unknown-id response in this
// package, which is a 404. mapSSHKeyDeleteNotFound requires this phrase
// together with the key's own ID, so an unrelated 400 (a different reason,
// or a different key ID from a stale retry) is not misreported as this
// key's not-found.
const sshKeyDeleteNotFoundPhrase = "cannot get ssh key with id"

// mapSSHKeyDeleteNotFound rewraps a 400 APIError from DeleteSSHKey, whose
// message contains both sshKeyDeleteNotFoundPhrase and sshKeyID, into the
// SDK's ordinary not-found sentinel, the same one GetSSHKey returns for an
// unknown id. The original *core.APIError stays in the returned error's
// chain, so errors.As against it still works alongside errors.Is against
// core.ErrNotFound. Any other error, including a 400 for a different
// reason or naming a different key, passes through unchanged.
func mapSSHKeyDeleteNotFound(op, sshKeyID string, err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		lower := strings.ToLower(apiErr.Message)
		if strings.Contains(lower, sshKeyDeleteNotFoundPhrase) && strings.Contains(lower, strings.ToLower(sshKeyID)) {
			return fmt.Errorf("%w: %s: ssh key %s: %w", core.ErrNotFound, op, sshKeyID, apiErr)
		}
	}
	return err
}

type DeleteSSHKeyInput struct {
	SSHKeyID string `vngcloud:"required"`
}

type DeleteSSHKeyOutput struct{}

// DeleteSSHKey deletes a key. DELETE is idempotent and keeps the
// transport's own retries. A retry that finds the key already gone comes
// back as a 400 "Cannot get ssh key with id <id>", not a 404;
// DeleteSSHKey recognizes that specific message and returns the SDK's
// ordinary not-found sentinel for it, the same one core.ErrNotFound wraps
// everywhere else, so a caller checks vngcloud.IsNotFound(err) rather than
// matching the message itself.
func (c *Client) DeleteSSHKey(ctx context.Context, in *DeleteSSHKeyInput) (*DeleteSSHKeyOutput, error) {
	const op = "compute.DeleteSSHKey"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SSHKeyID", in.SSHKeyID); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.computeURL("v2", []string{projectID, "sshKeys", in.SSHKeyID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, mapSSHKeyDeleteNotFound(op, in.SSHKeyID, err)
	}
	return &DeleteSSHKeyOutput{}, nil
}
