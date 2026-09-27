package iam

import (
	"context"
	"fmt"
)

// combineProtectedReasons merges two independent protectedReason findings,
// such as a user's own and its target group's, into the one result the
// design's self-wins rule requires: protectedBySelf if either input is
// protectedBySelf, even when the other is only protectedByPrivilege or
// notProtected; otherwise protectedByPrivilege if either is that.
func combineProtectedReasons(a, b protectedReason) protectedReason {
	if a == protectedBySelf || b == protectedBySelf {
		return protectedBySelf
	}
	if a == protectedByPrivilege || b == protectedByPrivilege {
		return protectedByPrivilege
	}
	return notProtected
}

// refuseForProtectedReason turns reason into the guard error op should
// return, using privilegedMsg for a protectedByPrivilege refusal, or nil
// when reason is notProtected.
func refuseForProtectedReason(op string, reason protectedReason, privilegedMsg string) error {
	switch reason {
	case protectedBySelf:
		return fmt.Errorf("%w: %s: the change would affect the caller's own rights", ErrSelfChange, op)
	case protectedByPrivilege:
		return fmt.Errorf("%w: %s: %s", ErrPrivilegedChange, op, privilegedMsg)
	default:
		return nil
	}
}

// guardDeleteGroup refuses to delete groupID before any request, per the
// design's guard rules: a group with a member or a policy attached is
// refused, protected or not, since a delete silently removes rights from
// every member. guardGetGroupAttachments fails closed on a null or missing
// policies or iamUsers field, so an unprovable read refuses rather than
// being read as an empty group.
func (c *Client) guardDeleteGroup(ctx context.Context, op, groupID string) error {
	if _, err := c.requireClassifiedCaller(ctx, op); err != nil {
		return err
	}
	policyIDs, userIDs, err := c.guardGetGroupAttachments(ctx, op, groupID)
	if err != nil {
		return err
	}
	if len(policyIDs) > 0 || len(userIDs) > 0 {
		return fmt.Errorf("%w: %s: the group has a member or a policy", ErrInUse, op)
	}
	return nil
}

// guardGroupMembershipWrite refuses to add or remove userID from groupID
// before op sends any request, per the design's guard rules: the user or the
// group is protected. It checks both independently, through the same
// userIsProtected and groupIsProtected the policy guards use, sharing one
// GetPolicy cache between them, and combines the two results so a
// self-change found on either side wins over a privileged-only refusal on
// the other.
func (c *Client) guardGroupMembershipWrite(ctx context.Context, op, groupID, userID string) error {
	caller, err := c.requireClassifiedCaller(ctx, op)
	if err != nil {
		return err
	}
	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	seen := map[string]*Policy{}

	userReason, err := c.userIsProtected(ctx, op, userID, caller, writeActionNames, seen)
	if err != nil {
		return err
	}
	groupReason, err := c.groupIsProtected(ctx, op, groupID, caller, writeActionNames, seen)
	if err != nil {
		return err
	}
	return refuseForProtectedReason(op, combineProtectedReasons(userReason, groupReason), "the user or the group is protected")
}

// guardGroupPolicyAttach refuses to attach or detach policyID and groupID
// before op sends any request, per the design's guard rules: the target
// group is protected, checked first so a self-change is never shadowed by a
// separate privileged-policy finding, or the policy itself is privileged.
// It covers both attach and detach: the design refuses either the same way.
func (c *Client) guardGroupPolicyAttach(ctx context.Context, op, policyID, groupID string) error {
	caller, err := c.requireClassifiedCaller(ctx, op)
	if err != nil {
		return err
	}
	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	reason, err := c.groupIsProtected(ctx, op, groupID, caller, writeActionNames, map[string]*Policy{})
	if err != nil {
		return err
	}
	if err := refuseForProtectedReason(op, reason, "the group is protected"); err != nil {
		return err
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: policyID})
	if err != nil {
		return err
	}
	if policyIsPrivileged(&got.Policy, writeActionNames) {
		return fmt.Errorf("%w: %s: the policy grants an IAM write action", ErrPrivilegedChange, op)
	}
	return nil
}

// guardUserPolicyAttach is guardGroupPolicyAttach for attaching or detaching
// policyID directly to or from an IAM user: the target user is protected,
// checked first for the same reason, or the policy itself is privileged.
func (c *Client) guardUserPolicyAttach(ctx context.Context, op, policyID, userID string) error {
	caller, err := c.requireClassifiedCaller(ctx, op)
	if err != nil {
		return err
	}
	writeActionNames, err := c.guardWriteActionNames(ctx)
	if err != nil {
		return err
	}
	reason, err := c.userIsProtected(ctx, op, userID, caller, writeActionNames, map[string]*Policy{})
	if err != nil {
		return err
	}
	if err := refuseForProtectedReason(op, reason, "the user is protected"); err != nil {
		return err
	}

	got, err := c.GetPolicy(ctx, &GetPolicyInput{PolicyID: policyID})
	if err != nil {
		return err
	}
	if policyIsPrivileged(&got.Policy, writeActionNames) {
		return fmt.Errorf("%w: %s: the policy grants an IAM write action", ErrPrivilegedChange, op)
	}
	return nil
}
