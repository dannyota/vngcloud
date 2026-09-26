package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/monitor"
)

// monitorOps is monitor's operation table. PauseCheck and ResumeCheck are
// Write, per the monitor design, but not Destructive: resume-check undoes
// pause-check, so neither needs --yes. A read-only profile still refuses
// both before any request, since that guard applies to every Write op.
var monitorOps = []Op[monitor.Client]{
	Read[monitor.Client, monitor.ListChecksInput, monitor.ListChecksOutput](
		kebab("ListChecks"), (*monitor.Client).ListChecks),
	Read[monitor.Client, monitor.GetCheckInput, monitor.GetCheckOutput](
		kebab("GetCheck"), (*monitor.Client).GetCheck),
	Write[monitor.Client, monitor.PauseCheckInput, monitor.PauseCheckOutput](
		kebab("PauseCheck"), (*monitor.Client).PauseCheck),
	Write[monitor.Client, monitor.ResumeCheckInput, monitor.ResumeCheckOutput](
		kebab("ResumeCheck"), (*monitor.Client).ResumeCheck),
}

func newMonitorCmd(e *env) *cobra.Command {
	return Service(e, "monitor", "vMonitor synthetic checks", monitor.New, monitorOps...)
}
