package iam

import (
	"context"
	"fmt"
)

// protectedReason is the result of a check for a protected principal or
// group: notProtected when nothing was found, and otherwise which error
// class the finding requires. protectedBySelf always wins over
// protectedByPrivilege, per the design's rule that a change to the caller's
// own rights is ErrSelfChange even when the caller is also, separately, a
// protected principal.
type protectedReason int

const (
	notProtected protectedReason = iota
	protectedBySelf
	protectedByPrivilege
)

// guardCreatePolicy refuses to create a policy whose statements are
// privileged, per the design's guard rules. It sends nothing on refusal. An
// unclassified caller type also refuses, per the design's Terms section,
// even though CreatePolicy targets no principal.
func (c *Client) guardCreatePolicy(ctx context.Context, op string, statements []Statement) error {
	if _, err := c.requireClassifiedCaller(ctx, op); err != nil {
		return err
	}
	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	if statementsArePrivileged(statements, writeActionNames) {
		return fmt.Errorf("%w: %s: the statements grant an IAM write action", ErrPrivilegedChange, op)
	}
	return nil
}

// guardUpdatePolicy refuses to update policyID before any request, per the
// design's guard rules: the policy is managed; its current or proposed
// statements are privileged; or it is attached to a protected principal or
// group. newStatements is the update's own Statements field, nil when the
// caller leaves it unset.
//
// On success it returns the policy's current state, read once, so
// UpdatePolicy can fill in the PUT body's fields the caller left nil from
// it, without a second read.
func (c *Client) guardUpdatePolicy(ctx context.Context, op, policyID string, newStatements *[]Statement) (*Policy, error) {
	caller, err := c.requireClassifiedCaller(ctx, op)
	if err != nil {
		return nil, err
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: policyID})
	if err != nil {
		return nil, err
	}
	current := got.Policy
	if current.Managed() {
		return nil, fmt.Errorf("%w: %s: the policy is managed", ErrManagedPolicy, op)
	}

	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return nil, err
	}
	if statementsArePrivileged(current.Statements, writeActionNames) {
		return nil, fmt.Errorf("%w: %s: the policy's current statements grant an IAM write action", ErrPrivilegedChange, op)
	}
	if newStatements != nil && statementsArePrivileged(*newStatements, writeActionNames) {
		return nil, fmt.Errorf("%w: %s: the proposed statements grant an IAM write action", ErrPrivilegedChange, op)
	}

	reason, err := c.policyAttachedToProtected(ctx, op, policyID, caller, writeActionNames)
	if err != nil {
		return nil, err
	}
	switch reason {
	case protectedBySelf:
		return nil, fmt.Errorf("%w: %s: the change would affect the caller's own rights", ErrSelfChange, op)
	case protectedByPrivilege:
		return nil, fmt.Errorf("%w: %s: the policy is attached to a protected principal or group", ErrPrivilegedChange, op)
	}
	return &current, nil
}

// guardDeletePolicy refuses to delete policyID before any request: the
// design's guard rules refuse a managed policy, and a policy attached to
// anything at all, protected or not, since a delete silently removes rights
// from every attached principal and group.
func (c *Client) guardDeletePolicy(ctx context.Context, op, policyID string) error {
	if _, err := c.requireClassifiedCaller(ctx, op); err != nil {
		return err
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: policyID})
	if err != nil {
		return err
	}
	if got.Policy.Managed() {
		return fmt.Errorf("%w: %s: the policy is managed", ErrManagedPolicy, op)
	}

	attachments, err := c.guardPolicyAttachments(ctx, op, policyID)
	if err != nil {
		return err
	}
	if len(attachments.Groups) > 0 || len(attachments.UserIDs) > 0 || len(attachments.ServiceAccountIDs) > 0 {
		return fmt.Errorf("%w: %s: the policy is attached", ErrInUse, op)
	}
	return nil
}

// guardServiceAccountPolicyAttach refuses to attach or detach policyID and
// serviceAccountID before op sends any request, per the design's guard
// rules: it runs guardServiceAccountWrite's caller and target checks first,
// then refuses again when the policy itself is privileged. This covers both
// attach and detach: the design refuses a privileged policy either way.
func (c *Client) guardServiceAccountPolicyAttach(ctx context.Context, op, policyID, serviceAccountID string) error {
	if err := c.guardServiceAccountWrite(ctx, op, serviceAccountID); err != nil {
		return err
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: policyID})
	if err != nil {
		return err
	}
	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	if policyIsPrivileged(&got.Policy, writeActionNames) {
		return fmt.Errorf("%w: %s: the policy grants an IAM write action", ErrPrivilegedChange, op)
	}
	return nil
}

