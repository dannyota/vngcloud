package loadbalancer

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreatePolicyInput's Action values.
const (
	ActionRedirectToPool = "REDIRECT_TO_POOL"
	ActionRedirectToURL  = "REDIRECT_TO_URL"
)

// PolicyRuleInput's Type values.
const (
	PolicyRuleTypePath     = "PATH"
	PolicyRuleTypeHostName = "HOST_NAME"
)

// PolicyRuleInput's CompareType values.
const (
	CompareTypeContains   = "CONTAINS"
	CompareTypeEndsWith   = "ENDS_WITH"
	CompareTypeEqualTo    = "EQUAL_TO"
	CompareTypeRegex      = "REGEX"
	CompareTypeStartsWith = "STARTS_WITH"
)

// PolicyRuleInput is one L7 policy rule. Type is a PolicyRuleType constant
// and CompareType a CompareType constant; the server accepts other values
// too (ADR 0002 rule 5), but every field is required.
type PolicyRuleInput struct {
	Type        string `vngcloud:"required"`
	CompareType string `vngcloud:"required"`
	Value       string `vngcloud:"required"`
}

// policyRuleBody is one entry of the rules list a policy write sends.
type policyRuleBody struct {
	RuleType    string `json:"ruleType"`
	CompareType string `json:"compareType"`
	RuleValue   string `json:"ruleValue"`
}

func policyRuleBodyOf(r PolicyRuleInput) policyRuleBody {
	return policyRuleBody{RuleType: r.Type, CompareType: r.CompareType, RuleValue: r.Value}
}

func policyRuleBodiesOf(rules []PolicyRuleInput) []policyRuleBody {
	bodies := make([]policyRuleBody, len(rules))
	for i, r := range rules {
		bodies[i] = policyRuleBodyOf(r)
	}
	return bodies
}

// policyRuleBodiesFromRead builds the rules list from a Policy's own
// L7Rules exactly as GetPolicy read them, for UpdatePolicy's read-merge
// fallback when the caller leaves Rules unset.
func policyRuleBodiesFromRead(rules []L7Rule) []policyRuleBody {
	bodies := make([]policyRuleBody, len(rules))
	for i, r := range rules {
		bodies[i] = policyRuleBody{RuleType: r.RuleType, CompareType: r.CompareType, RuleValue: r.RuleValue}
	}
	return bodies
}

// checkPolicyRules returns core.ErrInvalidInput naming the first rule
// missing a field, for a rule missing Type, CompareType, or Value.
func checkPolicyRules(op string, rules []PolicyRuleInput) error {
	for i, r := range rules {
		if r.Type == "" || r.CompareType == "" || r.Value == "" {
			return fmt.Errorf("%w: %s: Rules[%d] requires Type, CompareType, and Value", core.ErrInvalidInput, op, i)
		}
	}
	return nil
}

// checkReadRulesComplete returns an error naming the first rule missing
// RuleType, CompareType, or RuleValue, for the rules UpdatePolicy read back
// and would otherwise resend unchanged when the caller leaves Rules unset.
// policyRuleBodiesFromRead can only carry forward what L7Rule models; a rule
// missing one of these three here would mean either a genuinely incomplete
// read or a server rule shape this SDK does not fully capture, and
// UpdatePolicy must refuse rather than silently resend it narrowed to
// whatever it did decode.
func checkReadRulesComplete(op string, rules []policyRuleBody) error {
	for i, r := range rules {
		if r.RuleType == "" || r.CompareType == "" || r.RuleValue == "" {
			return &core.APIError{Operation: op, Message: fmt.Sprintf("policy rule %d read back incomplete; refusing to resend it unchanged", i)}
		}
	}
	return nil
}

// policyRedirectFields are the redirect fields of a policy write, after any
// read-merge.
type policyRedirectFields struct {
	PoolID   string
	URL      string
	HTTPCode int
	Keep     bool
}

// carriesPool and carriesURL report which redirect fields an action sends.
// An action other than the two named reaches the server as given, with
// every field it is given (ADR 0002 rule 5).
func carriesPool(action string) bool { return action != ActionRedirectToURL }
func carriesURL(action string) bool  { return action != ActionRedirectToPool }

