package iam

import (
	"context"
	"fmt"
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

	protected, err := c.policyAttachedToProtected(ctx, op, policyID, caller, writeActionNames)
	if err != nil {
		return nil, err
	}
	if protected {
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

	attachments, err := c.ListPolicyAttachments(ctx, &ListPolicyAttachmentsInput{PolicyID: policyID})
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
func (c *Client) policyAttachedToProtected(ctx context.Context, op, policyID string, caller *GetCallerIdentityOutput, writeActionNames []string) (bool, error) {
	attachments, err := c.ListPolicyAttachments(ctx, &ListPolicyAttachmentsInput{PolicyID: policyID})
	if err != nil {
		return false, err
	}
	seen := map[string]*Policy{}

	for _, group := range attachments.Groups {
		protected, err := c.groupIsProtected(ctx, op, group.ID, caller, writeActionNames, seen)
		if err != nil {
			return false, err
		}
		if protected {
			return true, nil
		}
	}
	for _, userID := range attachments.UserIDs {
		protected, err := c.userIsProtected(ctx, op, userID, caller, writeActionNames, seen)
		if err != nil {
			return false, err
		}
		if protected {
			return true, nil
		}
	}
	for _, saID := range attachments.ServiceAccountIDs {
		protected, err := c.serviceAccountIsProtected(ctx, op, saID, writeActionNames, seen)
		if err != nil {
			return false, err
		}
		if protected {
			return true, nil
		}
	}
	return false, nil
}

// groupIsProtected reports whether a group is a "Protected group": one with
// a privileged policy attached, read from the single GetGroup response
// (embedded, not the paged groups/{id}/policies list, so no page can hide
// one), or with a protected member. Group members are IAM users only; the
// design's open questions note that no call yet adds a service account to a
// group.
func (c *Client) groupIsProtected(ctx context.Context, op, groupID string, caller *GetCallerIdentityOutput, writeActionNames []string, seen map[string]*Policy) (bool, error) {
	got, err := c.GetGroup(ctx, &GetGroupInput{GroupID: groupID})
	if err != nil {
		return false, err
	}
	privileged, err := c.anyPolicyIDPrivileged(ctx, got.Group.PolicyIDs, writeActionNames, seen)
	if err != nil {
		return false, err
	}
	if privileged {
		return true, nil
	}
	for _, userID := range got.Group.UserIDs {
		protected, err := c.userIsProtected(ctx, op, userID, caller, writeActionNames, seen)
		if err != nil {
			return false, err
		}
		if protected {
			return true, nil
		}
	}
	return false, nil
}

// userIsProtected reports whether userID is a "Protected principal": the
// caller, an IAM user with a privileged policy attached directly, or one
// that belongs to a group with a privileged policy attached. The direct
// attachment read is the paged, fail-closed guardUserPolicySummaries, not
// the plain ListUserPolicies, so a truncated page can never hide a
// privileged policy. The group check reads each of the user's groups by
// GetGroup, the same embedded PolicyIDs groupIsProtected trusts, so it never
// recurses back into groupIsProtected itself.
func (c *Client) userIsProtected(ctx context.Context, op, userID string, caller *GetCallerIdentityOutput, writeActionNames []string, seen map[string]*Policy) (bool, error) {
	if !isServiceAccountCallerType(caller.UserType) && caller.UserID == userID {
		return true, nil
	}

	direct, err := c.guardUserPolicySummaries(ctx, op, userID)
	if err != nil {
		return false, err
	}
	directIDs := make([]string, len(direct))
	for i, summary := range direct {
		directIDs[i] = summary.ID
	}
	privileged, err := c.anyPolicyIDPrivileged(ctx, directIDs, writeActionNames, seen)
	if err != nil {
		return false, err
	}
	if privileged {
		return true, nil
	}

	groups, err := c.ListUserGroups(ctx, &ListUserGroupsInput{UserID: userID})
	if err != nil {
		return false, err
	}
	for _, group := range groups.Items {
		privileged, err := c.anyPolicyIDPrivileged(ctx, group.PolicyIDs, writeActionNames, seen)
		if err != nil {
			return false, err
		}
		if privileged {
			return true, nil
		}
	}
	return false, nil
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
