package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"danny.vn/vngcloud/internal/transport"
)

// Caller user types the console and the action list name. callerTypeRoot is
// not a classified type for guard purposes: the guard's "Caller" is an IAM
// user or a service account only, so a root-user token, like any other
// unrecognized UserType, fails every guarded write.
const (
	callerTypeRoot      = "root-user"
	callerTypeIAMUser   = "iam-user"
	callerTypeUserSA    = "user-sa"
	callerTypeServiceSA = "service-sa"
)

// isClassifiedCallerType reports whether userType is one the guard can
// reason about: an IAM user or a service account. callerTypeRoot is
// deliberately excluded; see the constant block above.
func isClassifiedCallerType(userType string) bool {
	switch userType {
	case callerTypeIAMUser, callerTypeUserSA, callerTypeServiceSA:
		return true
	default:
		return false
	}
}

// isServiceAccountCallerType reports whether userType identifies a service
// account principal, as opposed to an IAM user.
func isServiceAccountCallerType(userType string) bool {
	return userType == callerTypeUserSA || userType == callerTypeServiceSA
}

// isWriteLabel reports whether label marks an IAM action as a write action
// for the guard rules: every label except "List", "Read", and "Tagging",
// including one the action list has not named before.
func isWriteLabel(label string) bool {
	return label != "List" && label != "Read" && label != "Tagging"
}

// stripIAMPrefix removes a leading "iam:" from action, compared
// case-insensitively, so an action value the server already qualifies with
// its product is not double-prefixed by writeActionNames. Leaving a
// double-prefixed name in place (such as "iam:iam:createpolicy") would
// never match a normal "iam:createpolicy" pattern, silently hiding a real
// write action from the privileged check.
func stripIAMPrefix(action string) string {
	const prefix = "iam:"
	if len(action) >= len(prefix) && strings.EqualFold(action[:len(prefix)], prefix) {
		return action[len(prefix):]
	}
	return action
}

// writeActionNames returns the full "iam:<Action>" name of every write
// action in actions, lower case, for matching against a policy statement's
// action patterns. actions must already be filtered to product "iam", as
// ListActions returns.
func writeActionNames(actions []Action) []string {
	names := make([]string, 0, len(actions))
	for _, a := range actions {
		if isWriteLabel(a.Label) {
			names = append(names, strings.ToLower("iam:"+stripIAMPrefix(a.Action)))
		}
	}
	return names
}

// alwaysPrivilegedActionPattern reports whether pattern, compared
// case-insensitively, grants every IAM write action by its own shape alone:
// a bare "*", the explicit "iam:*", or the product wildcard "*:*", which
// also covers every other product. These always count as privileged, even
// against an empty or incomplete writeActionNames, so a broken account
// action list can never hide one of them.
func alwaysPrivilegedActionPattern(pattern string) bool {
	switch strings.ToLower(pattern) {
	case "*", "*:*", "iam:*":
		return true
	default:
		return false
	}
}

// matchesWriteAction reports whether pattern, a statement's action entry
// such as "iam:*" or "iam:CreatePolicy", matches any name in
// writeActionNames, compared case-insensitively with "*" as a wildcard.
// path.Match never errors on the wildcard-only patterns this API uses, but
// a malformed pattern is treated as a match: a privileged policy that this
// check cannot parse must never be waved through.
func matchesWriteAction(pattern string, writeActionNames []string) bool {
	if alwaysPrivilegedActionPattern(pattern) {
		return true
	}
	lowered := strings.ToLower(pattern)
	for _, name := range writeActionNames {
		matched, err := path.Match(lowered, name)
		if err != nil {
			return true
		}
		if matched {
			return true
		}
	}
	return false
}

