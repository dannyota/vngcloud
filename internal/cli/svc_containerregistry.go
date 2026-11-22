package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/internal/transport"
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

func callCreateUser(cmd *cobra.Command, client *containerregistry.Client, ctx context.Context, in any) (any, error) {
	input := in.(*containerregistry.CreateUserInput)
	out, err := client.CreateUser(ctx, input)
	if out == nil {
		return nil, err
	}
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	secret := out.SecretKey.Reveal()
	if !strings.HasSuffix(secret, "\n") {
		secret += "\n"
	}
	if writeErr := writeSecretFile(path, []byte(secret)); writeErr != nil {
		return nil, newCreateUserCleanupFailed()
	}
	return &createUserOutput{CreateUserOutput: *out, SecretFile: transport.RedactErrorText(path, out.SecretKey.Reveal())}, err
}

func newCreateUserCleanupFailed() error {
	return secretFileFailedError{msg: "could not write --secret-file; create ownership is unproven and no cleanup was attempted; reconcile ownership with list-users and other administrators before any deletion"}
}
