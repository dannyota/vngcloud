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
//
// CreateChannel and UpdateChannel carry WriteRedact for the same reason, and
// each carries a Guard that refuses a literal --address: a Webhook or Slack
// channel's address, and a Webhook channel's header values, can hold a
// secret, and argv reaches ps and shell history, exactly what configure
// refuses a literal password for. DeleteChannel is Write and Destructive: a
// deleted channel cannot be restored by one more command, so it needs
// --yes. A read-only profile refuses all three, before any request, the
// same as every other Write op here.
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
	Write[monitor.Client, monitor.CreateChannelInput, monitor.CreateChannelOutput](
		kebab("CreateChannel"), (*monitor.Client).CreateChannel,
		Guard(refuseLiteralCreateChannelAddress),
		WriteRedact(func(out *monitor.CreateChannelOutput) {
			out.Channel = redactChannel(out.Channel)
		})),
	Write[monitor.Client, monitor.UpdateChannelInput, monitor.UpdateChannelOutput](
		kebab("UpdateChannel"), (*monitor.Client).UpdateChannel,
		Guard(refuseLiteralUpdateChannelAddress),
		WriteRedact(func(out *monitor.UpdateChannelOutput) {
			out.Channel = redactChannel(out.Channel)
		})),
	Write[monitor.Client, monitor.DeleteChannelInput, monitor.DeleteChannelOutput](
		kebab("DeleteChannel"), (*monitor.Client).DeleteChannel, Destructive()),
}

// refuseLiteralCreateChannelAddress refuses a literal --address flag on
// create-channel when the merged Input's Type is Webhook or Slack: either
// channel's address can carry a bearer token. M2's own CreateChannel already
// refuses every type but Webhook before any request (see
// monitor.CreateChannel), so only Webhook can be created today; Slack is
// named here too, per the monitor design, so this guard needs no change once
// OTP channels can be created. cmd.Flags().Changed reports only a flag the
// operator actually set, so an Address that reached the Input through
// --cli-input-json alone never triggers this.
func refuseLiteralCreateChannelAddress(cmd *cobra.Command, in any) error {
	if !cmd.Flags().Changed("address") {
		return nil
	}
	create, ok := in.(*monitor.CreateChannelInput)
	if !ok || (create.Type != monitor.ChannelTypeWebhook && create.Type != monitor.ChannelTypeSlack) {
		return nil
	}
	return newUsageError(
		"--address for a %s channel can hold a secret; pass it only through --cli-input-json file://channel.json",
		create.Type)
}

// refuseLiteralUpdateChannelAddress refuses every literal --address flag on
// update-channel. UpdateChannelInput carries no Type field, since the API
// has no way to change a channel's type, so this guard cannot tell a
// Webhook or Slack channel (whose address the monitor design requires
// --cli-input-json for) apart from an Email, SMS, or Telegram one without a
// request of its own, and a Guard must refuse before any request. Refusing
// every type costs nothing today: UpdateChannelInput carries no OTP fields
// yet, so updating an Email, SMS, Telegram, or Slack channel's Address
// always fails on the server for lacking one, and only a Webhook channel's
// Address update can succeed, which is exactly the case the design already
// requires --cli-input-json for.
func refuseLiteralUpdateChannelAddress(cmd *cobra.Command, _ any) error {
	if !cmd.Flags().Changed("address") {
		return nil
	}
	return newUsageError("--address can hold a secret; pass it only through --cli-input-json file://channel.json")
}

func newMonitorCmd(e *env) *cobra.Command {
	return Service(e, "monitor", "vMonitor synthetic checks", monitor.New, monitorOps...)
}
