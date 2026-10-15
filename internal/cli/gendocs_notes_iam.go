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
	"cdn":     cdnServiceIntroNote,
	"iam":     iamServiceIntroNote,
	"tagging": taggingServiceIntroNote,
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
	"its client secret and needs reset-service-account-secret. A failed confirm read preserves the ID and " +
	"secret but may omit ClientID: recover it with get-service-account --service-account-id <id>, never " +
	"another create. Configure a fresh profile using client_id and stdin-only client_secret, then set " +
	"read_only last; see [Configuration](Configuration.md#service-account-profile)."

// iamResetServiceAccountSecretNote documents reset-service-account-secret's
// --secret-file requirement, its own guard, its no-secret case, and its
// retry advice: the flag table shows no --secret-file at all, since it
// backs no Input field, and gives no hint that the old secret stops working
// for further exchanges. Issued tokens can remain usable.
const iamResetServiceAccountSecretNote = "Needs --secret-file <path>, checked the same way as " +
	"create-service-account: must not already exist, symlink included, checked before any request. The " +
	"previous secret stops working for new exchanges when the reset request lands; if the response then holds no client " +
	"secret at all, or if writing --secret-file fails, there is no way to get it back, so the command exits 1 " +
	"with error code SecretFileFailed naming the service account and telling the caller to run " +
	"reset-service-account-secret again, rather than deleting anything. Never retried after a failure that may " +
	"have already reached the server: no read shows whether the secret changed, so treat the previous secret as " +
	"replaced. Replace the protected credentials, rebuild configs, and restart consumers. Old issued tokens " +
	"and cache entries may survive; reset does not promise immediate token revocation or automatic cache " +
	"purging. A read_only profile needs a trusted editor or provisioner for rotation.\n\n" +
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

const iamCreateUserNote = "Requires exactly one of --password-file <path> (or - for stdin) and " +
	"--secret-file <new-path>. Password cannot be supplied through argv or --cli-input-json. " +
	"--password-length applies only to generation: 8 to 50 characters, default 32, with uppercase, " +
	"lowercase, digits, and @. Input files must be regular and private on Unix; a symlink to one is allowed. " +
	"Input is capped at 4096 bytes, removes one final LF or CRLF, preserves spaces, and rejects controls " +
	"and invalid UTF-8. Stdin refuses a terminal, requires EOF within 30 seconds, honors cancellation, " +
	"and cannot be combined with file:// JSON. Read-only refuses before password input. " +
	"Generated passwords are saved exclusively at mode 0600, including uncertain and " +
	"accepted-but-unconfirmed outcomes. Use a trusted private parent directory; parent symlinks are followed, " +
	"and Unix modes do not guarantee Windows access controls. Password always prints redacted; SecretFile " +
	"appears only after a generated password is saved. Errors print no partial output. " +
	"RequestFailed exits 1 for either create outcome sentinel, even if confirmation failed with 401 or 404. " +
	"An uncertain outcome does not establish password ownership: do not retry; reconcile with list-users --name " +
	"and other administrators before trusting the password, resetting or deleting a candidate. " +
	"SecretFileFailed sends no cleanup request and names manual delete-user --user-id <id> --yes recovery; " +
	"a removal failure warns that a partial file may remain. Process termination before persistence can lose " +
	"the generated password. Creation attaches no rights and configures no MFA. See [IAM](IAM.md#iam-users)."

const iamDeleteUserNote = "Requires --yes. Refuses the caller's own user (SelfChange), root or unknown " +
	"callers (PrivilegedChange), and an unproven IAM origin or mismatched ID (RequestFailed). " +
	"Guard refusals may read but send no DELETE. No privilege or attachment guard applies; server 409 remains " +
	"Conflict, without automatic detach. DELETE keeps SDK retries; a final 404 remains NotFound. " +
	"After an uncertain delete, inspect the same ID with get-user. See [IAM](IAM.md#iam-users)."

const iamListUsersNote = "--name adds the server's observed case-insensitive substring filter; " +
	"compare exact usernames and IDs before reconciliation. A name match proves current state only, " +
	"never ownership of a create or its password. Paging remains one zero-based page, with default size 10000."

