package cli

import (
	"context"
	"errors"
	"fmt"
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
//
// CreatePolicy and UpdatePolicy are built by hand for the same reason as
// CreateServiceAccount: --document-file, the only way to give Statements a
// value beyond --cli-input-json, backs no Input field. DeletePolicy,
// AttachServiceAccountPolicy, and DetachServiceAccountPolicy need no
// CLI-side guard: the SDK itself refuses a managed or attached policy, a
// privileged one, or a protected target before sending anything (see
// iam/policy_guard.go), and internal/cli/errors.go maps those sentinels to
// the ManagedPolicy, ResourceInUse, SelfChange, and PrivilegedChange error
// classes. UpdatePolicy, DeletePolicy, AttachServiceAccountPolicy, and
// DetachServiceAccountPolicy are Destructive, per the design's CLI table.
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
	createPolicyOp(),
	updatePolicyOp(),
	Write[iam.Client, iam.DeletePolicyInput, iam.DeletePolicyOutput](
		kebab("DeletePolicy"), (*iam.Client).DeletePolicy, Destructive()),
	Write[iam.Client, iam.AttachServiceAccountPolicyInput, iam.AttachServiceAccountPolicyOutput](
		kebab("AttachServiceAccountPolicy"), (*iam.Client).AttachServiceAccountPolicy, Destructive()),
	Write[iam.Client, iam.DetachServiceAccountPolicyInput, iam.DetachServiceAccountPolicyOutput](
		kebab("DetachServiceAccountPolicy"), (*iam.Client).DetachServiceAccountPolicy, Destructive()),
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
// this call ever ran. iam.CreateServiceAccount returns a non-nil Output
// alongside an error for its two documented failure cases, and this call
// branches on them before touching the generic err != nil case below:
//
//   - iam.ErrNoSecret: the create response held no secret at all. The new
//     service account is kept, no file is written, and the command reports
//     SecretFileFailed naming reset-service-account-secret.
//   - iam.ErrCreateUnconfirmed: the create landed but the read-back that
//     fills in the rest of the service account's fields failed.
//     finishUnconfirmedServiceAccountCreate still writes --secret-file from
//     the create response's own secret, since the account and its secret
//     are real either way and the secret is only ever handed out once.
//
// Any other error means the create request itself never produced a usable
// response, so nothing was created and nothing is written.
//
// A write failure after a create that did return a secret deletes the new
// service account through the SDK, since the secret is unusable either way
// once lost, and reports SecretFileFailed; if that delete itself fails, the
// resource is named only by its ID. The cleanup delete runs on a context
// detached from ctx (context.WithoutCancel, with its own short timeout), so
// a canceled command still cleans up the orphaned account; a NotFound from
// that delete counts as cleanup succeeding, since the account is gone
// either way.
func callCreateServiceAccount(cmd *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
	out, err := client.CreateServiceAccount(ctx, in.(*iam.CreateServiceAccountInput))
	// guardSecretFilePath ran before any request and already required this
	// flag and checked its path, so the only new failure possible below is
	// the actual write.
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	switch {
	case errors.Is(err, iam.ErrNoSecret):
		return nil, newSecretFileNoSecret("service account", out.ServiceAccount.ID, "reset-service-account-secret")
	case errors.Is(err, iam.ErrCreateUnconfirmed):
		return finishUnconfirmedServiceAccountCreate(client, ctx, out, path)
	case err != nil:
		return nil, err
	}
	if writeErr := writeServiceAccountSecretFile(client, ctx, out.ServiceAccount.ID, out.ClientSecret.Reveal(), path); writeErr != nil {
		return nil, writeErr
	}
	return &createServiceAccountOutput{CreateServiceAccountOutput: *out, SecretFile: path}, nil
}

// finishUnconfirmedServiceAccountCreate handles iam.ErrCreateUnconfirmed:
// out already carries the create response's own ID and client secret
// (every other ServiceAccount field zero), since CreateServiceAccount never
// drops a secret the server issued just because its own read-back failed.
// The secret is written to path exactly as a normal success would; only
// this command's own success, and the account's other fields, are what the
// failed read-back costs. A write failure still deletes the account through
// the SDK by the create response's own ID, since no read-back ID exists to
// use instead, the same cleanup callCreateServiceAccount's success path
// uses. When the create response itself held no secret either, this is
// treated the same as iam.ErrNoSecret: no file is written and the account
// is kept, since there is nothing left to write or lose.
func finishUnconfirmedServiceAccountCreate(client *iam.Client, ctx context.Context, out *iam.CreateServiceAccountOutput, path string) (any, error) {
	id := out.ServiceAccount.ID
	secret := out.ClientSecret.Reveal()
	if secret == "" {
		return nil, newSecretFileNoSecret("service account", id, "reset-service-account-secret")
	}
	if writeErr := writeServiceAccountSecretFile(client, ctx, id, secret, path); writeErr != nil {
		return nil, writeErr
	}
	return nil, newServiceAccountCreateUnconfirmed(id, path, "list-service-accounts")
}

// writeServiceAccountSecretFile writes secret to path, the file
// --secret-file names, with one trailing newline added only when secret
// does not already end in one. A write failure deletes serviceAccountID
// through the SDK, since the secret is unusable either way once lost, and
// returns newSecretFileWriteFailed, or newSecretFileCleanupFailed if that
// delete itself fails; a NotFound from the delete counts as cleanup
// succeeding, since the account is gone either way. The cleanup delete runs
// on a context detached from ctx (context.WithoutCancel, with its own short
// timeout), so a canceled command still cleans up the orphaned account. A
// nil return means path now holds secret.
func writeServiceAccountSecretFile(client *iam.Client, ctx context.Context, serviceAccountID, secret, path string) error {
	if !strings.HasSuffix(secret, "\n") {
		secret += "\n"
	}
	if writeErr := writeSecretFile(path, []byte(secret)); writeErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretFileCleanupDeleteTimeout)
		defer cancel()
		if _, delErr := client.DeleteServiceAccount(cleanupCtx, &iam.DeleteServiceAccountInput{ServiceAccountID: serviceAccountID}); delErr != nil && !vngcloud.IsNotFound(delErr) {
			return newSecretFileCleanupFailed("service account", serviceAccountID)
		}
		return newSecretFileWriteFailed("service account", serviceAccountID, writeErr)
	}
	return nil
}

