package cli

// containerregistry's own doc notes, one paragraph per operation that needs
// one beyond its kind, flags, and example; see docOpNotes in
// gendocs_notes.go, which keys every entry below by "containerregistry
// <op-name>". Kept apart from that file so neither grows past the length
// limit.

// containerRegistryListRepositoriesNote documents that a row's Name carries
// no account prefix, since the flag table shows --name and --access-level as
// plain filters with no hint of that.
const containerRegistryListRepositoriesNote = "The server applies no account prefix: a row's Name is exactly " +
	"the value given to create-repository's own --name."

// containerRegistryGetRepositoryNote documents get-repository's own
// unconfirmed not-found status, since the flag table shows only
// --repository-id with no hint of it.
const containerRegistryGetRepositoryNote = "How a missing repository reads is unconfirmed: the API reference " +
	"documents only a 500 status for this call besides 401, never 404. After any other 5xx, this command lists " +
	"repositories once to check whether the id is still there before deciding NotFound."

// containerRegistryCreateRepositoryNote documents create-repository's always-
// private, never-retried, and confirm-by-read behavior, for guidance the
// flag table cannot show: it lists only --name, --quota-limit-gb, and
// --no-wait, as plain fields.
const containerRegistryCreateRepositoryNote = "Always creates a private repository; there is no --public flag. " +
	"The server applies no account prefix, so the output's Name matches --name exactly. Never retried after a " +
	"failure that may have already reached the server: run list-repositories --name <name> and match the exact " +
	"name before creating again, rather than repeating this command blindly. The create response carries no " +
	"status to wait on; without --no-wait, this command confirms the new repository with a read, polling for up " +
	"to 60 seconds. A timeout, or any other failure during that confirm, is NotSettled, and the repository " +
	"exists, so the create must not be sent again. The server requires --name to be 6 to 20 characters of " +
	"lowercase letters, digits, '_' or '-', starting with a letter or digit, and refuses any other name with 400."

// containerRegistryDeleteRepositoryNote documents delete-repository's
// pre-delete image guard and its post-delete wait, since the flag table
// shows only --repository-id and --no-wait.
const containerRegistryDeleteRepositoryNote = "Refuses, before any request, a repository that still holds " +
	"images (error code RepositoryNotEmpty); delete the images with docker or the console first. Attached " +
	"users are not a guard: they keep existing and lose access to the deleted repository. Without --no-wait, " +
	"waits up to 60 seconds for a read of the repository to report it gone; a timeout, or any other failure " +
	"during that wait, is NotSettled, but this command always reads first, so a rerun is safe either way."

// containerRegistryUserNameNote documents, for list-users and
// list-repository-users, that a row's Name carries no account prefix, the
// same fact containerRegistryListRepositoriesNote documents for a repository
// row, since the flag table shows --name only as a plain filter.
const containerRegistryUserNameNote = "The server applies no account prefix: a row's Name is exactly the value " +
	"given to create-user's own --name."

// containerRegistryCreateUserNote documents create-user's own name rule,
// action names, --secret-file requirement, never-rerun-blindly advice, and
// its two failure classes, UserNotFound and SecretFileFailed: the flag table
// shows --name, --description, and --duration-days as plain fields,
// Permissions only as a Go type settable through --cli-input-json, and no
// --secret-file at all, since it backs no Input field.
const containerRegistryCreateUserNote = "The server requires --name to be 6 to 14 characters of letters, " +
	"digits, '_', or '-', starting with a letter or digit, and refuses any other name with 400. Each " +
	"Permissions[].Actions entry must exactly match one of the server's own action names, read from " +
	"list-permissions: currently \"Pull Images\", \"Push Images\", and \"All\"; an unknown action is refused " +
	"before any request, naming the ones list-permissions did return. Needs --secret-file <path>: the create " +
	"returns the new secret once, and this command writes it only to that file, at mode 0600, never to " +
	"stdout, stderr, --debug, or an error message; the printed SecretKey field always reads \"[redacted]\", " +
	"and a SecretFile field names the path. --secret-file must not already exist, symlink included, checked " +
	"before any request. The create is never retried after a failure that may have already reached the " +
	"server: run list-users --name <name> and delete a stray match before creating again, rather than " +
	"repeating this command blindly, since that match has already lost its secret. When the create itself " +
	"succeeds but a follow-up list cannot confirm the new user by name (error code UserNotFound), the secret " +
	"is still written to --secret-file and the command still exits 1; check list-users --name <name> by hand. " +
	"If writing --secret-file itself fails, the new user is deleted through the SDK and the command exits 1 " +
	"with error code SecretFileFailed; without a confirmed user id (after UserNotFound) or if that delete " +
	"also fails, the message names the user only by --name, for a person to find and delete by hand. Which of " +
	"the printed User's Name or a repository's own Name is docker login's -u value is unconfirmed; the vCR " +
	"writes design recommends trying the user's Name first."

// containerRegistryDeleteUserNote documents delete-user's repeat-delete
// status, since the flag table shows only --user-id.
const containerRegistryDeleteUserNote = "A user holds no data of its own, so there is no pre-delete guard. A " +
	"repeat delete of an already-deleted user returns NotFound."
