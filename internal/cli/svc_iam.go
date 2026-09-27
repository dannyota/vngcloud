package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/iam"
)

// iamOps is iam's operation table: every read the IAM writes design lists.
var iamOps = []Op[iam.Client]{
	Read[iam.Client, iam.GetCallerIdentityInput, iam.GetCallerIdentityOutput](
		kebab("GetCallerIdentity"), (*iam.Client).GetCallerIdentity),
	Read[iam.Client, iam.ListUsersInput, iam.ListUsersOutput](
		kebab("ListUsers"), (*iam.Client).ListUsers),
	Read[iam.Client, iam.ListActionsInput, iam.ListActionsOutput](
		kebab("ListActions"), (*iam.Client).ListActions),
	Read[iam.Client, iam.ListServiceAccountsInput, iam.ListServiceAccountsOutput](
		kebab("ListServiceAccounts"), (*iam.Client).ListServiceAccounts),
	Read[iam.Client, iam.GetServiceAccountInput, iam.GetServiceAccountOutput](
		kebab("GetServiceAccount"), (*iam.Client).GetServiceAccount),
	Read[iam.Client, iam.ListServiceAccountPoliciesInput, iam.ListServiceAccountPoliciesOutput](
		kebab("ListServiceAccountPolicies"), (*iam.Client).ListServiceAccountPolicies),
	Read[iam.Client, iam.ListPoliciesInput, iam.ListPoliciesOutput](
		kebab("ListPolicies"), (*iam.Client).ListPolicies),
	Read[iam.Client, iam.GetPolicyInput, iam.GetPolicyOutput](
		kebab("GetPolicy"), (*iam.Client).GetPolicy),
	Read[iam.Client, iam.ListPolicyAttachmentsInput, iam.ListPolicyAttachmentsOutput](
		kebab("ListPolicyAttachments"), (*iam.Client).ListPolicyAttachments),
	Read[iam.Client, iam.ListGroupsInput, iam.ListGroupsOutput](
		kebab("ListGroups"), (*iam.Client).ListGroups),
	Read[iam.Client, iam.GetGroupInput, iam.GetGroupOutput](
		kebab("GetGroup"), (*iam.Client).GetGroup),
	Read[iam.Client, iam.ListGroupPoliciesInput, iam.ListGroupPoliciesOutput](
		kebab("ListGroupPolicies"), (*iam.Client).ListGroupPolicies),
	Read[iam.Client, iam.ListUserGroupsInput, iam.ListUserGroupsOutput](
		kebab("ListUserGroups"), (*iam.Client).ListUserGroups),
	Read[iam.Client, iam.ListUserPoliciesInput, iam.ListUserPoliciesOutput](
		kebab("ListUserPolicies"), (*iam.Client).ListUserPolicies),
}

func newIAMCmd(e *env) *cobra.Command {
	return Service(e, "iam", "Callers, users, service accounts, groups, and policies", iam.New, iamOps...)
}
