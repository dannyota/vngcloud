package cli

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

const vcdnKeyPlaceholder = "<secret>"

func TestConfigureSetVCDNKeyFromStdinWritesCredentialsFile(t *testing.T) {
	home := withCleanEnv(t)
	out, err := runConfigure(t, vcdnKeyPlaceholder+"\n", []string{"configure", "set", "vcdn_api_key", "-"})
	if err != nil {
		t.Fatalf("set vcdn_api_key: %v", err)
	}
	if strings.Contains(out, vcdnKeyPlaceholder) {
		t.Fatalf("set echoed the key: %q", out)
	}
	data, err := os.ReadFile(credentialsPath(home))
	if err != nil {
		t.Fatalf("ReadFile credentials: %v", err)
	}
	if got := readINIValue(data, "default", "vcdn_api_key"); got != vcdnKeyPlaceholder {
		t.Fatalf("vcdn_api_key in [default] = %q, want the piped value", got)
	}
	if _, statErr := os.Stat(configPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file should not have been created: %v", statErr)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(credentialsPath(home))
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("credentials mode = %o, want 0600", perm)
		}
	}
}

func TestConfigureSetVCDNKeyUsesTheActiveProfile(t *testing.T) {
	home := withCleanEnv(t)
	args := []string{"--profile", "agent", "configure", "set", "vcdn_api_key", "-"}
	if _, err := runConfigure(t, vcdnKeyPlaceholder+"\n", args); err != nil {
		t.Fatalf("set vcdn_api_key: %v", err)
	}
	data, err := os.ReadFile(credentialsPath(home))
	if err != nil {
		t.Fatalf("ReadFile credentials: %v", err)
	}
	if got := readINIValue(data, "agent", "vcdn_api_key"); got != vcdnKeyPlaceholder {
		t.Fatalf("vcdn_api_key in [agent] = %q, want the piped value", got)
	}
}

func TestConfigureGetAndListMaskVCDNKey(t *testing.T) {
	withCleanEnv(t)
	if _, err := runConfigure(t, vcdnKeyPlaceholder+"\n", []string{"configure", "set", "vcdn_api_key", "-"}); err != nil {
		t.Fatalf("set vcdn_api_key: %v", err)
	}
	out, err := runConfigure(t, "", []string{"configure", "get", "vcdn_api_key"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.TrimSpace(out) != "****" {
		t.Fatalf("get = %q, want ****", out)
	}
	out, err = runConfigure(t, "", []string{"configure", "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(out, vcdnKeyPlaceholder) {
		t.Fatalf("list leaked the key: %s", out)
	}
	if !strings.Contains(out, "vcdn_api_key = ****") {
		t.Fatalf("list = %q, want a masked vcdn_api_key line", out)
	}
}

func TestConfigureListShowsAnUnsetVCDNKeyAsEmpty(t *testing.T) {
	withCleanEnv(t)
	out, err := runConfigure(t, "", []string{"configure", "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "vcdn_api_key = \n") {
		t.Fatalf("list = %q, want an empty vcdn_api_key line", out)
	}
}

func TestConfigureLiteralVCDNKeyRefusedNamingStdin(t *testing.T) {
	home := withCleanEnv(t)
	_, err := runConfigure(t, "", []string{"configure", "set", "vcdn_api_key", vcdnKeyPlaceholder})
	if err == nil {
		t.Fatal("expected an error for a literal vcdn_api_key")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if !strings.Contains(err.Error(), "configure set vcdn_api_key -") {
		t.Fatalf("error does not name the stdin form: %v", err)
	}
	if strings.Contains(err.Error(), vcdnKeyPlaceholder) {
		t.Fatalf("error echoed the key: %v", err)
	}
	if _, statErr := os.Stat(credentialsPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("credentials file should not have been created: %v", statErr)
	}
}

// LoadConfig refuses a key with whitespace for every command, so configure
// refuses to store one.
func TestConfigureSetVCDNKeyRefusesWhitespaceAndOversize(t *testing.T) {
	for name, stdin := range map[string]string{
		"inner space": "<secret> two\n",
		"tab":         "<secret>\ttwo\n",
		"oversize":    strings.Repeat("a", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			home := withCleanEnv(t)
			_, err := runConfigure(t, stdin, []string{"configure", "set", "vcdn_api_key", "-"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if exitCode(err) != 2 {
				t.Fatalf("exitCode = %d, want 2", exitCode(err))
			}
			if strings.Contains(err.Error(), "<secret>") {
				t.Fatalf("error echoed the key: %v", err)
			}
			if _, statErr := os.Stat(credentialsPath(home)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("credentials file should not have been created: %v", statErr)
			}
		})
	}
}

func TestConfigureInteractiveDoesNotPromptForVCDNKey(t *testing.T) {
	for _, key := range configureKeyOrder {
		if key == "vcdn_api_key" {
			t.Fatal("vcdn_api_key is in the interactive prompt order")
		}
	}
}

func TestConfigureSetVCDNKeyRefusedWhileReadOnly(t *testing.T) {
	withCleanEnv(t)
	_, err := runConfigure(t, vcdnKeyPlaceholder+"\n", []string{"--read-only", "configure", "set", "vcdn_api_key", "-"})
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("err = %v, exit %d, want exit 2", err, exitCode(err))
	}
}
