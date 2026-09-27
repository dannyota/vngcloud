package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/containerregistry"
)

// containerRegistryOps is containerregistry's operation table. list-users
// stays out of it: containerregistry.User is still map-backed, and no live
// row has checked it for a secret-shaped key, so the command is held until
// the SDK types User from a live capture, per the CLI reads design's
// "Secrets" section. Repository is a typed struct, so list-repositories,
// get-repository, create-repository, and delete-repository all ship:
// decoding a response into Repository drops any key the struct does not
// declare, so no unseen or secret-looking key ever reaches CLI output for
// these four.
var containerRegistryOps = []Op[containerregistry.Client]{
	Read[containerregistry.Client, containerregistry.ListRepositoriesInput, containerregistry.ListRepositoriesOutput](
		kebab("ListRepositories"), (*containerregistry.Client).ListRepositories),
	Read[containerregistry.Client, containerregistry.GetRepositoryInput, containerregistry.GetRepositoryOutput](
		kebab("GetRepository"), (*containerregistry.Client).GetRepository),
	Write[containerregistry.Client, containerregistry.CreateRepositoryInput, containerregistry.CreateRepositoryOutput](
		kebab("CreateRepository"), (*containerregistry.Client).CreateRepository),
	Write[containerregistry.Client, containerregistry.DeleteRepositoryInput, containerregistry.DeleteRepositoryOutput](
		kebab("DeleteRepository"), (*containerregistry.Client).DeleteRepository, Destructive()),
}

func newContainerRegistryCmd(e *env) *cobra.Command {
	return Service(e, "containerregistry", "Container registry repositories", containerregistry.New, containerRegistryOps...)
}
