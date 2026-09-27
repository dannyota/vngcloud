package cli

// iamServiceIntroNote documents the IAM design's own security
// recommendation for the whole service, since no single operation's flag
// table can state an account-wide setting: the guards documented below stop
// mistakes by a caller that already holds IAM write rights, not a caller
// the server itself can refuse outright.
const iamServiceIntroNote = "The guards documented below stop mistakes, not a caller that already holds IAM " +
	"write rights: give agent profiles an IAM user without IAM write rights, and turn on read_only where they " +
	"only read, so the server itself refuses what a guard would. A caller whose own type is a service account " +
	"is refused on every write that targets an existing service account (update-service-account, " +
	"delete-service-account, and reset-service-account-secret), since its own identity cannot be confirmed " +
	"against a target's ID or ClientID."

// docServiceIntro gives one service's page a paragraph of prose before its
// operations, keyed by service name, for a fact that holds across the whole
// service rather than one operation.
var docServiceIntro = map[string]string{
	"iam": iamServiceIntroNote,
}

// iamServiceAccountGuardNote documents the guard every service account
// write but create runs before any request: the flag table shows only IDs
// and plain fields, with no hint that the target's own rights, or the
// caller's own type, can refuse the command outright.
const iamServiceAccountGuardNote = "Refuses, before any request, with error code SelfChange when the service " +
	"account is the caller's own token, or the caller's own type is a service account (every service account " +
	"target is refused then, not only the caller's own ID), and PrivilegedChange when the target holds a " +
	"policy that grants an IAM write right; see the IAM design's guard rules. Neither guard has a flag or " +
	"Input field that turns it off."

// iamCreateServiceAccountNote documents create-service-account's
// --secret-file requirement, its cleanup, no-secret, and unconfirmed-read
// cases, and its retry advice: the flag table shows no --secret-file at
// all, since it backs no Input field, and gives no hint of any of this.
const iamCreateServiceAccountNote = "Needs --secret-file <path>: the client secret is returned once, and this " +
	"command writes it only to that file, at mode 0600, never to stdout, stderr, --debug, or an error message; " +
	"the printed ClientSecret field always reads \"[redacted]\", and a SecretFile field names the path. " +
	"--secret-file must not already exist, symlink included, checked before any request. If the create response " +
	"holds no client secret at all, the new service account is kept, no file is written, and the command exits " +
	"1 with error code SecretFileFailed naming reset-service-account-secret. If the read the SDK makes after " +
	"the create to confirm the new account fails, --secret-file is still written from the create response's " +
	"own secret and the account is kept, but the command exits 1 naming list-service-accounts, since the " +
	"account's other fields could not be confirmed. If writing --secret-file itself fails after a secret was " +
	"returned, the new service account is deleted through the SDK and the command exits 1 with " +
	"SecretFileFailed; if that delete also fails, the message names the service account only by its ID. Never " +
	"retried after a failure that may have already reached the server: list service accounts by --name before " +
	"creating it again rather than repeating this command; a service account found that way has already lost " +
	"its client secret and needs reset-service-account-secret."

// iamResetServiceAccountSecretNote documents reset-service-account-secret's
// --secret-file requirement, its own guard, its no-secret case, and its
// retry advice: the flag table shows no --secret-file at all, since it
// backs no Input field, and gives no hint that the old secret stops working
// immediately.
const iamResetServiceAccountSecretNote = "Needs --secret-file <path>, checked the same way as " +
	"create-service-account: must not already exist, symlink included, checked before any request. The " +
	"previous secret stops working the moment the reset request lands; if the response then holds no client " +
	"secret at all, or if writing --secret-file fails, there is no way to get it back, so the command exits 1 " +
	"with error code SecretFileFailed naming the service account and telling the caller to run " +
	"reset-service-account-secret again, rather than deleting anything. Never retried after a failure that may " +
	"have already reached the server: no read shows whether the secret changed, so treat the previous secret as " +
	"revoked and reset again if it still works.\n\n" +
	iamServiceAccountGuardNote

// iamUpdateServiceAccountNote and iamDeleteServiceAccountNote document
// update-service-account's and delete-service-account's own guard, which the
// flag table cannot show at all.
var iamUpdateServiceAccountNote = iamServiceAccountGuardNote
var iamDeleteServiceAccountNote = iamServiceAccountGuardNote

// iamPolicyDocumentNote documents create-policy's and update-policy's own
// --document-file format: the flag table lists Statements only as "(via
// --cli-input-json only)", with no hint that --document-file is the other,
// more usual way to set it, or of the shape either one must decode into.
const iamPolicyDocumentNote = "Statements has no flag of its own; set it with --document-file <path>, a JSON " +
	"object in the console's own form, or --cli-input-json (Statements, its exact Go field name). " +
	"--document-file wins when both are given. The file must be a regular file of at most 64 KiB, opened " +
	"without following a FIFO or other special file, and is decoded with unknown fields refused: an AWS-style " +
	"document (top-level Version and Statement, or a statement's own Action and Resource) exits 2 before any " +
	"request instead of reaching the server as an empty policy. Keys match without regard to case, so " +
	"get-policy's own Go field names (Statements, Effect, Actions, Resources) decode the same way as the " +
	"console's lower-case ones. Example, granting a read-only vServer action:\n\n" +
	"```json\n{\"statements\": [{\"effect\": \"allow\", \"actions\": [\"vserver:ListServers\"], " +
	"\"resources\": [\"*\"]}]}\n```"

