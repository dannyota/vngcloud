package cli

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/monitor"
)

// monitorOps is monitor's operation table. PauseCheck and ResumeCheck are
// Write, per the monitor design, but not Destructive: resume-check undoes
// pause-check, so neither needs --yes. CreateCheck and UpdateCheck are Write
// but not Destructive either. DeleteCheck is Write and Destructive: a
// deleted check and its history cannot be restored by one more command, so
// it needs --yes. A read-only profile refuses every Write op, all five,
// before any request. CreateCheckInput's Locations, Headers, Query, and
// Assertions fields have no flag-settable type, so they reach the command
// only through --cli-input-json; every other CreateCheckInput field gets a
// flag from flags.go's reflection. UpdateCheckInput's Name, URL, Method,
// Body, Timeout, TestFrequency, Tests, and FailedLocations are pointers to a
// flag-settable type, so each still gets a flag the same way; its Headers,
// Query, Locations, Assertions, and Notifications are pointers to a map,
// slice, or struct, so, like CreateCheckInput's own Locations, Headers,
// Query, and Assertions, they reach the command only through
// --cli-input-json. UpdateCheck needs no Guard: unlike a channel's Address, a
// check's request headers carry no CLI-enforced secrecy rule (the monitor
// design prints them in full), so the flags.go reflection alone is enough.
//
// ListChannels and GetChannel carry Redact: the vMonitor Alerts design's CLI
// redaction rule applies to every reader of a Channel, and there is no flag
// to turn it off. ListChannelTypes returns no Channel, so it needs none.
//
// CreateChannel and UpdateChannel carry WriteRedact for the same reason, and
// each carries a Guard that refuses a literal --address, or an inline
// --cli-input-json value that sets Address or Headers: create refuses
// Address for every Type but Email, SMS, and Telegram, since any other
// type's address can carry a secret, and refuses Headers for every Type,
// since a Webhook channel's header values can hold one and Headers has no
// flag of its own; update refuses both fields unconditionally, since
// UpdateChannelInput carries no Type for the guard to check. Both argv and
// an inline --cli-input-json value reach ps and shell history, exactly what
// configure refuses a literal password for; a file:// value does not.
// DeleteChannel is Write and Destructive: a deleted channel cannot be
// restored by one more command, so it needs --yes. A read-only profile
// refuses all three, before any request, the same as every other Write op
// here.
//
// ListLogProjects, GetLogProject, ListLogProjectClasses, and
// QuoteCreateLogProject carry no Guard or Redact either: neither LogProject
// nor LogProjectClass holds a secret the monitor design's redaction rule
// covers.
//
// ListAlarms and GetAlarm carry no Guard or Redact: an Alarm holds no
// secret. GetAlarm needs no special handling for an unknown ID either: the
// API answers one with a 500, which vngcloud.IsNotFound never matches, so
// exitCode's default case already returns 1 rather than the 4 a real 404
// gets; docOpNotes states this for the wiki page since the flag table
// cannot show it.
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
	Write[monitor.Client, monitor.UpdateCheckInput, monitor.UpdateCheckOutput](
		kebab("UpdateCheck"), (*monitor.Client).UpdateCheck),
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
	Read[monitor.Client, monitor.ListLogProjectsInput, monitor.ListLogProjectsOutput](
		kebab("ListLogProjects"), (*monitor.Client).ListLogProjects),
	Read[monitor.Client, monitor.GetLogProjectInput, monitor.GetLogProjectOutput](
		kebab("GetLogProject"), (*monitor.Client).GetLogProject),
	Read[monitor.Client, monitor.ListLogProjectClassesInput, monitor.ListLogProjectClassesOutput](
		kebab("ListLogProjectClasses"), (*monitor.Client).ListLogProjectClasses),
	// QuoteCreateLogProject shares CreateLogProjectInput with the
	// create-log-project command a later release adds. MaxPrice and NoWait
	// only govern that create's own price ceiling and wait, not this read,
	// which neither orders nor waits, so NoFlag keeps both settable only
	// through --cli-input-json until create-log-project ships.
	Read[monitor.Client, monitor.CreateLogProjectInput, monitor.QuoteCreateLogProjectOutput](
		kebab("QuoteCreateLogProject"), (*monitor.Client).QuoteCreateLogProject,
		NoFlag("MaxPrice", "NoWait")),
	Read[monitor.Client, monitor.ListAlarmsInput, monitor.ListAlarmsOutput](
		kebab("ListAlarms"), (*monitor.Client).ListAlarms),
	Read[monitor.Client, monitor.GetAlarmInput, monitor.GetAlarmOutput](
		kebab("GetAlarm"), (*monitor.Client).GetAlarm),
}