// checkPolicyRedirectFields returns core.ErrInvalidInput when action is
// ActionRedirectToPool but PoolID is empty or any URL field is set, or when
// action is ActionRedirectToURL but URL is empty or PoolID is set. The
// server refuses a field its action cannot carry, even at its zero value.
func checkPolicyRedirectFields(op, action string, f policyRedirectFields) error {
	switch action {
	case ActionRedirectToPool:
		if f.PoolID == "" {
			return fmt.Errorf("%w: %s: RedirectPoolID is required when Action is %s", core.ErrInvalidInput, op, ActionRedirectToPool)
		}
		if f.URL != "" || f.HTTPCode != 0 || f.Keep {
			return fmt.Errorf("%w: %s: RedirectURL, RedirectHTTPCode, and KeepQueryString must be unset when Action is %s", core.ErrInvalidInput, op, ActionRedirectToPool)
		}
	case ActionRedirectToURL:
		if f.URL == "" {
			return fmt.Errorf("%w: %s: RedirectURL is required when Action is %s", core.ErrInvalidInput, op, ActionRedirectToURL)
		}
		if f.PoolID != "" {
			return fmt.Errorf("%w: %s: RedirectPoolID must be empty when Action is %s", core.ErrInvalidInput, op, ActionRedirectToURL)
		}
	}
	return nil
}

// policyWriteBody is CreatePolicy and UpdatePolicy's shared body. Name is
// left empty, and dropped by omitempty, for an update: the API has no call
// to rename a policy. KeepQueryString is nil, and so omitted, for an action
// that cannot carry it.
type policyWriteBody struct {
	Name             string           `json:"name,omitempty"`
	Action           string           `json:"action"`
	RedirectPoolID   string           `json:"redirectPoolId,omitempty"`
	RedirectURL      string           `json:"redirectUrl,omitempty"`
	RedirectHTTPCode int              `json:"redirectHttpCode,omitempty"`
	KeepQueryString  *bool            `json:"keepQueryString,omitempty"`
	Rules            []policyRuleBody `json:"rules"`
}

// mergeRedirect returns the caller's value when set, else the value read
// when the action carries the field, else the zero value.
func mergeRedirect[T any](carried bool, set *T, read T) T {
	var zero T
	switch {
	case set != nil:
		return *set
	case carried:
		return read
	}
	return zero
}

// newPolicyWriteBody keeps only the redirect fields action carries.
func newPolicyWriteBody(name, action string, f policyRedirectFields, rules []policyRuleBody) policyWriteBody {
	b := policyWriteBody{Name: name, Action: action, Rules: rules}
	if carriesPool(action) {
		b.RedirectPoolID = f.PoolID
	}
	if carriesURL(action) {
		b.RedirectURL, b.RedirectHTTPCode, b.KeepQueryString = f.URL, f.HTTPCode, &f.Keep
	}
	return b
}

// CreatePolicyInput creates an L7 policy on a listener. Action is
// ActionRedirectToPool or ActionRedirectToURL, or another value the server
// accepts as is. ActionRedirectToPool requires RedirectPoolID and refuses
// RedirectURL, RedirectHTTPCode, and KeepQueryString; ActionRedirectToURL
// requires RedirectURL and refuses RedirectPoolID. A write sends only the
// fields its action carries. Every rule in Rules must set Type, CompareType, and
// Value.
type CreatePolicyInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`
	Name           string `vngcloud:"required"`
	// Action is ActionRedirectToPool, ActionRedirectToURL, or another value
	// the server accepts.
	Action           string `vngcloud:"required"`
	RedirectPoolID   string
	RedirectURL      string
	RedirectHTTPCode int
	KeepQueryString  bool
	Rules            []PolicyRuleInput

	NoWait bool
}

type CreatePolicyOutput struct {
	Policy Policy
}

// CreatePolicy creates an L7 policy on a listener. It waits, within the
// pre-write bound, until the load balancer is not busy (ErrBusy, nothing
// sent, past that bound), then sends the create.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// policy may have been created, and the caller checks with list-policies
// and matches Name exactly before creating it again. A response with no
// uuid is the same kind of error.
//
// Without NoWait, CreatePolicy then waits, within the child bound, for the
// policy's progressStatus to reach CREATED while the load balancer is no
// longer busy, the same wait CreatePool's doc comment describes; its
// Output follows the same fallback rule.
func (c *Client) CreatePolicy(ctx context.Context, in *CreatePolicyInput) (*CreatePolicyOutput, error) {
	const op = "loadbalancer.CreatePolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ListenerID", in.ListenerID); err != nil {
		return nil, err
	}
	if in.RedirectPoolID != "" {
		if err := core.CheckPathID(op, "RedirectPoolID", in.RedirectPoolID); err != nil {
			return nil, err
		}
	}
	createRedirectFields := policyRedirectFields{PoolID: in.RedirectPoolID, URL: in.RedirectURL, HTTPCode: in.RedirectHTTPCode, Keep: in.KeepQueryString}
	if err := checkPolicyRedirectFields(op, in.Action, createRedirectFields); err != nil {
		return nil, err
	}
	if err := checkPolicyRules(op, in.Rules); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := c.waitLoadBalancerPreWriteReady(ctx, op, in.LoadBalancerID); err != nil {
		return nil, err
	}

	body := newPolicyWriteBody(in.Name, in.Action, createRedirectFields, policyRuleBodiesOf(in.Rules))

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		UUID string `json:"uuid"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "listeners", in.ListenerID, "l7policies"}, nil),
		Body:      body,
		OK:        httpStatusOKCreate,
	}
	var status int
	sendErr := sendWithBusyResend(ctx,
		func(ctx context.Context) error { return c.waitLoadBalancerPreWriteReady(ctx, op, in.LoadBalancerID) },
		func() error {
			var err error
			status, err = c.c.DoJSONStatus(ctx, req, &resp)
			return err
		},
	)
	if sendErr != nil {
		return nil, wrapAmbiguousCreateErr(op, "list-policies", sendErr)
	}
	if resp.UUID == "" {
		return nil, errCreateResponseNoID(op, status, "list-policies")
	}

	fallback := Policy{UUID: resp.UUID, Name: in.Name, Action: in.Action, RedirectPoolID: in.RedirectPoolID, RedirectURL: in.RedirectURL}
	if in.NoWait {
		return &CreatePolicyOutput{Policy: fallback}, nil
	}

	var policy *Policy
	waitErr := c.waitChildSettled(ctx, op, in.LoadBalancerID, "policy", resp.UUID, func(ctx context.Context) (string, error) {
		out, err := c.GetPolicy(ctx, &GetPolicyInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID, PolicyID: resp.UUID})
		if err != nil {
			if core.IsNotFound(err) {
				return "", nil
			}
			return "", err
		}
		policy = &out.Policy
		return policy.ProgressStatus, nil
	})
	if policy == nil {
		policy = &fallback
	}
	return &CreatePolicyOutput{Policy: *policy}, waitErr
}

