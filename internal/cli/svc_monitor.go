package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/monitor"
)

// monitorOps is monitor's operation table. PauseCheck and ResumeCheck are
// Write, per the monitor design, but not Destructive: resume-check undoes
// pause-check, so neither needs --yes. CreateCheck is Write but not
// Destructive either. DeleteCheck is Write and Destructive: a deleted check
// and its history cannot be restored by one more command, so it needs
// --yes. A read-only profile refuses every Write op, all four, before any
// request. CreateCheckInput's Locations, Headers, Query, and Assertions
// fields have no flag-settable type, so they reach the command only through
// --cli-input-json; every other CreateCheckInput field gets a flag from
// flags.go's reflection.
//
// ListChannels and GetChannel carry Redact: the vMonitor Alerts design's CLI
// redaction rule applies to every reader of a Channel, and there is no flag
// to turn it off. ListChannelTypes returns no Channel, so it needs none.
var monitorOps = []Op[monitor.Client]{
	Read[monitor.Client, monitor.ListChecksInput, monitor.ListChecksOutput](
		kebab("ListChecks"), (*monitor.Client).ListChecks),
	Read[monitor.Client, monitor.GetCheckInput, monitor.GetCheckOutput](
		kebab("GetCheck"), (*monitor.Client).GetCheck),
	Write[monitor.Client, monitor.PauseCheckInput, monitor.PauseCheckOutput](
		kebab("PauseCheck"), (*monitor.Client).PauseCheck),
	Write[monitor.Client, monitor.ResumeCheckInput, monitor.ResumeCheckOutput](
		kebab("ResumeCheck"), (*monitor.Client).ResumeCheck),
	Write[monitor.Client, monitor.CreateCheckInput, monitor.CreateCheckOutput](
		kebab("CreateCheck"), (*monitor.Client).CreateCheck),
	Write[monitor.Client, monitor.DeleteCheckInput, monitor.DeleteCheckOutput](
		kebab("DeleteCheck"), (*monitor.Client).DeleteCheck, Destructive()),
	Read[monitor.Client, monitor.ListLocationsInput, monitor.ListLocationsOutput](
		kebab("ListLocations"), (*monitor.Client).ListLocations),
	Read[monitor.Client, monitor.ListChannelTypesInput, monitor.ListChannelTypesOutput](
		kebab("ListChannelTypes"), (*monitor.Client).ListChannelTypes),
	Read[monitor.Client, monitor.ListChannelsInput, monitor.ListChannelsOutput](
		kebab("ListChannels"), (*monitor.Client).ListChannels,
		Redact(func(out *monitor.ListChannelsOutput) {
			for i := range out.Items {
				out.Items[i] = redactChannel(out.Items[i])
			}
		})),
	Read[monitor.Client, monitor.GetChannelInput, monitor.GetChannelOutput](
		kebab("GetChannel"), (*monitor.Client).GetChannel,
		Redact(func(out *monitor.GetChannelOutput) {
			out.Channel = redactChannel(out.Channel)
		})),
}

func newMonitorCmd(e *env) *cobra.Command {
	return Service(e, "monitor", "vMonitor synthetic checks", monitor.New, monitorOps...)
}
