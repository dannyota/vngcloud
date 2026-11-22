package cli

import (
	"errors"
	"os"
	"testing"
)

func TestConfigureRejectsUnsafeRegionBeforeWrite(t *testing.T) {
	for _, mode := range []string{"set", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			home := withCleanEnv(t)
			var err error
			if mode == "set" {
				_, err = runConfigure(t, "", []string{"configure", "set", "region", "HCM-3"})
			} else {
				previous := isTerminalOverride
				isTerminalOverride = func(any) bool { return true }
				t.Cleanup(func() { isTerminalOverride = previous })
				_, err = runConfigure(t, "HCM-3\n", []string{"configure"})
			}
			if err == nil {
				t.Fatal("invalid region accepted")
			}
			if _, err := os.Stat(configPath(home)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("config was written or could not be checked: %v", err)
			}
		})
	}
}