// statementsArePrivileged reports whether statements has any statement,
// other than an explicit "deny", whose action pattern matches any name in
// writeActionNames. No statements at all counts as privileged: there is
// nothing to prove the document safe, so it is refused rather than waved
// through. Resources and conditions are not read.
func statementsArePrivileged(statements []Statement, writeActionNames []string) bool {
	if len(statements) == 0 {
		return true
	}
	for _, stmt := range statements {
		if strings.EqualFold(stmt.Effect, "deny") {
			continue
		}
		for _, pattern := range stmt.Actions {
			if matchesWriteAction(pattern, writeActionNames) {
				return true
			}
		}
	}
	return false
}

// policyIsPrivileged reports whether p's statements are privileged; see
// statementsArePrivileged.
func policyIsPrivileged(p *Policy, writeActionNames []string) bool {
	return statementsArePrivileged(p.Statements, writeActionNames)
}

// guardCallerIdentity returns the caller's identity, fetching it once per
// Client and reusing the cached value after that.
func (c *Client) guardCallerIdentity(ctx context.Context) (*GetCallerIdentityOutput, error) {
	c.guardMu.Lock()
	defer c.guardMu.Unlock()
	if c.guardCaller != nil {
		return c.guardCaller, nil
	}
	caller, err := c.GetCallerIdentity(ctx, nil)
	if err != nil {
		return nil, err
	}
	c.guardCaller = caller
	return caller, nil
}

// guardWriteActionNames returns the account's IAM write action names,
// fetching the action list once per Client and reusing the cached value
// after that. It refuses with an error, and never caches the result, when
// the fetched list names no write action at all: IAM always has write
// actions, so an empty result is treated as a failed read, never as
// evidence that no policy can be privileged. A guarded write that let an
// empty action list through would have every privileged-policy check
// silently pass, turning every guard off at once.
func (c *Client) guardWriteActionNames(ctx context.Context) ([]string, error) {
	c.guardMu.Lock()
	defer c.guardMu.Unlock()
	if len(c.guardWriteActions) > 0 {
		return c.guardWriteActions, nil
	}
	out, err := c.ListActions(ctx, nil)
	if err != nil {
		return nil, err
	}
	names := writeActionNames(out.Items)
	if len(names) == 0 {
		return nil, errors.New("iam: guard: refused: the account's IAM action list names no write action")
	}
	c.guardWriteActions = names
	return names, nil
}

// maxGuardAttachmentPage caps the page size a guard read asks for when it
// lists a service account's attached policies. The account's own quota
// (20 customer policies) is far below it, so a real response always fits on
// one page; guardServiceAccountPolicySummaries refuses rather than trust a
// response that claims otherwise.
const maxGuardAttachmentPage = 10000

// pagedPolicySummaries is the shape a paged policy-summary attachment list
// decodes into for a guard read. Data and TotalItems are pointers so a key
// the server leaves out is distinguishable from an explicit empty array or
// zero: guardServiceAccountPolicySummaries refuses on a missing key rather
// than reading it as "no attachments". TotalPages is checked only when the
// server sends it.
type pagedPolicySummaries struct {
	Data       *[]PolicySummary `json:"data"`
	TotalItems *int             `json:"totalItems"`
	TotalPages *int             `json:"totalPages"`
}

// guardServiceAccountPolicySummaries reads every policy attached to
// serviceAccountID for a guard check; see guardPagedPolicySummaries.
func (c *Client) guardServiceAccountPolicySummaries(ctx context.Context, op, serviceAccountID string) ([]PolicySummary, error) {
	return c.guardPagedPolicySummaries(ctx, op, []string{"user-attachments", "service-accounts", serviceAccountID, "policies"})
}

// guardUserPolicySummaries reads every policy attached to userID for a
// guard check; see guardPagedPolicySummaries.
func (c *Client) guardUserPolicySummaries(ctx context.Context, op, userID string) ([]PolicySummary, error) {
	return c.guardPagedPolicySummaries(ctx, op, []string{"user-attachments", "iam-users", userID, "policies"})
}

