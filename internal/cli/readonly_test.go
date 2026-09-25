package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"danny.vn/vngcloud"
)

func TestReadOnlyPreConfig(t *testing.T) {
	tests := []struct {
		name       string
		flag       bool
		env        string
		wantOn     bool
		wantSource string
		wantErr    bool
	}{
		{"off by default", false, "", false, "", false},
		{"flag turns it on", true, "", true, "--read-only", false},
		{"env 1 turns it on", false, "1", true, envReadOnly, false},
		{"env true turns it on", false, "true", true, envReadOnly, false},
		{"env TRUE case insensitive turns it on", false, "TRUE", true, envReadOnly, false},
		{"env 0 leaves it off", false, "0", false, "", false},
		{"env false leaves it off", false, "false", false, "", false},
		{"flag wins over env off", true, "0", true, "--read-only", false},
		{"bad env value is a config error", false, "ture", false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envReadOnly, tt.env)
			flags := &globalFlags{readOnly: tt.flag}
			on, source, err := readOnlyPreConfig(flags)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none")
				}
				if exitCode(err) != 2 {
					t.Fatalf("exitCode = %d, want 2", exitCode(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if on != tt.wantOn || source != tt.wantSource {
				t.Fatalf("got (%v, %q), want (%v, %q)", on, source, tt.wantOn, tt.wantSource)
			}
		})
	}
}

func TestReadOnlyFromProfile(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n\n[profile writer]\nregion = hcm-3\nread_only = false\n\n[profile bad]\nregion = hcm-3\nread_only = ture\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n\n[writer]\nusername = u\npassword = p\nroot_email = e@example.com\n\n[bad]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	cfgAgent, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithProfile("agent"))
	if err != nil {
		t.Fatalf("LoadConfig(agent): %v", err)
	}
	if on, source, err := readOnlyFromProfile(cfgAgent, "agent"); err != nil || !on || source != `read_only in profile "agent"` {
		t.Fatalf("got (%v, %q, %v), want (true, read_only in profile \"agent\", nil)", on, source, err)
	}

	cfgWriter, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithProfile("writer"))
	if err != nil {
		t.Fatalf("LoadConfig(writer): %v", err)
	}
	if on, _, err := readOnlyFromProfile(cfgWriter, "writer"); err != nil || on {
		t.Fatalf("got (%v, _, %v), want (false, nil)", on, err)
	}

	cfgBad, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithProfile("bad"))
	if err != nil {
		t.Fatalf("LoadConfig(bad): %v", err)
	}
	if _, _, err := readOnlyFromProfile(cfgBad, "bad"); err == nil {
		t.Fatalf("expected a config error for a bad read_only value")
	} else if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestResolvedProfileName(t *testing.T) {
	t.Setenv(envProfile, "")
	if got := resolvedProfileName(&globalFlags{}); got != "default" {
		t.Fatalf("got %q, want default", got)
	}
	t.Setenv(envProfile, "from-env")
	if got := resolvedProfileName(&globalFlags{}); got != "from-env" {
		t.Fatalf("got %q, want from-env", got)
	}
	if got := resolvedProfileName(&globalFlags{profile: "explicit"}); got != "explicit" {
		t.Fatalf("got %q, want explicit (flag beats env)", got)
	}
}

// writeConfigFile and writeCredentialsFile write the CLI's two profile files
// under home/.vngcloud, matching LoadConfig's default paths, with the
// credentials file at mode 0600 as LoadConfig requires.
func writeConfigFile(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".vngcloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
}

func writeCredentialsFile(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".vngcloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile credentials: %v", err)
	}
}