// UpdatePolicyInput changes a policy; a nil field keeps its current value.
// A set Rules replaces the whole rule list; an unset one resends the rules
// read.
type UpdatePolicyInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`
	PolicyID       string `vngcloud:"required"`

	Action           *string
	RedirectPoolID   *string
	RedirectURL      *string
	RedirectHTTPCode *int
	KeepQueryString  *bool
	Rules            *[]PolicyRuleInput

	NoWait bool
}

type UpdatePolicyOutput struct {
	Policy Policy
}

func updatePolicyAnySet(in *UpdatePolicyInput) bool {
	return in.Action != nil || in.RedirectPoolID != nil || in.RedirectURL != nil ||
		in.RedirectHTTPCode != nil || in.KeepQueryString != nil || in.Rules != nil
}

// UpdatePolicy changes a policy. At least one field must be set, checked
// before any request (core.ErrInvalidInput). It waits, within the
// pre-write bound, until the load balancer and the policy are both not busy
// (ErrBusy, nothing sent, past that bound), reads the policy, applies every
// set field, and sends the full body with the read values for the rest,
// keeping a read redirect field only when the merged Action carries it. The
// merged Action and redirect fields are checked exactly as CreatePolicy
// checks them.
//
// The PUT keeps the transport's normal retries: resending the same full
// body is safe. Without NoWait, UpdatePolicy waits exactly as CreatePolicy
// does; its Output is a fresh read once settled. On a wait failure it is
// instead the last read the wait itself completed, which may already show
// the update applied even though the wait never confirmed the load balancer
// settled, or, if no read ever completed, the policy as it read before the
// update, not the fields this call sent.
func (c *Client) UpdatePolicy(ctx context.Context, in *UpdatePolicyInput) (*UpdatePolicyOutput, error) {
	const op = "loadbalancer.UpdatePolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ListenerID", in.ListenerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}
	if !updatePolicyAnySet(in) {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}
	if in.RedirectPoolID != nil && *in.RedirectPoolID != "" {
		if err := core.CheckPathID(op, "RedirectPoolID", *in.RedirectPoolID); err != nil {
			return nil, err
		}
	}
	if in.Rules != nil {
		if err := checkPolicyRules(op, *in.Rules); err != nil {
			return nil, err
		}
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	policy, err := waitPreWriteReady(c, ctx, op, in.LoadBalancerID, "policy", in.PolicyID, func(ctx context.Context) (*Policy, string, error) {
		out, err := c.GetPolicy(ctx, &GetPolicyInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID, PolicyID: in.PolicyID})
		if err != nil {
			return nil, "", err
		}
		return &out.Policy, out.Policy.ProgressStatus, nil
	})
	if err != nil {
		return nil, err
	}

	action := stringOr(in.Action, policy.Action)
	fields := policyRedirectFields{
		PoolID:   mergeRedirect(carriesPool(action), in.RedirectPoolID, policy.RedirectPoolID),
		URL:      mergeRedirect(carriesURL(action), in.RedirectURL, policy.RedirectURL),
		HTTPCode: mergeRedirect(carriesURL(action), in.RedirectHTTPCode, policy.RedirectHTTPCode),
		Keep:     mergeRedirect(carriesURL(action), in.KeepQueryString, policy.KeepQueryString),
	}
	if err := checkPolicyRedirectFields(op, action, fields); err != nil {
		return nil, err
	}

	rules := policyRuleBodiesFromRead(policy.L7Rules)
	if in.Rules != nil {
		rules = policyRuleBodiesOf(*in.Rules)
	} else if err := checkReadRulesComplete(op, rules); err != nil {
		return nil, err
	}

	body := newPolicyWriteBody("", action, fields, rules)

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "listeners", in.ListenerID, "l7policies", in.PolicyID}, nil),
		Body:      body,
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, policyBusyWaiter(c, op, in.LoadBalancerID, in.ListenerID, in.PolicyID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, err
	}

	if in.NoWait {
		fallback := *policy
		fallback.Action, fallback.RedirectPoolID, fallback.RedirectURL = action, body.RedirectPoolID, body.RedirectURL
		fallback.RedirectHTTPCode, fallback.KeepQueryString = body.RedirectHTTPCode, fields.Keep
		return &UpdatePolicyOutput{Policy: fallback}, nil
	}

	var settled *Policy
	waitErr := c.waitChildSettled(ctx, op, in.LoadBalancerID, "policy", in.PolicyID, func(ctx context.Context) (string, error) {
		out, err := c.GetPolicy(ctx, &GetPolicyInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID, PolicyID: in.PolicyID})
		if err != nil {
			return "", err
		}
		settled = &out.Policy
		return settled.ProgressStatus, nil
	})
	if settled == nil {
		settled = policy
	}
	return &UpdatePolicyOutput{Policy: *settled}, waitErr
}

// DeletePolicyInput identifies the policy to delete.
type DeletePolicyInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`
	PolicyID       string `vngcloud:"required"`

	NoWait bool
}

