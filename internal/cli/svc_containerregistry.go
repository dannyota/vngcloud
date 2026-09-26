package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/containerregistry"
)

// containerRegistryOps is containerregistry's operation table. list-users
// stays out of it: containerregistry.User is map-backed and no live row has
// checked it for a secret-shaped key, so the command is held until the SDK
// types User from a live capture, per the CLI reads design's "Secrets"
// section. list-repositories' Repository is also map-backed, but its rows
// are unlikely to hold a secret and redact_maps.go's redactMaps still covers
// any key that looks like one, so it ships now.
var containerRegistryOps = []Op[containerregistry.Client]{
	Read[containerregistry.Client, containerregistry.ListRepositoriesInput, containerregistry.ListRepositoriesOutput](
		kebab("ListRepositories"), (*containerregistry.Client).ListRepositories),
}

func newContainerRegistryCmd(e *env) *cobra.Command {
	return Service(e, "containerregistry", "Container registry repositories", containerregistry.New, containerRegistryOps...)
}
