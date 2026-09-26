package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/project"
)

// projectOps is project's operation table. ListProjectsInput.Region only
// filters the result: the global --region flag already picks the regional
// host, and the SDK filters by the configured region when the field is
// empty. Its mechanical flag name ("--region") would collide with that
// global flag, so NoFlag keeps it settable only through --cli-input-json.
var projectOps = []Op[project.Client]{
	Read[project.Client, project.ListProjectsInput, project.ListProjectsOutput](
		kebab("ListProjects"), (*project.Client).ListProjects, NoFlag("Region")),
}

func newProjectCmd(e *env) *cobra.Command {
	return Service(e, "project", "Projects in the configured region", project.New, projectOps...)
}
