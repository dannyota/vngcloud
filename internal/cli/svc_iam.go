package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
)

// iamOps is iam's operation table. Every read the IAM writes design lists
// runs unmodified through Read. CreateServiceAccount and
// ResetServiceAccountSecret are built by hand, like compute's
// create-ssh-key, since both need --secret-file, a flag no Input field
// derives, and each wraps the SDK's own Output with a SecretFile field.
// UpdateServiceAccount and DeleteServiceAccount need no CLI-side guard: the
// SDK itself refuses a self-change or a privileged target before sending
// anything (see iam/guard.go), and internal/cli/errors.go maps those
// sentinels to the SelfChange and PrivilegedChange error classes.
// ResetServiceAccountSecret and DeleteServiceAccount are Destructive, per
// the design's CLI table: a reset invalidates the previous secret with no
// way back, and a delete removes the account outright.
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
	createServiceAccountOp(),
	Write[iam.Client, iam.UpdateServiceAccountInput, iam.UpdateServiceAccountOutput](
		kebab("UpdateServiceAccount"), (*iam.Client).UpdateServiceAccount),
	resetServiceAccountSecretOp(),
	Write[iam.Client, iam.DeleteServiceAccountInput, iam.DeleteServiceAccountOutput](
		kebab("DeleteServiceAccount"), (*iam.Client).DeleteServiceAccount, Destructive()),
}

func newIAMCmd(e *env) *cobra.Command {
	return Service(e, "iam", "Callers, users, service accounts, groups, and policies", iam.New, iamOps...)
}

// createServiceAccountOutput is create-service-account's own JSON shape:
// iam's CreateServiceAccountOutput, whose ClientSecret already prints
// "[redacted]" through vngcloud.Secret's own encoding, plus the path
// --secret-file wrote the secret to. The embedding flattens ServiceAccount
// and ClientSecret to the top level, the same shape CreateServiceAccountOutput's
// own fields would have on their own.
type createServiceAccountOutput struct {
	iam.CreateServiceAccountOutput
	SecretFile string
}

// createServiceAccountOp builds create-service-account's Op directly, for
// the same two reasons createSSHKeyOp (svc_compute.go) does: --secret-file
// has no backing Input field, and a successful call's own Output type
// (createServiceAccountOutput above) differs from iam.CreateServiceAccountOutput.
// methodName is still set to "CreateServiceAccount" by hand, so
// checkOpName's name-matches-the-SDK-method rule holds exactly as it does
// for every op Write and Read build from a real method value.
func createServiceAccountOp() Op[iam.Client] {
	return Op[iam.Client]{
		name:       kebab("CreateServiceAccount"),
		methodName: "CreateServiceAccount",
		kind:       kindWrite,
		guard:      guardSecretFilePath,
		extraFlags: registerSecretFileFlag,
		newInput:   func() any { return new(iam.CreateServiceAccountInput) },
		newOutput:  func() any { return new(createServiceAccountOutput) },
		call:       callCreateServiceAccount,
	}
}

// callCreateServiceAccount runs the real create, then writes the client
// secret to --secret-file, which guardSecretFilePath already checked before
// this call ever ran. Per the design's secret file rules: a create response
// with no secret at all keeps the new service account and writes no file,
// reporting SecretFileFailed and naming reset-service-account-secret; a
// write failure after a create that did return a secret deletes the new
// service account through the SDK, since the secret is unusable either way
// once lost, and reports SecretFileFailed; if that delete itself fails, the
// resource is named only by its ID. The cleanup delete runs on a context
// detached from ctx (context.WithoutCancel, with its own short timeout), so
// a canceled command still cleans up the orphaned account; a NotFound from
// that delete counts as cleanup succeeding, since the account is gone
// either way.
func callCreateServiceAccount(cmd *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
	out, err := client.CreateServiceAccount(ctx, in.(*iam.CreateServiceAccountInput))
	if err != nil {
		return nil, err
	}
	secret := out.ClientSecret.Reveal()
	if secret == "" {
		return nil, newSecretFileNoSecret("service account", out.ServiceAccount.ID, "reset-service-account-secret")
	}
	// guardSecretFilePath ran before any request and already required this
	// flag and checked its path, so the only new failure possible here is
	// the actual write.
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	if !strings.HasSuffix(secret, "\n") {
		secret += "\n"
	}
	if writeErr := writeSecretFile(path, []byte(secret)); writeErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretFileCleanupDeleteTimeout)
		defer cancel()
		if _, delErr := client.DeleteServiceAccount(cleanupCtx, &iam.DeleteServiceAccountInput{ServiceAccountID: out.ServiceAccount.ID}); delErr != nil && !vngcloud.IsNotFound(delErr) {
			return nil, newSecretFileCleanupFailed("service account", out.ServiceAccount.ID)
		}
		return nil, newSecretFileWriteFailed("service account", out.ServiceAccount.ID, writeErr)
	}
	return &createServiceAccountOutput{CreateServiceAccountOutput: *out, SecretFile: path}, nil
}

// resetServiceAccountSecretOutput is reset-service-account-secret's own JSON
// shape: iam's ResetServiceAccountSecretOutput, whose ClientSecret already
// prints "[redacted]", plus the path --secret-file wrote the new secret to.
type resetServiceAccountSecretOutput struct {
	iam.ResetServiceAccountSecretOutput
	SecretFile string
}

// resetServiceAccountSecretOp builds reset-service-account-secret's Op
// directly, for the same reasons createServiceAccountOp does, plus
// Destructive: the reset invalidates the previous secret immediately, with
// no way back, so the design's CLI table requires --yes.
func resetServiceAccountSecretOp() Op[iam.Client] {
	return Op[iam.Client]{
		name:        kebab("ResetServiceAccountSecret"),
		methodName:  "ResetServiceAccountSecret",
		kind:        kindWrite,
		destructive: true,
		guard:       guardSecretFilePath,
		extraFlags:  registerSecretFileFlag,
		newInput:    func() any { return new(iam.ResetServiceAccountSecretInput) },
		newOutput:   func() any { return new(resetServiceAccountSecretOutput) },
		call:        callResetServiceAccountSecret,
	}
}

// callResetServiceAccountSecret runs the real reset, then writes the new
// client secret to --secret-file. Unlike create-service-account's own
// failure path, a reset cannot be undone: the old secret already stopped
// working the moment the reset request landed, so a write failure here
// reports SecretFileFailed and tells the caller to reset again, rather than
// deleting anything.
func callResetServiceAccountSecret(cmd *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
	resetIn := in.(*iam.ResetServiceAccountSecretInput)
	out, err := client.ResetServiceAccountSecret(ctx, resetIn)
	if err != nil {
		return nil, err
	}
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	secret := out.ClientSecret.Reveal()
	if !strings.HasSuffix(secret, "\n") {
		secret += "\n"
	}
	if writeErr := writeSecretFile(path, []byte(secret)); writeErr != nil {
		return nil, newSecretFileResetFailed("service account", resetIn.ServiceAccountID, "reset-service-account-secret", writeErr)
	}
	return &resetServiceAccountSecretOutput{ResetServiceAccountSecretOutput: *out, SecretFile: path}, nil
}