// literalCLIInputJSONFields returns the top-level key set of cmd's
// --cli-input-json flag when that flag holds a JSON object directly, rather
// than a file:// path: those bytes sit on argv, and so in ps and shell
// history, exactly like a literal flag's value does. A file:// value, or no
// --cli-input-json at all, returns nil, since neither one's content ever
// reaches argv. applyCLIInputJSON has already parsed and validated the same
// value by the time any Guard runs, so a parse error here cannot happen for
// a command that got this far; it is treated as no fields rather than
// adding a second, unreachable error return.
func literalCLIInputJSONFields(cmd *cobra.Command) map[string]bool {
	raw, err := cmd.Flags().GetString("cli-input-json")
	if err != nil || raw == "" || strings.HasPrefix(raw, "file://") {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil
	}
	names := make(map[string]bool, len(fields))
	for key := range fields {
		names[key] = true
	}
	return names
}

// channelAddressTypeAllowsLiteral reports whether typ may carry its Address
// as a literal --address flag or an inline --cli-input-json value, per the
// monitor design's CLI redaction rule (see redactChannel): only Email, SMS,
// and Telegram carry an Address that is personal data rather than a secret.
// Every other type, known or not, including a differently-cased spelling of
// Webhook or Slack, is denied by default: strings.EqualFold matches
// redactChannel's rule regardless of how the operator or an inline JSON
// value happened to case Type, rather than deny by a list of the types
// known to carry a secret today.
func channelAddressTypeAllowsLiteral(typ string) bool {
	return strings.EqualFold(typ, monitor.ChannelTypeEmail) ||
		strings.EqualFold(typ, monitor.ChannelTypeSMS) ||
		strings.EqualFold(typ, monitor.ChannelTypeTelegram)
}

// refuseLiteralCreateChannelAddress refuses a literal --address flag, or an
// inline --cli-input-json value that sets Address, on create-channel when
// the merged Input's Type does not pass channelAddressTypeAllowsLiteral:
// every type but Email, SMS, and Telegram can carry a secret in its
// address. M2's own CreateChannel already refuses every type but Webhook
// before any request (see monitor.CreateChannel), so only Webhook can be
// created today; every other type is denied here too, per the monitor
// design, so this guard needs no change once OTP channels can be created.
// It also refuses an inline --cli-input-json value that sets Headers, for
// every Type: Headers has no flag of its own (see flagSpecsFor), so the
// only way it ever reaches argv is through --cli-input-json, and any
// Webhook channel's header value can hold a secret. cmd.Flags().Changed
// reports only an --address flag the operator actually set;
// literalCLIInputJSONFields reports Address or Headers from an inline
// --cli-input-json value, but never from a file:// one, since a file's
// content never reaches argv.
func refuseLiteralCreateChannelAddress(cmd *cobra.Command, in any) error {
	create, ok := in.(*monitor.CreateChannelInput)
	if !ok {
		return nil
	}
	fields := literalCLIInputJSONFields(cmd)
	if (cmd.Flags().Changed("address") || fields["Address"]) && !channelAddressTypeAllowsLiteral(create.Type) {
		return newUsageError(
			"--address for a %s channel can hold a secret; pass it only through --cli-input-json file://channel.json",
			create.Type)
	}
	if fields["Headers"] {
		return newUsageError(
			"inline --cli-input-json Headers can hold a secret; pass it only through --cli-input-json file://channel.json")
	}
	return nil
}

// refuseLiteralUpdateChannelAddress refuses every literal --address flag,
// and every inline --cli-input-json value that sets Address or Headers, on
// update-channel, unconditionally. UpdateChannelInput carries no Type
// field, since the API has no way to change a channel's type, so this
// guard cannot tell a Webhook or Slack channel (whose address the monitor
// design requires --cli-input-json for) apart from an Email, SMS, or
// Telegram one without a request of its own, and a Guard must refuse
// before any request. Refusing every type costs nothing today:
// UpdateChannelInput carries no OTP fields yet, so updating an Email, SMS,
// Telegram, or Slack channel's Address always fails on the server for
// lacking one, and only a Webhook channel's Address update can succeed,
// which is exactly the case the design already requires --cli-input-json
// for. Headers has no flag of its own, so the only way it ever reaches
// argv is through an inline --cli-input-json value; a file:// one is exempt,
// the same as Address.
func refuseLiteralUpdateChannelAddress(cmd *cobra.Command, _ any) error {
	fields := literalCLIInputJSONFields(cmd)
	if cmd.Flags().Changed("address") || fields["Address"] {
		return newUsageError("--address can hold a secret; pass it only through --cli-input-json file://channel.json")
	}
	if fields["Headers"] {
		return newUsageError(
			"inline --cli-input-json Headers can hold a secret; pass it only through --cli-input-json file://channel.json")
	}
	return nil
}

func newMonitorCmd(e *env) *cobra.Command {
	return Service(e, "monitor", "vMonitor synthetic checks", monitor.New, monitorOps...)
}
