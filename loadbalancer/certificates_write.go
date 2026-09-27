package loadbalancer

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Certificate type values ImportCertificateInput.Type takes. The server
// accepts other values too (ADR 0002 rule 5: value rules stay on the
// server), but only CertificateTypeTLS changes which fields ImportCertificate
// allows; see its doc comment.
const (
	CertificateTypeTLS = "TLS/SSL"
	CertificateTypeCA  = "CA"
)

// ErrCertificateInUse means a certificate delete was refused because a
// listener still uses it: either DeleteCertificate's own pre-read found
// Certificate.InUse true, sending nothing, or the server's own refusal on
// the DELETE itself named it, whatever status the server used.
var ErrCertificateInUse = errors.New("loadbalancer: certificate in use")

// certificatePrivateKeyMarker is the text every PEM private key header
// contains ("-----BEGIN ... PRIVATE KEY-----"). ImportCertificate refuses a
// Certificate or CertificateChain holding it, so a caller who passes a key
// file as one of those public fields never sends it, and it never reaches an
// error message either: those fields are not vngcloud.Secret, so their text
// could otherwise be echoed back unredacted.
const certificatePrivateKeyMarker = "PRIVATE KEY"

// checkNoCertificatePrivateKeyText returns an error wrapping
// core.ErrInvalidInput when value contains certificatePrivateKeyMarker,
// naming field but never echoing value.
func checkNoCertificatePrivateKeyText(op, field, value string) error {
	if strings.Contains(value, certificatePrivateKeyMarker) {
		return fmt.Errorf("%w: %s: %s looks like a private key, not a certificate", core.ErrInvalidInput, op, field)
	}
	return nil
}

// checkPEMCertificateBlocks returns an error wrapping core.ErrInvalidInput
// when value does not decode as one or more PEM blocks all of type
// "CERTIFICATE". It never echoes value.
func checkPEMCertificateBlocks(op, field, value string) error {
	rest := []byte(value)
	blocks := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("%w: %s: %s must hold only PEM CERTIFICATE blocks, found %q", core.ErrInvalidInput, op, field, block.Type)
		}
		blocks++
	}
	if blocks == 0 {
		return fmt.Errorf("%w: %s: %s must hold a PEM CERTIFICATE block", core.ErrInvalidInput, op, field)
	}
	return nil
}

// checkPrivateKeyPEM returns an error wrapping core.ErrInvalidInput when
// privateKey does not decode as exactly one PEM block whose type ends in
// "PRIVATE KEY" (covering "RSA PRIVATE KEY", "EC PRIVATE KEY", "PRIVATE
// KEY", and "ENCRYPTED PRIVATE KEY"). It never echoes privateKey.
func checkPrivateKeyPEM(op, privateKey string) error {
	block, rest := pem.Decode([]byte(privateKey))
	if block == nil {
		return fmt.Errorf("%w: %s: PrivateKey must hold a PEM block", core.ErrInvalidInput, op)
	}
	if len(strings.TrimSpace(string(rest))) != 0 {
		return fmt.Errorf("%w: %s: PrivateKey must hold exactly one PEM block", core.ErrInvalidInput, op)
	}
	if !strings.HasSuffix(block.Type, "PRIVATE KEY") {
		return fmt.Errorf("%w: %s: PrivateKey must be a PEM block whose type ends in PRIVATE KEY, found %q", core.ErrInvalidInput, op, block.Type)
	}
	return nil
}

// importCertificateBody is ImportCertificate's request body. Every field is
// a plain string, filled from ImportCertificateInput.PrivateKey.Reveal() and
// .Passphrase.Reveal(): a vngcloud.Secret placed in the body directly would
// encode as "[redacted]", and the server would receive that text as the key.
// Empty optional fields are left out with omitempty, per the design.
type importCertificateBody struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	Certificate      string `json:"certificate"`
	CertificateChain string `json:"certificateChain,omitempty"`
	PrivateKey       string `json:"privateKey,omitempty"`
	Passphrase       string `json:"passphrase,omitempty"`
}