// policyAttachedToProtected reports whether policyID is attached to a
// protected principal or group, per the design's guard rules: a group with
// a privileged policy attached or a protected member; an IAM user that is
// the caller, holds a privileged policy directly, or belongs to a group
// with one; or a service account with a privileged policy attached. seen
// dedupes GetPolicy reads across this whole check.
//
// A caller whose own type is a service account never reaches the
// service-account loop below: as soon as the policy is attached to any
// service account at all, this refuses as protectedBySelf, mirroring
// guardServiceAccountWrite's own blanket refusal (see its doc comment), since
// the caller's UserID is not confirmed to use the same form as a target
// service account's ID and so cannot safely be ruled out.
func (c *Client) policyAttachedToProtected(ctx context.Context, op, policyID string, caller *GetCallerIdentityOutput, writeActionNames []string) (protectedReason, error) {
	attachments, err := c.guardPolicyAttachments(ctx, op, policyID)
	if err != nil {
		return notProtected, err
	}
	seen := map[string]*Policy{}

	for _, group := range attachments.Groups {
		reason, err := c.groupIsProtected(ctx, op, group.ID, caller, writeActionNames, seen)
		if err != nil {
			return notProtected, err
		}
		if reason != notProtected {
			return reason, nil
		}
	}
	for _, userID := range attachments.UserIDs {
		reason, err := c.userIsProtected(ctx, op, userID, caller, writeActionNames, seen)
		if err != nil {
			return notProtected, err
		}
		if reason != notProtected {
			return reason, nil
		}
	}
	if isServiceAccountCallerType(caller.UserType) && len(attachments.ServiceAccountIDs) > 0 {
		return protectedBySelf, nil
	}
	for _, saID := range attachments.ServiceAccountIDs {
		protected, err := c.serviceAccountIsProtected(ctx, op, saID, writeActionNames, seen)
		if err != nil {
			return notProtected, err
		}
		if protected {
			return protectedByPrivilege, nil
		}
	}
	return notProtected, nil
}

// groupIsProtected reports whether a group is a "Protected group": one with
// a privileged policy attached, read from the single guardGetGroupAttachments
// read (embedded, not the paged groups/{id}/policies list, so no page can
// hide one), or with a protected member. Group members are IAM users only;
// the design's open questions note that no call yet adds a service account
// to a group.
func (c *Client) groupIsProtected(ctx context.Context, op, groupID string, caller *GetCallerIdentityOutput, writeActionNames []string, seen map[string]*Policy) (protectedReason, error) {
	policyIDs, userIDs, err := c.guardGetGroupAttachments(ctx, op, groupID)
	if err != nil {
		return notProtected, err
	}
	privileged, err := c.anyPolicyIDPrivileged(ctx, policyIDs, writeActionNames, seen)
	if err != nil {
		return notProtected, err
	}
	if privileged {
		return protectedByPrivilege, nil
	}
	for _, userID := range userIDs {
		reason, err := c.userIsProtected(ctx, op, userID, caller, writeActionNames, seen)
		if err != nil {
			return notProtected, err
		}
		if reason != notProtected {
			return reason, nil
		}
	}
	return notProtected, nil
}

// userIsProtected reports whether userID is a "Protected principal": the
// caller, an IAM user with a privileged policy attached directly, or one
// that belongs to a group with a privileged policy attached. A match on the
// caller itself returns protectedBySelf; either other case returns
// protectedByPrivilege. The direct attachment read is the paged, fail-closed
// guardUserPolicySummaries, not the plain ListUserPolicies, so a truncated
// page can never hide a privileged policy. The group check reads the user's
// groups and each one's own policies field through guardUserGroups, the
// guard's fail-closed decode of the same endpoint ListUserGroups uses, so a
// null or missing policies list refuses instead of being read as "no
// policies"; it never recurses back into groupIsProtected itself.
func (c *Client) userIsProtected(ctx context.Context, op, userID string, caller *GetCallerIdentityOutput, writeActionNames []string, seen map[string]*Policy) (protectedReason, error) {
	if !isServiceAccountCallerType(caller.UserType) && caller.UserID == userID {
		return protectedBySelf, nil
	}

	direct, err := c.guardUserPolicySummaries(ctx, op, userID)
	if err != nil {
		return notProtected, err
	}
	directIDs := make([]string, len(direct))
	for i, summary := range direct {
		directIDs[i] = summary.ID
	}
	privileged, err := c.anyPolicyIDPrivileged(ctx, directIDs, writeActionNames, seen)
	if err != nil {
		return notProtected, err
	}
	if privileged {
		return protectedByPrivilege, nil
	}

	groups, err := c.guardUserGroups(ctx, op, userID)
	if err != nil {
		return notProtected, err
	}
	for _, group := range groups {
		privileged, err := c.anyPolicyIDPrivileged(ctx, *group.PolicyIDs, writeActionNames, seen)
		if err != nil {
			return notProtected, err
		}
		if privileged {
			return protectedByPrivilege, nil
		}
	}
	return notProtected, nil
}

// anyPolicyIDPrivileged reports whether any policy in policyIDs is
// privileged, reading each one not already in seen with GetPolicy. seen
// dedupes reads across one guard call; it is never shared across calls.
func (c *Client) anyPolicyIDPrivileged(ctx context.Context, policyIDs []string, writeActionNames []string, seen map[string]*Policy) (bool, error) {
	for _, id := range policyIDs {
		policy, ok := seen[id]
		if !ok {
			got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: id})
			if err != nil {
				return false, err
			}
			policy = &got.Policy
			seen[id] = policy
		}
		if policyIsPrivileged(policy, writeActionNames) {
			return true, nil
		}
	}
	return false, nil
}
