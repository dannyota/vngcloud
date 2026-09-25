package cli

import (
	"fmt"
	"os"
	"strings"

	"danny.vn/vngcloud"
)

const envReadOnly = "VNGCLOUD_READ_ONLY"

// envProfile mirrors the SDK's own VNGCLOUD_PROFILE variable name, used only
// to name the profile in a read-only refusal message; it is not a call into
// the SDK's profile-resolution logic, which stays entirely inside LoadConfig.
const envProfile = "VNGCLOUD_PROFILE"

// readOnlyPreConfig checks the two sources available before LoadConfig runs:
// the --read-only flag and VNGCLOUD_READ_ONLY. Either source can only turn
// read-only on; an unrecognized value in the variable is a config error
// (exit 2) naming the variable, so a typo can never silently leave writes
// open. configure and configure set check only this: they never look at a
// profile's own read_only key.
func readOnlyPreConfig(flags *globalFlags) (on bool, source string, err error) {
	if flags.readOnly {
		return true, "--read-only", nil
	}
	value := os.Getenv(envReadOnly)
	on, ok := parseOnOff(value)
	if !ok {
		return false, "", newUsageError("%s must be 1, true, 0, false, or empty, got %q", envReadOnly, value)
	}
	if on {
		return true, envReadOnly, nil
	}
	return false, "", nil
}

// readOnlyFromProfile checks the resolved profile's own read_only config
// key, the third and last read-only source, evaluated only after LoadConfig
// has resolved which profile applies. profileName is for the refusal
// message only.
func readOnlyFromProfile(cfg vngcloud.Config, profileName string) (on bool, source string, err error) {
	value := cfg.ProfileSetting("read_only")
	on, ok := parseOnOff(value)
	if !ok {
		return false, "", newUsageError("read_only in profile %q must be 1, true, 0, false, or empty, got %q", profileName, value)
	}
	if on {
		return true, fmt.Sprintf("read_only in profile %q", profileName), nil
	}
	return false, "", nil
}

// parseOnOff reports whether value (case-insensitively) means on ("1" or
// "true"), off ("", "0", or "false"), and whether it was recognized at all.
func parseOnOff(value string) (on, ok bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false":
		return false, true
	case "1", "true":
		return true, true
	default:
		return false, false
	}
}

// resolvedProfileName names the profile LoadConfig will resolve, replicating
// only its name precedence (an explicit --profile, else VNGCLOUD_PROFILE,
// else "default") so a read-only refusal message can name the profile before
// LoadConfig itself has run. It affects no behavior: LoadConfig performs its
// own resolution independently.
func resolvedProfileName(flags *globalFlags) string {
	if flags.profile != "" {
		return flags.profile
	}
	if v := os.Getenv(envProfile); v != "" {
		return v
	}
	return "default"
}