// newServiceAccountCreateUnconfirmed reports create-service-account's own
// partial-success case (iam.ErrCreateUnconfirmed): the create request
// landed and the secret is safely in secretFilePath, but the read-back
// CreateServiceAccount makes to fill in the rest of the service account's
// fields failed, so this command cannot show them or report success.
// checkCommand names how the caller can see the account's current fields.
func newServiceAccountCreateUnconfirmed(id, secretFilePath, checkCommand string) error {
	return fmt.Errorf("service account %s was created and its secret saved to %s, but the read to confirm it failed; run %s to check it: %w",
		id, secretFilePath, checkCommand, iam.ErrCreateUnconfirmed)
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
// deleting anything. ResetServiceAccountSecret's own iam.ErrNoSecret case is
// handled the same way: the reset itself still reached the server and most
// likely rotated the secret without returning it, so this writes no file
// and reports SecretFileFailed naming this same command to run again,
// rather than pointing at any other one.
func callResetServiceAccountSecret(cmd *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
	resetIn := in.(*iam.ResetServiceAccountSecretInput)
	out, err := client.ResetServiceAccountSecret(ctx, resetIn)
	if errors.Is(err, iam.ErrNoSecret) {
		return nil, newSecretFileNoSecretRotated("service account", resetIn.ServiceAccountID, "reset-service-account-secret")
	}
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

// createPolicyOp and updatePolicyOp build create-policy's and
// update-policy's Op directly, rather than through Write, because Statements
// has no flag of its own (flagSpecsFor never derives one for a slice field):
// both need --document-file, registered through extraFlags, and a guard that
// reads it into the Input before checkRequiredFlags or the SDK ever sees it.
// Statements is marked NoFlag so gen-docs lists it as JSON-only rather than
// as a nonexistent "--statements" flag; guardCreatePolicyDocument's own
// message names --document-file directly instead of relying on
// checkRequiredFlags' generic NoFlag message, which would not.
func createPolicyOp() Op[iam.Client] {
	return Op[iam.Client]{
		name:       kebab("CreatePolicy"),
		methodName: "CreatePolicy",
		kind:       kindWrite,
		guard:      guardCreatePolicyDocument,
		noFlag:     map[string]bool{"Statements": true},
		extraFlags: registerDocumentFileFlag,
		newInput:   func() any { return new(iam.CreatePolicyInput) },
		newOutput:  func() any { return new(iam.CreatePolicyOutput) },
		call: func(_ *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
			return client.CreatePolicy(ctx, in.(*iam.CreatePolicyInput))
		},
	}
}

// guardCreatePolicyDocument reads --document-file, when given, into the
// merged Input's Statements field before create-policy's own required-field
// check runs: a document that fails to parse refuses the whole command here,
// before any request, and an empty --document-file leaves whatever
// --cli-input-json already set (checkRequiredFlags then requires Statements,
// via a message this function's own final check improves on). Statements
// set through --document-file always wins over one --cli-input-json set,
// matching every other field's own flag-wins-over-JSON rule (input.go).
func guardCreatePolicyDocument(cmd *cobra.Command, in any) error {
	create := in.(*iam.CreatePolicyInput)
	path, err := cmd.Flags().GetString(policyDocumentFlagName)
	if err != nil {
		return usageError{msg: err.Error()}
	}
	if path != "" {
		statements, rerr := readPolicyDocumentFile(path)
		if rerr != nil {
			return rerr
		}
		create.Statements = statements
	}
	if len(create.Statements) == 0 {
		return newUsageError("create-policy needs Statements: pass --document-file <path> or set Statements with --cli-input-json")
	}
	return nil
}

func updatePolicyOp() Op[iam.Client] {
	return Op[iam.Client]{
		name:        kebab("UpdatePolicy"),
		methodName:  "UpdatePolicy",
		kind:        kindWrite,
		destructive: true,
		guard:       guardUpdatePolicyDocument,
		noFlag:      map[string]bool{"Statements": true},
		extraFlags:  registerDocumentFileFlag,
		newInput:    func() any { return new(iam.UpdatePolicyInput) },
		newOutput:   func() any { return new(iam.UpdatePolicyOutput) },
		call: func(_ *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
			return client.UpdatePolicy(ctx, in.(*iam.UpdatePolicyInput))
		},
	}
}

// guardUpdatePolicyDocument mirrors guardCreatePolicyDocument for
// update-policy, whose Statements is optional (*[]iam.Statement): a
// --document-file given at all, even one that parses to zero statements
// (UpdatePolicy's own SDK-side shape check refuses that, before any
// request), sets the pointer; leaving --document-file unset leaves the field
// nil, exactly as if the caller never mentioned Statements.
func guardUpdatePolicyDocument(cmd *cobra.Command, in any) error {
	update := in.(*iam.UpdatePolicyInput)
	path, err := cmd.Flags().GetString(policyDocumentFlagName)
	if err != nil {
		return usageError{msg: err.Error()}
	}
	if path == "" {
		return nil
	}
	statements, rerr := readPolicyDocumentFile(path)
	if rerr != nil {
		return rerr
	}
	update.Statements = &statements
	return nil
}