// guardPagedPolicySummaries reads a policy-attachment list at urlParts for a
// guard check, in one page sized at maxGuardAttachmentPage, and refuses
// instead of guessing whenever the response cannot prove that page held
// everything: a missing data or totalItems key, a totalItems the returned
// data does not match, or more than one totalPages. Reading a partial or
// ambiguous page as "no more attachments" would silently stop protecting a
// principal whose attachments the guard never fully saw.
func (c *Client) guardPagedPolicySummaries(ctx context.Context, op string, urlParts []string) ([]PolicySummary, error) {
	var resp pagedPolicySummaries
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL(urlParts, pageQuery(0, maxGuardAttachmentPage)),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("iam: guard: %s: attachment list response had no data field", op)
	}
	if resp.TotalItems == nil {
		return nil, fmt.Errorf("iam: guard: %s: attachment list response had no totalItems field", op)
	}
	if len(*resp.Data) != *resp.TotalItems {
		return nil, fmt.Errorf("iam: guard: %s: attachment list held %d of totalItems %d", op, len(*resp.Data), *resp.TotalItems)
	}
	if resp.TotalPages != nil && *resp.TotalPages > 1 {
		return nil, fmt.Errorf("iam: guard: %s: attachment list reports more than one page", op)
	}
	return *resp.Data, nil
}

// serviceAccountIsProtected reports whether serviceAccountID has a
// privileged policy attached: a "Protected principal" is a service account
// with a privileged policy attached, per the design's guard rules. seen
// dedupes GetPolicy reads within one guard call; it is never shared across
// calls.
func (c *Client) serviceAccountIsProtected(ctx context.Context, op, serviceAccountID string, writeActionNames []string, seen map[string]*Policy) (bool, error) {
	attached, err := c.guardServiceAccountPolicySummaries(ctx, op, serviceAccountID)
	if err != nil {
		return false, err
	}
	for _, summary := range attached {
		policy, ok := seen[summary.ID]
		if !ok {
			got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: summary.ID})
			if err != nil {
				return false, err
			}
			policy = &got.Policy
			seen[summary.ID] = policy
		}
		if policyIsPrivileged(policy, writeActionNames) {
			return true, nil
		}
	}
	return false, nil
}

// requireClassifiedCaller returns the caller's identity, refusing op with no
// request sent when the caller's user type is not one the guard can reason
// about (see the design's Terms section: an unknown user type fails every
// guarded write, whatever that write's own rule names).
func (c *Client) requireClassifiedCaller(ctx context.Context, op string) (*GetCallerIdentityOutput, error) {
	caller, err := c.guardCallerIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if !isClassifiedCallerType(caller.UserType) {
		return nil, fmt.Errorf("%w: %s: the caller's user type is not recognized", ErrPrivilegedChange, op)
	}
	return caller, nil
}

// guardServiceAccountWrite refuses a write targeting serviceAccountID
// before op builds any request: it returns ErrSelfChange when the caller is
// a service account (see below), ErrPrivilegedChange when the caller's type
// cannot be classified or the service account has a privileged policy
// attached, and the first error any guard read itself fails with. A nil
// return means op may proceed. No returned error names a statement, action,
// or policy beyond the rule it violates.
//
// A caller whose type is user-sa or service-sa refuses every
// service-account-targeted write, not only one against its own ID.
// GetCallerIdentity's UserID for a service-account caller is not confirmed
// to use the same ID form as a target service account's ID or its ClientID
// (see the design's open questions), so comparing them could miss a real
// self-change; refusing every case is the conservative choice until that is
// verified live.
func (c *Client) guardServiceAccountWrite(ctx context.Context, op, serviceAccountID string) error {
	caller, err := c.requireClassifiedCaller(ctx, op)
	if err != nil {
		return err
	}
	if isServiceAccountCallerType(caller.UserType) {
		return fmt.Errorf("%w: %s: the caller is a service account", ErrSelfChange, op)
	}

	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	protected, err := c.serviceAccountIsProtected(ctx, op, serviceAccountID, writeActionNames, map[string]*Policy{})
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("%w: %s: the service account has a privileged policy attached", ErrPrivilegedChange, op)
	}
	return nil
}