type DeletePolicyOutput struct{}

// DeletePolicy deletes an L7 policy. It waits, within the pre-write bound,
// until the load balancer and the policy are both not busy (ErrBusy,
// nothing sent, past that bound), then sends the DELETE.
//
// DELETE is idempotent and keeps the transport's normal retries; a retry
// that finds the policy already gone returns core.ErrNotFound. Without
// NoWait, DeletePolicy then waits, within the child bound, for the policy
// to 404 while the load balancer is no longer busy, the same wait
// DeletePool's doc comment describes.
func (c *Client) DeletePolicy(ctx context.Context, in *DeletePolicyInput) (*DeletePolicyOutput, error) {
	const op = "loadbalancer.DeletePolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ListenerID", in.ListenerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PolicyID", in.PolicyID); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if _, err := waitPreWriteReady(c, ctx, op, in.LoadBalancerID, "policy", in.PolicyID, func(ctx context.Context) (struct{}, string, error) {
		out, err := c.GetPolicy(ctx, &GetPolicyInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID, PolicyID: in.PolicyID})
		if err != nil {
			return struct{}{}, "", err
		}
		return struct{}{}, out.Policy.ProgressStatus, nil
	}); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID, "listeners", in.ListenerID, "l7policies", in.PolicyID}, nil),
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, policyBusyWaiter(c, op, in.LoadBalancerID, in.ListenerID, in.PolicyID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &DeletePolicyOutput{}, nil
	}
	err = c.waitChildDeleted(ctx, op, in.LoadBalancerID, "policy", in.PolicyID, func(ctx context.Context) (string, bool, error) {
		out, err := c.GetPolicy(ctx, &GetPolicyInput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID, PolicyID: in.PolicyID})
		if err != nil {
			if core.IsNotFound(err) {
				return "", true, nil
			}
			return "", false, err
		}
		return out.Policy.ProgressStatus, false, nil
	})
	return &DeletePolicyOutput{}, err
}

// policyBusyWaiter returns sendWithBusyResend's waitBusy function for a
// write already targeting an existing policy: waiting again for the load
// balancer and the policy to both go idle, the same check UpdatePolicy and
// DeletePolicy already ran once before their first send.
func policyBusyWaiter(c *Client, op, lbID, listenerID, policyID string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := waitPreWriteReady(c, ctx, op, lbID, "policy", policyID, func(ctx context.Context) (*Policy, string, error) {
			out, err := c.GetPolicy(ctx, &GetPolicyInput{LoadBalancerID: lbID, ListenerID: listenerID, PolicyID: policyID})
			if err != nil {
				return nil, "", err
			}
			return &out.Policy, out.Policy.ProgressStatus, nil
		})
		return err
	}
}
