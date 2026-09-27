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
