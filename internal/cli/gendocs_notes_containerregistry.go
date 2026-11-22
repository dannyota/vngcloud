package cli

const containerRegistryListRepositoriesNote = "The server applies no account prefix: a row's Name equals the create name. " +
	"A response reporting more pages returns partial output and an explicit incomplete-list error, since " +
	"repository continuation parameters remain unverified. Never use that partial output as a complete inventory."

const containerRegistryGetRepositoryNote = "How a missing repository reads is unconfirmed: the API reference " +
	"documents only a 500 status for this call besides 401, never 404. After any other 5xx, this command lists " +
	"repositories once to check whether the id is still there before deciding NotFound."

const containerRegistryCreateRepositoryNote = "Always creates a private repository; there is no --public flag. " +
	"The server applies no account prefix, so the output's Name matches --name exactly. Never retried after a " +
	"failure that may have already reached the server: run list-repositories --name <name> and match the exact " +
	"name before creating again, rather than repeating this command blindly. The create response carries no " +
	"status to wait on; without --no-wait, this command confirms the new repository with a read, polling for up " +
	"to 60 seconds. A timeout, or any other failure during that confirm, is NotSettled, and the repository " +
	"exists, so the create must not be sent again. The server requires --name to be 6 to 20 characters of " +
	"lowercase letters, digits, '_' or '-', starting with a letter or digit, and refuses any other name with 400."

const containerRegistryDeleteRepositoryNote = "Refuses, before any request, a repository that still holds " +
	"images (error code RepositoryNotEmpty); delete the images with docker or the console first. Attached " +
	"users are not a guard: they keep existing and lose access to the deleted repository. Without --no-wait, " +
	"waits up to 60 seconds for a read of the repository to report it gone; a timeout, or any other failure " +
	"during that wait, is NotSettled, but this command always reads first, so a rerun is safe either way."

const containerRegistryUserNameNote = "The server applies no account prefix: a row's Name is exactly the value " +
	"given to create-user's own --name."

const containerRegistryCreateUserNote = "Needs --secret-file <path>, which must be new, including no existing symlink. " +
	"The server requires names of 6 to 14 letters, digits, '_' or '-', starting with a letter or digit. " +
	"Permissions[].Actions must match list-permissions exactly. The SDK refuses an existing exact name " +
	"before creating and checks every supported user page. Set --duration-days for temporary users. " +
	"The create sends once. Its response carries a secret but no attributable user ID, so even one " +
	"exact-name match is only a candidate. The SDK returns UserNotFound with the secret retained and User " +
	"empty; the CLI saves the secret at mode 0600 and exits 1. Inspect both output and error in SDK callers. " +
	"Never log Reveal(). Dependent reads withhold captures and reject reflected credentials. A name match " +
	"does not authorize automatic cleanup, including after secret-file failure. Reconcile ownership with " +
	"list-users --name and other administrators before any delete or another create. The printed SecretKey " +
	"is redacted. Which name docker login needs remains unverified."

const containerRegistryDeleteUserNote = "A user holds no data of its own, so there is no pre-delete guard. A " +
	"repeat delete of an already-deleted user returns NotFound."
