package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/backup"
)

var backupOps = []Op[backup.Client]{
	Read[backup.Client, backup.ListBackendsInput, backup.ListBackendsOutput](
		kebab("ListBackends"), (*backup.Client).ListBackends),
	Read[backup.Client, backup.ListPoliciesInput, backup.ListPoliciesOutput](
		kebab("ListPolicies"), (*backup.Client).ListPolicies),
}

func newBackupCmd(e *env) *cobra.Command {
	return Service(e, "backup", "Backup backends and policies", backup.New, backupOps...)
}
