package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/containerregistry"
)

// containerRegistryOps is containerregistry's operation table. Repository and
// User are both typed structs, so decoding a response into either drops any
// key it does not declare and no unseen or secret-looking key ever reaches
// CLI output; this is what let list-users, list-repository-users, and
// list-permissions ship alongside list-repositories and get-repository.
// create-user is built by hand below, like compute's create-ssh-key, since it
// needs --secret-file, a flag no Input field derives.
var containerRegistryOps = []Op[containerregistry.Client]{
	Read[containerregistry.Client, containerregistry.ListRepositoriesInput, containerregistry.ListRepositoriesOutput](
		kebab("ListRepositories"), (*containerregistry.Client).ListRepositories),
	Read[containerregistry.Client, containerregistry.GetRepositoryInput, containerregistry.GetRepositoryOutput](
		kebab("GetRepository"), (*containerregistry.Client).GetRepository),
	Write[containerregistry.Client, containerregistry.CreateRepositoryInput, containerregistry.CreateRepositoryOutput](
		kebab("CreateRepository"), (*containerregistry.Client).CreateRepository),
	Write[containerregistry.Client, containerregistry.DeleteRepositoryInput, containerregistry.DeleteRepositoryOutput](
		kebab("DeleteRepository"), (*containerregistry.Client).DeleteRepository, Destructive()),
	Read[containerregistry.Client, containerregistry.ListUsersInput, containerregistry.ListUsersOutput](
		kebab("ListUsers"), (*containerregistry.Client).ListUsers),
	Read[containerregistry.Client, containerregistry.ListRepositoryUsersInput, containerregistry.ListRepositoryUsersOutput](
		kebab("ListRepositoryUsers"), (*containerregistry.Client).ListRepositoryUsers),
	Read[containerregistry.Client, containerregistry.ListPermissionsInput, containerregistry.ListPermissionsOutput](
		kebab("ListPermissions"), (*containerregistry.Client).ListPermissions),
	createUserOp(),
	Write[containerregistry.Client, containerregistry.DeleteUserInput, containerregistry.DeleteUserOutput](
		kebab("DeleteUser"), (*containerregistry.Client).DeleteUser, Destructive()),
}

func newContainerRegistryCmd(e *env) *cobra.Command {
	return Service(e, "containerregistry", "Container registry repositories", containerregistry.New, containerRegistryOps...)
}

// createUserOutput is create-user's own JSON shape: containerregistry's
// CreateUserOutput, whose SecretKey already prints "[redacted]" through
// vngcloud.Secret's own encoding, plus the path --secret-file wrote the
// secret to. The embedding flattens User and SecretKey to the top level, the
// same shape CreateUserOutput's own fields would have on their own.
type createUserOutput struct {
	containerregistry.CreateUserOutput
	SecretFile string
}

// createUserOp builds create-user's Op directly, rather than through Write,
// for the same two reasons createSSHKeyOp (svc_compute.go) does: --secret-file
// has no backing Input field, and a successful call's own Output type
// (createUserOutput above) differs from containerregistry.CreateUserOutput,
// the type Write's generic method parameter would otherwise fix it to.
// methodName is still set to "CreateUser" by hand, so checkOpName's
// name-matches-the-SDK-method rule holds exactly as it does for every op
// Write and Read build from a real method value.
func createUserOp() Op[containerregistry.Client] {
	return Op[containerregistry.Client]{
		name:       kebab("CreateUser"),
		methodName: "CreateUser",
		kind:       kindWrite,
		guard:      guardSecretFilePath,
		extraFlags: registerSecretFileFlag,
		newInput:   func() any { return new(containerregistry.CreateUserInput) },
		newOutput:  func() any { return new(createUserOutput) },
		call:       callCreateUser,
	}
}

// callCreateUser runs the real create, then writes the new secret to
// --secret-file, which guardSecretFilePath already checked before this call
// ever ran. Per the vCR writes design's create-user section, the file gets
// the secret as returned plus one trailing newline.
//
// CreateUser's own Output is non-nil both on a plain success and when the
// create itself succeeded but the post-create list could not confirm the new
// user (err wraps containerregistry.ErrUserNotFound): either way the secret
// has already been issued and must still reach --secret-file, and the
// original err, if any, is returned unchanged once that write is done. Any
// other error leaves Output nil, so nothing was ever received to write, and
// that error is returned as is.
func callCreateUser(cmd *cobra.Command, client *containerregistry.Client, ctx context.Context, in any) (any, error) {
	input := in.(*containerregistry.CreateUserInput)
	out, err := client.CreateUser(ctx, input)
	if out == nil {
		return nil, err
	}
	// guardSecretFilePath ran before any request and already required this
	// flag and checked its path, so the only new failure possible here is
	// the actual write.
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	secret := out.SecretKey.Reveal()
	if !strings.HasSuffix(secret, "\n") {
		secret += "\n"
	}
	if writeErr := writeSecretFile(path, []byte(secret)); writeErr != nil {
		return nil, cleanUpAfterFailedUserSecretFile(ctx, client, input.Name, out.User.ID, writeErr)
	}
	return &createUserOutput{CreateUserOutput: *out, SecretFile: path}, err
}

// cleanUpAfterFailedUserSecretFile reports create-user's SecretFileFailed
// case, per the vCR writes design's create-user section point 3: when
// userID is known (a plain create success), it deletes the new user through
// the SDK and, once that delete succeeds (a NotFound from it counts as
// succeeding, since the user is gone either way), names the user by that id,
// the same shape callCreateSSHKey (svc_compute.go) already reports for a
// compute SSH key. Without a known id, after containerregistry.ErrUserNotFound
// left User unfilled, or when the delete itself fails, the design calls for
// naming the user only by the --name the caller gave, not an id, so a person
// can find and delete it by hand with list-users --name <name>; both of those
// cases share newCreateUserCleanupFailed below. The cleanup delete runs on a
// context detached from ctx (context.WithoutCancel, with the same bound
// callCreateSSHKey's own cleanup delete uses), so a canceled command still
// cleans up the orphaned user.
func cleanUpAfterFailedUserSecretFile(ctx context.Context, client *containerregistry.Client, name, userID string, writeErr error) error {
	if userID == "" {
		return newCreateUserCleanupFailed(name)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretFileCleanupDeleteTimeout)
	defer cancel()
	if _, delErr := client.DeleteUser(cleanupCtx, &containerregistry.DeleteUserInput{UserID: userID}); delErr != nil && !vngcloud.IsNotFound(delErr) {
		return newCreateUserCleanupFailed(name)
	}
	return newSecretFileWriteFailed("user", userID, writeErr)
}

// newCreateUserCleanupFailed reports a create-user --secret-file write
// failure whose orphaned user cannot be deleted automatically, either
// because CreateUser's own post-create lookup never confirmed its id (an
// error wrapping containerregistry.ErrUserNotFound) or because the cleanup
// delete this command tried itself failed. Per the vCR writes design, both
// cases name the user only by the --name the caller gave, not an id, so a
// person can find and delete it with list-users --name <name>.
func newCreateUserCleanupFailed(name string) error {
	return secretFileFailedError{msg: fmt.Sprintf(
		"could not write --%s: user %q exists and must be deleted; check with list-users --name %q",
		secretFileFlagName, name, name)}
}
