package iam

import (
	"context"
	"fmt"
	"path"
	"strings"
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

// writeActionNames returns the full "iam:<Action>" name of every write
// action in actions, lower case, for matching against a policy statement's
// action patterns. actions must already be filtered to product "iam", as
// ListActions returns.
func writeActionNames(actions []Action) []string {
	names := make([]string, 0, len(actions))
	for _, a := range actions {
		if isWriteLabel(a.Label) {
			names = append(names, strings.ToLower("iam:"+a.Action))
		}
	}
	return names
}

// matchesWriteAction reports whether pattern, a statement's action entry
// such as "iam:*" or "iam:CreatePolicy", matches any name in
// writeActionNames, compared case-insensitively with "*" as a wildcard.
// path.Match never errors on the wildcard-only patterns this API uses, but
// a malformed pattern is treated as a match: a privileged policy that this
// check cannot parse must never be waved through.
func matchesWriteAction(pattern string, writeActionNames []string) bool {
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

// policyIsPrivileged reports whether p has an allow statement whose action
// pattern matches any name in writeActionNames. A deny statement is never
// privileged: [Guards](../docs/design/iam-writes.md#guards) only counts
// allow grants. Resources and conditions are not read.
func policyIsPrivileged(p *Policy, writeActionNames []string) bool {
	for _, stmt := range p.Statements {
		if !strings.EqualFold(stmt.Effect, "allow") {
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
// after that.
func (c *Client) guardWriteActionNames(ctx context.Context) ([]string, error) {
	c.guardMu.Lock()
	defer c.guardMu.Unlock()
	if c.guardActions == nil {
		out, err := c.ListActions(ctx, nil)
		if err != nil {
			return nil, err
		}
		c.guardActions = out.Items
	}
	return writeActionNames(c.guardActions), nil
}

// serviceAccountIsProtected reports whether serviceAccountID has a
// privileged policy attached: a "Protected principal" is a service account
// with a privileged policy attached, per the design's guard rules. seen
// dedupes GetPolicy reads within one guard call; it is never shared across
// calls.
func (c *Client) serviceAccountIsProtected(ctx context.Context, serviceAccountID string, writeActionNames []string, seen map[string]*Policy) (bool, error) {
	attached, err := c.ListServiceAccountPolicies(ctx, &ListServiceAccountPoliciesInput{ServiceAccountID: serviceAccountID})
	if err != nil {
		return false, err
	}
	for _, summary := range attached.Items {
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

// guardServiceAccountWrite refuses a write targeting serviceAccountID
// before op builds any request: it returns ErrSelfChange when the caller is
// that service account, ErrPrivilegedChange when the caller's type cannot
// be classified or the service account has a privileged policy attached,
// and the first error any guard read itself fails with. A nil return means
// op may proceed. No returned error names a statement, action, or policy
// beyond the rule it violates.
func (c *Client) guardServiceAccountWrite(ctx context.Context, op, serviceAccountID string) error {
	caller, err := c.guardCallerIdentity(ctx)
	if err != nil {
		return err
	}
	if !isClassifiedCallerType(caller.UserType) {
		return fmt.Errorf("%w: %s: the caller's user type is not recognized", ErrPrivilegedChange, op)
	}
	if isServiceAccountCallerType(caller.UserType) && caller.UserID == serviceAccountID {
		return fmt.Errorf("%w: %s: the service account is the caller", ErrSelfChange, op)
	}

	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	protected, err := c.serviceAccountIsProtected(ctx, serviceAccountID, writeActionNames, map[string]*Policy{})
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("%w: %s: the service account has a privileged policy attached", ErrPrivilegedChange, op)
	}
	return nil
}