// ImportCertificateInput imports a certificate. Certificate and
// CertificateChain (when given) must each hold only PEM CERTIFICATE blocks
// and must never contain private key text; PrivateKey is required, and
// checked to be a single PEM private-key block, only when Type is
// CertificateTypeTLS. When Type is anything else, PrivateKey, Passphrase,
// and CertificateChain must all be empty: a key the server has no use for
// should not leave the machine. Every check runs before any request and
// never echoes the rejected value, only the field name.
type ImportCertificateInput struct {
	Name             string `vngcloud:"required"`
	Type             string `vngcloud:"required"`
	Certificate      string `vngcloud:"required"`
	CertificateChain string
	PrivateKey       vngcloud.Secret
	Passphrase       vngcloud.Secret
}

type ImportCertificateOutput struct {
	Certificate Certificate
}

// wrapAmbiguousCertificateImportErr wraps err from ImportCertificate's POST
// that failed ambiguously: a 5xx or a network error, where whether the
// certificate reached the server is unknown. It is never called for a 4xx
// *core.APIError, which means the request was rejected outright and nothing
// was imported. name is the exact value the call sent, so the hint tells the
// caller how to check what happened instead of retrying blind; a certificate
// found that way is complete, since its key already went in the request that
// may have reached the server. A nil err stays nil.
func wrapAmbiguousCertificateImportErr(op, name string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: import may have already reached the server; check with list-certificates --name %q, then match exactly: %w", op, name, err)
}

