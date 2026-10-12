package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestObjectProfileIsolation(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[default]\noutput = text\nregion = invalid\nauth_type = invalid\n[profile reader]\noutput = table\nread_only = true\nproject_id = ignored\n")
	writeCredentialsFile(t, home, "not valid INI or credentials")
	t.Setenv(envCredsFileVar, filepath.Join(home, "must-not-open"))
	if err := os.WriteFile(filepath.Join(home, ".vngcloud", "cache"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ flag, env, output, ro string }{{"", "", "text", ""}, {"", "reader", "table", "true"}, {"default", "reader", "text", ""}} {
		t.Setenv(envProfile, tc.env)
		p, err := loadObjectProfile(&globalFlags{profile: tc.flag})
		if err != nil || p.Output != tc.output || p.ReadOnly != tc.ro {
			t.Fatalf("profile = %+v, %v", p, err)
		}
	}
}

func TestObjectProfileFailures(t *testing.T) {
	for _, tc := range []struct {
		name, content, profile string
		explicit               bool
	}{
		{"missing default", "", "default", false},
		{"missing explicit", "", "default", true},
		{"missing named", "", "named", false},
		{"absent section", "[default]\noutput=json\n", "named", true},
		{"malformed", "[default\npassword=synthetic-secret\n", "default", true},
		{"oversize", "[default]\n#" + strings.Repeat("x", 1024*1024), "default", true},
		{"directory", "", "default", true},
		{"bad profile", "", "[bad]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := withCleanEnv(t)
			path := filepath.Join(home, "config")
			if tc.name == "directory" {
				path = home
			} else if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.explicit {
				t.Setenv(envConfigFileVar, path)
			}
			_, err := loadObjectProfile(&globalFlags{profile: tc.profile})
			if tc.name == "missing default" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || exitCode(err) != 2 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("error = %v", err)
			}
			if tc.name != "bad profile" && !errors.Is(err, vngcloud.ErrInvalidConfig) {
				t.Fatalf("identity = %v", err)
			}
		})
	}
}

func TestObjectProfileNoHome(t *testing.T) {
	withCleanEnv(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := loadObjectProfile(&globalFlags{}); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("missing home error=%v", err)
	}
}

func TestObjectProfileFIFO(t *testing.T) {
	if !objectFilesSupported() {
		t.Skip("nonblocking Unix config open")
	}
	withCleanEnv(t)
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("mkfifo unavailable")
	}
	path := filepath.Join(t.TempDir(), "config-fifo")
	if err := exec.CommandContext(context.Background(), "mkfifo", path).Run(); err != nil { //nolint:gosec // fixed executable and synthetic temporary path, with no shell interpolation
		t.Fatal(err)
	}
	t.Setenv(envConfigFileVar, path)
	done := make(chan error, 1)
	go func() { _, err := loadObjectProfile(&globalFlags{}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, vngcloud.ErrInvalidConfig) {
			t.Fatalf("FIFO error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("profile open blocked on FIFO")
	}
}