// iamCreatePolicyNote documents create-policy's own guard, its document
// format, and its retry advice: the flag table shows only --name and
// --description as plain fields, with no hint of any of this.
const iamCreatePolicyNote = "Refuses, before any request, with error code PrivilegedChange when the statements " +
	"grant an IAM write action; see the IAM design's guard rules. Never retried after a failure that may have " +
	"already reached the server: list policies by --name before creating it again rather than repeating this " +
	"command.\n\n" + iamPolicyDocumentNote

// iamUpdatePolicyNote documents update-policy's own guard and document
// format: the flag table shows only --policy-id, --name, and --description
// as plain fields, with no hint of either.
const iamUpdatePolicyNote = "Refuses, before any request, with error code ManagedPolicy when the policy is " +
	"managed, and PrivilegedChange when its current or proposed statements grant an IAM write action, or it is " +
	"attached to a protected principal or group; see the IAM design's guard rules. Sends a full PUT, filling any " +
	"field left unset (Name, Description, Statements) from the policy's own current state first, so leaving out " +
	"--document-file keeps the current statements rather than clearing them.\n\n" + iamPolicyDocumentNote

// iamDeletePolicyNote documents delete-policy's own guard, which the flag
// table cannot show at all.
const iamDeletePolicyNote = "Refuses, before any request, with error code ManagedPolicy when the policy is " +
	"managed, and ResourceInUse when it is attached to a group, an IAM user, or a service account; see the IAM " +
	"design's guard rules."

// iamServiceAccountPolicyAttachGuardNote documents the guard
// attach-service-account-policy and detach-service-account-policy both run
// before any request: the flag table shows only two IDs, with no hint that
// either the caller's own type or the policy's or the target's own rights
// can refuse the command outright.
const iamServiceAccountPolicyAttachGuardNote = "Refuses, before any request, with error code SelfChange when the " +
	"caller's own type is a service account, and PrivilegedChange when the policy grants an IAM write action or " +
	"the target service account already holds one; see the IAM design's guard rules. Neither guard has a flag " +
	"or Input field that turns it off."

var iamAttachServiceAccountPolicyNote = iamServiceAccountPolicyAttachGuardNote
var iamDetachServiceAccountPolicyNote = iamServiceAccountPolicyAttachGuardNote

// iamCreateGroupNote documents create-group's own retry advice: creating a
// group with no initial members or policies has no guard beyond read-only,
// since it changes no one's rights, but the flag table gives no hint that a
// failed create should never simply be run again.
const iamCreateGroupNote = "Changes no one's rights and has no guard beyond read-only: a new group starts with " +
	"no members and no attached policy, added only through add-user-to-group and attach-group-policy. Never " +
	"retried after a failure that may have already reached the server: list groups by --name before creating it " +
	"again rather than repeating this command."

// iamUpdateGroupNote documents update-group's own no-guard rule and its
// fill-from-read behavior, which the flag table cannot show.
const iamUpdateGroupNote = "Changes no one's rights and has no guard beyond read-only. Sends a full PATCH, " +
	"reading the group first to fill Name when left unset, since the request requires a name either way; " +
	"Description is sent only when set, and left as is otherwise."

// iamDeleteGroupNote documents delete-group's own guard, which the flag
// table cannot show at all.
const iamDeleteGroupNote = "Refuses, before any request, with error code ResourceInUse when the group has a " +
	"member or an attached policy, protected or not: removing it would silently take rights from whatever it " +
	"is attached to. See the IAM design's guard rules."

// iamGroupMembershipGuardNote documents the guard add-user-to-group and
// remove-user-from-group both run before any request: the flag table shows
// only two IDs, with no hint that either the user's or the group's own
// rights can refuse the command outright.
const iamGroupMembershipGuardNote = "Refuses, before any request, with error code SelfChange when the user is " +
	"the caller or the group is one the caller already belongs to, and PrivilegedChange when the user or the " +
	"group otherwise holds a privileged policy; see the IAM design's guard rules. Neither guard has a flag or " +
	"Input field that turns it off."

var iamAddUserToGroupNote = iamGroupMembershipGuardNote
var iamRemoveUserFromGroupNote = iamGroupMembershipGuardNote

// iamGroupPolicyAttachGuardNote documents the guard attach-group-policy and
// detach-group-policy both run before any request: the flag table shows only
// two IDs, with no hint that either the group's own rights or the policy's
// can refuse the command outright.
const iamGroupPolicyAttachGuardNote = "Refuses, before any request, with error code SelfChange when the group is " +
	"one the caller already belongs to, and PrivilegedChange when the policy grants an IAM write action or the " +
	"group otherwise holds one; see the IAM design's guard rules. Neither guard has a flag or Input field that " +
	"turns it off."

var iamAttachGroupPolicyNote = iamGroupPolicyAttachGuardNote
var iamDetachGroupPolicyNote = iamGroupPolicyAttachGuardNote

// iamUserPolicyAttachGuardNote documents the guard attach-user-policy and
// detach-user-policy both run before any request: the flag table shows only
// two IDs, with no hint that either the user's own rights or the policy's
// can refuse the command outright.
const iamUserPolicyAttachGuardNote = "Refuses, before any request, with error code SelfChange when the user is " +
	"the caller, and PrivilegedChange when the policy grants an IAM write action or the user otherwise holds " +
	"one, directly or through a group; see the IAM design's guard rules. Neither guard has a flag or Input " +
	"field that turns it off."

var iamAttachUserPolicyNote = iamUserPolicyAttachGuardNote
var iamDetachUserPolicyNote = iamUserPolicyAttachGuardNote