const iamResetUserPasswordNote = "Requires --yes, --user-id, and exactly one of --password-file <path> " +
	"(or - for stdin) and --secret-file <new-path>. Password cannot be supplied through argv or JSON. " +
	"--password-length applies only to generation: 8 to 50, default 32, with uppercase, lowercase, digits, " +
	"and @. Uses create-user's private regular-file and bounded stdin rules, including the 30-second " +
	"deadline and refusal to share stdin with file:// JSON. Read-only refuses before password input. " +
	"Refuses self (SelfChange), root or unknown callers and protected users (PrivilegedChange), and " +
	"unproven IAM origin or mismatched target IDs (RequestFailed). Protection includes direct and group IAM " +
	"write rights. Guard reads require permission and fail closed; checks and writes are not atomic. " +
	"Sends at most one POST, with no confirmation, automatic sign-in, rollback, or cleanup. Accepted reset " +
	"has no undo; treat the old password as unusable after acceptance and potentially unusable after an " +
	"uncertain send. Session and token revocation remain unknown. Generated passwords are saved exclusively " +
	"at mode 0600, including accepted response failures (NotSettled) and uncertain sends (RequestFailed). " +
	"Both outcomes exit 1 before nested 401/404 errors. Generated success prints redacted Password and the " +
	"saved SecretFile; supplied mode prints neither. Errors print no partial object and report whether the " +
	"password was saved. SecretFileFailed retains outcome identity, sends no further request, and names " +
	"manual reset-user-password --user-id <user-id> --secret-file <fresh-private-path> --yes recovery. " +
	"Coordinate a deliberate new reset with the user and other administrators. Removal failure warns that " +
	"a partial password file may remain. Use a trusted private directory; process termination before " +
	"persistence can lose the generated password. See [IAM](IAM.md#reset-user-password)."

func init() {
	docOpNotes["iam reset-user-password"] = iamResetUserPasswordNote
	docExampleOverride["iam reset-user-password"] = "vngcloud iam reset-user-password --user-id <user-id> " +
		"--secret-file <private-directory>/password --yes"
}

const iamUserMFANote = "Requires --yes, --user-id, and --method google. Only Google factor administration " +
	"is supported; enable requires an existing verified factor, even for a no-op. Factor enrolment and " +
	"possession verification stay external: this command accepts no password, seed, code, or secret file. " +
	"Refuses self (SelfChange), root or unknown callers and protected users (PrivilegedChange), including " +
	"direct and group IAM write rights. An unproven IAM origin, mismatched ID, unknown factor state, or " +
	"unverified enable fails closed. Guard reads require permission and run before a no-op; read-only " +
	"refuses before any request. Checks and writes are not atomic. After guards, matching state returns " +
	"Changed=false without a mutation. Otherwise sends at most one bodyless POST and makes one logical " +
	"confirmation read, without replay, rollback, sign-in, profile edits, or cache repair. Success prints " +
	"Method, nullable Enabled, and Changed. Changed means a 204 was observed. NotSettled means acceptance " +
	"was observed but response handling or confirmation failed; RequestFailed covers an uncertain send. " +
	"Both outcomes exit 1 ahead of nested 401/404 errors and print no partial object. Inspect get-user and " +
	"coordinate with the user and other administrators before deliberately repeating a change. Disabling " +
	"MFA creates takeover risk; enabling a factor the user cannot access creates lockout risk. Account-wide " +
	"MFA effects and session or token revocation remain unknown. See [IAM](IAM.md#google-mfa)."

func init() {
	for _, op := range []string{"enable-user-mfa", "disable-user-mfa"} {
		docOpNotes["iam "+op] = iamUserMFANote
		docExampleOverride["iam "+op] = "vngcloud iam " + op + " --user-id <user-id> --method google --yes"
	}
	docOpNotes["iam get-user"] = "Returns nullable MFA factor state under User.MFA, with Email and " +
		"GoogleAuthenticator factors and nullable Verified and Enabled booleans. Missing or null means unknown, " +
		"not false. Contact details and factor secrets are omitted. See [IAM](IAM.md#google-mfa)."
}
