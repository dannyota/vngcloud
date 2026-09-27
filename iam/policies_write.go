package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// checkPolicyStatements checks a policy document's shape only, before any
// request and before the privileged-policy guard: at least one statement,
// Effect is "allow" or "deny", and Actions and Resources are each non-empty
// with no empty string. Condition keys are not checked, and no value, such
// as an action or resource name, is checked here: those stay server rules
// (ADR 0002 rule 5). Decoding with unknown fields refused, so an AWS-style
// document fails instead of reaching this check with no statements, is the
// CLI's job.
func checkPolicyStatements(op string, statements []Statement) error {
	if len(statements) == 0 {
		return fmt.Errorf("%w: %s requires at least one statement", core.ErrInvalidInput, op)
	}
	for i, stmt := range statements {
		if stmt.Effect != "allow" && stmt.Effect != "deny" {
			return fmt.Errorf("%w: %s: statement %d effect must be \"allow\" or \"deny\"", core.ErrInvalidInput, op, i)
		}
		if len(stmt.Actions) == 0 {
			return fmt.Errorf("%w: %s: statement %d has no actions", core.ErrInvalidInput, op, i)
		}
		if len(stmt.Resources) == 0 {
			return fmt.Errorf("%w: %s: statement %d has no resources", core.ErrInvalidInput, op, i)
		}
		for _, a := range stmt.Actions {
			if a == "" {
				return fmt.Errorf("%w: %s: statement %d has an empty action", core.ErrInvalidInput, op, i)
			}
		}
		for _, r := range stmt.Resources {
			if r == "" {
				return fmt.Errorf("%w: %s: statement %d has an empty resource", core.ErrInvalidInput, op, i)
			}
		}
	}
	return nil
}

// CreatePolicyInput's Statements go through checkPolicyStatements (shape
// only) and then the privileged-policy guard, before any request; see
// guard.go.
type CreatePolicyInput struct {
	Name        string `vngcloud:"required"`
	Description string
	Statements  []Statement `vngcloud:"required"`
}

type CreatePolicyOutput struct {
	Policy Policy
}

type createPolicyBody struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Statements  []Statement `json:"statements"`
}

type createPolicyResponse struct {
	ID string `json:"id"`
}

// CreatePolicy creates a customer policy and reads it back with GetPolicy.
//
// It sets transport.Request.Once: a resend after a 401 or a followed
// redirect would create a second policy, so the request is sent at most
// once. After any error that is not a 4xx *core.APIError, the policy may
// exist regardless, and the caller lists policies by Name before creating it
// again, rather than retrying blind.
func (c *Client) CreatePolicy(ctx context.Context, in *CreatePolicyInput) (*CreatePolicyOutput, error) {
	const op = "iam.CreatePolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkPolicyStatements(op, in.Statements); err != nil {
		return nil, err
	}
	if err := c.guardCreatePolicy(ctx, op, in.Statements); err != nil {
		return nil, err
	}

	var resp createPolicyResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.policiesURL([]string{"policies"}, nil),
		Body:      createPolicyBody{Name: in.Name, Description: in.Description, Statements: in.Statements},
		OK:        []int{201},
		Once:      true,
	}
	if _, err := c.c.DoJSONStatus(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousPolicyCreateErr(op, err)
	}
	if resp.ID == "" {
		return nil, &core.APIError{Operation: op, Message: "create response had no id; a policy may exist, check with list-policies --name"}
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: resp.ID})
	if err != nil {
		return nil, fmt.Errorf("%s: policy %s was created but the read to confirm it failed: %w", op, resp.ID, err)
	}
	return &CreatePolicyOutput{Policy: got.Policy}, nil
}

// wrapAmbiguousPolicyCreateErr wraps err from CreatePolicy when it failed
// ambiguously: a 5xx or a network error, where whether the request reached
// the server is unknown. It is never called for a 4xx *core.APIError, which
// means the request was rejected outright and nothing was created. A nil err
// stays nil.
func wrapAmbiguousPolicyCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; check with list-policies --name before creating it again: %w", op, err)
}

// UpdatePolicyInput's nil fields are filled from the policy's current state
// before sending, because the API's PUT may replace the whole policy; see
// guard.go and CreatePolicyInput's Statements note. PolicyID must not be
// managed and must not be, or become, privileged; see guard.go.
type UpdatePolicyInput struct {
	PolicyID    string `vngcloud:"required"`
	Name        *string
	Description *string
	Statements  *[]Statement
}

type UpdatePolicyOutput struct {
	Policy Policy
}

type updatePolicyBody struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Statements  []Statement `json:"statements"`
}

// UpdatePolicy replaces a customer policy's name, description, and
// statements with a PUT, filling any field the caller left nil from the
// policy's own current state, and reads it back with GetPolicy.
//
// The guard runs first and sends nothing when it refuses: ErrManagedPolicy
// if the policy is managed, or ErrPrivilegedChange if its current or
// proposed statements are privileged or it is attached to a protected
// principal or group.
//
// PUT is idempotent regardless of any request field, so the transport's own
// retry after a 5xx is safe to repeat.
func (c *Client) UpdatePolicy(ctx context.Context, in *UpdatePolicyInput) (*UpdatePolicyOutput, error) {
	const op = "iam.UpdatePolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if in.Statements != nil {
		if err := checkPolicyStatements(op, *in.Statements); err != nil {
			return nil, err
		}
	}

	current, err := c.guardUpdatePolicy(ctx, op, in.PolicyID, in.Statements)
	if err != nil {
		return nil, err
	}

	body := updatePolicyBody{Name: current.Name, Description: current.Description, Statements: current.Statements}
	if in.Name != nil {
		body.Name = *in.Name
	}
	if in.Description != nil {
		body.Description = *in.Description
	}
	if in.Statements != nil {
		body.Statements = *in.Statements
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.policiesURL([]string{"policies", in.PolicyID}, nil),
		Body:      body,
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: in.PolicyID})
	if err != nil {
		return nil, fmt.Errorf("%s: policy %s was updated but the read to confirm it failed: %w", op, in.PolicyID, err)
	}
	return &UpdatePolicyOutput{Policy: got.Policy}, nil
}

// DeletePolicyInput identifies the policy to delete. It must not be managed
// and must not be attached to anything; see guard.go.
type DeletePolicyInput struct {
	PolicyID string `vngcloud:"required"`
}

type DeletePolicyOutput struct{}

// DeletePolicy deletes a customer policy. The guard runs first and sends
// nothing when it refuses: ErrManagedPolicy if the policy is managed, or
// ErrInUse if it is attached to a group, an IAM user, or a service account.
// DELETE is idempotent and keeps the transport's own retries.
func (c *Client) DeletePolicy(ctx context.Context, in *DeletePolicyInput) (*DeletePolicyOutput, error) {
	const op = "iam.DeletePolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if err := c.guardDeletePolicy(ctx, op, in.PolicyID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.policiesURL([]string{"policies", in.PolicyID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeletePolicyOutput{}, nil
}