// ImportCertificate imports a certificate for use by vLB listeners.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// certificate may exist, and the caller lists certificates by Name before
// importing it again, rather than retrying blind; a certificate found that
// way is complete, since its key already went in the request.
//
// The request sets transport.Request.Sensitive, so the response never
// reaches the configured response-capture hook, and Redact with PrivateKey
// and Passphrase, so a rejecting response's error message never echoes
// either back, even a single line of a multi-line key.
func (c *Client) ImportCertificate(ctx context.Context, in *ImportCertificateInput) (*ImportCertificateOutput, error) {
	const op = "loadbalancer.ImportCertificate"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkNoCertificatePrivateKeyText(op, "Certificate", in.Certificate); err != nil {
		return nil, err
	}
	if err := checkPEMCertificateBlocks(op, "Certificate", in.Certificate); err != nil {
		return nil, err
	}
	if in.CertificateChain != "" {
		if err := checkNoCertificatePrivateKeyText(op, "CertificateChain", in.CertificateChain); err != nil {
			return nil, err
		}
		if err := checkPEMCertificateBlocks(op, "CertificateChain", in.CertificateChain); err != nil {
			return nil, err
		}
	}

	privateKey := in.PrivateKey.Reveal()
	passphrase := in.Passphrase.Reveal()

	if in.Type == CertificateTypeTLS && privateKey == "" {
		return nil, fmt.Errorf("%w: %s requires PrivateKey when Type is %s", core.ErrInvalidInput, op, CertificateTypeTLS)
	}
	if privateKey != "" {
		if err := checkPrivateKeyPEM(op, privateKey); err != nil {
			return nil, err
		}
	}
	if passphrase != "" && privateKey == "" {
		return nil, fmt.Errorf("%w: %s: Passphrase must be empty unless PrivateKey is given", core.ErrInvalidInput, op)
	}
	if in.Type != CertificateTypeTLS && (privateKey != "" || passphrase != "" || in.CertificateChain != "") {
		return nil, fmt.Errorf("%w: %s: PrivateKey, Passphrase, and CertificateChain must be empty unless Type is %s", core.ErrInvalidInput, op, CertificateTypeTLS)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data Certificate `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.lbURL([]string{projectID, "cas"}, nil),
		Body: importCertificateBody{
			Name:             in.Name,
			Type:             in.Type,
			Certificate:      in.Certificate,
			CertificateChain: in.CertificateChain,
			PrivateKey:       privateKey,
			Passphrase:       passphrase,
		},
		OK:        []int{201},
		Sensitive: true,
		Redact:    []string{privateKey, passphrase},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousCertificateImportErr(op, in.Name, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: status,
			Message:    fmt.Sprintf("import response had no id; a certificate may exist, check with list-certificates --name %q and match exactly", in.Name),
		}
	}
	return &ImportCertificateOutput{Certificate: resp.Data}, nil
}

// certificateInUseMessage reports whether msg indicates the server refused a
// request because the certificate is still in use by a listener, matched
// case-insensitively against "in use" or "is used": the design's assumed
// wording, unconfirmed against a live server since the test account has no
// load balancer to provoke it.
func certificateInUseMessage(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "in use") || strings.Contains(lower, "is used")
}

// wrapCertificateDeleteErr rewraps err with ErrCertificateInUse when it is a
// *core.APIError whose message matches certificateInUseMessage, whatever its
// status: the server's own refusal is the final guard behind
// DeleteCertificate's pre-read. Any other error passes through unchanged.
func wrapCertificateDeleteErr(op, certificateID string, err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && certificateInUseMessage(apiErr.Message) {
		return fmt.Errorf("%w: %s: certificate %s: %w", ErrCertificateInUse, op, certificateID, err)
	}
	return err
}

// certificateFoundAfterError lists certificates once and reports whether id
// is present, the same list confirm vServer network writes uses for network
// ACLs. ListCertificates defaults to a page of core.DefaultPageSize (10000),
// so this misses an id only in a project with more certificates than that.
//
// A list that comes back short of the account's own count, more than one
// page or fewer items than TotalItem, is not a reliable absence: id could
// simply be on a page this call never asked for. mapCertificateNotFound
// already treats any error from this method as "fall back to the original
// error," so this returns an error instead of a bare false in that case.
func (c *Client) certificateFoundAfterError(ctx context.Context, id string) (bool, error) {
	out, err := c.ListCertificates(ctx, nil)
	if err != nil {
		return false, err
	}
	for _, cert := range out.Items {
		if cert.UUID == id {
			return true, nil
		}
	}
	if out.TotalPage > 1 || len(out.Items) < out.TotalItem {
		return false, fmt.Errorf("certificate list confirm: got %d of %d item(s) across %d page(s); inconclusive",
			len(out.Items), out.TotalItem, out.TotalPage)
	}
	return false, nil
}

// mapCertificateNotFound rewraps err, from a GetCertificate pre-read or a
// delete call that failed with a status other than 404, into an error
// wrapping core.ErrNotFound once a follow-up ListCertificates confirms id is
// no longer listed. An err that already wraps core.ErrNotFound (a plain 404)
// passes through unchanged, with no list call; so does any error the list
// call itself cannot resolve, or that still lists id, since the original
// failure may mean something else entirely. A nil err stays nil.
func (c *Client) mapCertificateNotFound(ctx context.Context, op, id string, err error) error {
	if err == nil || core.IsNotFound(err) {
		return err
	}
	found, listErr := c.certificateFoundAfterError(ctx, id)
	if listErr != nil || found {
		return err
	}
	return fmt.Errorf("%w: %s: certificate %s", core.ErrNotFound, op, id)
}

type DeleteCertificateInput struct {
	CertificateID string `vngcloud:"required"`
}

type DeleteCertificateOutput struct{}

// DeleteCertificate reads the certificate first with GetCertificate and
// returns ErrCertificateInUse, sending nothing, when InUse is true. The
// server's own refusal is the final guard: an API error from the DELETE
// itself whose message matches certificateInUseMessage also wraps
// ErrCertificateInUse, whatever its status.
//
// Delete is synchronous: a 204 confirms it, and there is no wait. A retried
// delete that finds the certificate already gone returns an error wrapping
// core.ErrNotFound: after any error that is not already core.ErrNotFound,
// from either the pre-read or the DELETE itself, DeleteCertificate lists
// certificates once and checks whether CertificateID is still listed;
// absent, it returns core.ErrNotFound; listed, or if the list call itself
// fails, it returns the original error.
func (c *Client) DeleteCertificate(ctx context.Context, in *DeleteCertificateInput) (*DeleteCertificateOutput, error) {
	const op = "loadbalancer.DeleteCertificate"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CertificateID", in.CertificateID); err != nil {
		return nil, err
	}

	cert, err := c.GetCertificate(ctx, &GetCertificateInput{CertificateID: in.CertificateID})
	if err != nil {
		return nil, c.mapCertificateNotFound(ctx, op, in.CertificateID, err)
	}
	if cert.Certificate.InUse {
		return nil, fmt.Errorf("%w: %s: certificate %s is in use", ErrCertificateInUse, op, in.CertificateID)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	delErr := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.lbURL([]string{projectID, "cas", in.CertificateID}, nil),
		OK:        []int{204},
	}, nil)
	if delErr == nil {
		return &DeleteCertificateOutput{}, nil
	}
	delErr = wrapCertificateDeleteErr(op, in.CertificateID, delErr)
	if errors.Is(delErr, ErrCertificateInUse) {
		return nil, delErr
	}
	return nil, c.mapCertificateNotFound(ctx, op, in.CertificateID, delErr)
}
