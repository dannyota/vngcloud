package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"danny.vn/vngcloud/monitor"
)

const (
	inAlarmChannelFlag      = "in-alarm-channel-id"
	upChannelFlag           = "up-channel-id"
	undeterminedChannelFlag = "undetermined-channel-id"
)

func monitorCheckNotificationOp(op Op[monitor.Client]) Op[monitor.Client] {
	op.extraFlags = func(cmd *cobra.Command) {
		for _, name := range []string{inAlarmChannelFlag, upChannelFlag, undeterminedChannelFlag} {
			cmd.Flags().StringArray(name, nil, "Channel ID; repeat for multiple IDs, or pass an empty value to clear")
		}
	}
	op.guard = populateCheckNotificationFlags
	return op
}

func populateCheckNotificationFlags(cmd *cobra.Command, input any) error {
	checkNotificationFlags := [...]string{inAlarmChannelFlag, upChannelFlag, undeterminedChannelFlag}
	values := make([][]string, len(checkNotificationFlags))
	changed := 0
	for i, name := range checkNotificationFlags {
		if !cmd.Flags().Changed(name) {
			continue
		}
		changed++
		flag, ok := cmd.Flags().Lookup(name).Value.(pflag.SliceValue)
		if !ok {
			return fmt.Errorf("notification flag --%s is not a slice", name)
		}
		value := flag.GetSlice()
		for _, id := range value {
			if id == "" {
				if len(value) != 1 {
					return newUsageError("--%s cannot mix an empty value with other values", name)
				}
				value = []string{}
				break
			}
			if strings.TrimSpace(id) == "" {
				return newUsageError("--%s cannot contain a whitespace-only ID", name)
			}
		}
		values[i] = value
	}
	if changed == 0 {
		return nil
	}
	var notifications *monitor.CheckNotifications
	switch in := input.(type) {
	case *monitor.CreateCheckInput:
		notifications = &in.Notifications
	case *monitor.UpdateCheckInput:
		if changed != len(checkNotificationFlags) {
			return newUsageError("update-check requires all three notification channel ID flags or none")
		}
		in.Notifications = &monitor.CheckNotifications{}
		notifications = in.Notifications
	}
	if cmd.Flags().Changed(checkNotificationFlags[0]) {
		notifications.InAlarm = values[0]
	}
	if cmd.Flags().Changed(checkNotificationFlags[1]) {
		notifications.Up = values[1]
	}
	if cmd.Flags().Changed(checkNotificationFlags[2]) {
		notifications.Undetermined = values[2]
	}
	return nil
}
