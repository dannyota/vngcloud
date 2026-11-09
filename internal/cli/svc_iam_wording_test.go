package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIAMPermissionConfirmationReasons(t *testing.T) {
	tests := []struct {
		command string
		flags   []string
		reason  string
	}{
		{"attach-service-account-policy", []string{"--policy-id", "p", "--service-account-id", "sa"}, "grants permissions"},
		{"detach-service-account-policy", []string{"--policy-id", "p", "--service-account-id", "sa"}, "removes permissions"},
		{"attach-user-policy", []string{"--policy-id", "p", "--user-id", "u"}, "grants permissions"},
		{"detach-user-policy", []string{"--policy-id", "p", "--user-id", "u"}, "removes permissions"},
		{"attach-group-policy", []string{"--policy-id", "p", "--group-id", "g"}, "grants permissions"},
		{"detach-group-policy", []string{"--policy-id", "p", "--group-id", "g"}, "removes permissions"},
		{"add-user-to-group", []string{"--group-id", "g", "--user-id", "u"}, "grants permissions"},
		{"remove-user-from-group", []string{"--group-id", "g", "--user-id", "u"}, "removes permissions"},
	}
	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, _, _ := newSvcRoot(t, fixture)
			args := []string{"--region", "hcm-3", "iam", tc.command}
			root.SetArgs(append(args, tc.flags...))
			err := root.ExecuteContext(context.Background())
			want := "iam " + tc.command + " " + tc.reason + "; pass --yes to confirm"
			if err == nil || exitCode(err) != 2 || !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want exit 2 and %q", err, want)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Errorf("request count = %d, want 0", n)
			}
		})
	}
}

func TestNonIAMDestructiveConfirmationReason(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "delete-server-group", "--server-group-id", "sg-1"})
	err := root.ExecuteContext(context.Background())
	want := "compute delete-server-group is destructive; pass --yes to confirm"
	if err == nil || exitCode(err) != 2 || !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want exit 2 and %q", err, want)
	}
	if n := fixture.requestCount(); n != 0 {
		t.Errorf("request count = %d, want 0", n)
	}
}

func TestConfirmationFlagHelp(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "confirm a destructive or permission-changing operation") {
		t.Error("help does not describe permission-changing confirmations")
	}
}

func TestIAMPolicyLookupHelpAndDocs(t *testing.T) {
	tests := []struct {
		command string
		wants   []string
	}{
		{"get-service-account", []string{"list-service-account-policies", "--service-account-id"}},
		{"list-service-accounts", []string{"list-service-account-policies", "--service-account-id"}},
		{"get-user", []string{"list-user-policies", "list-user-groups", "--user-id"}},
		{"list-users", []string{"list-user-policies", "list-user-groups", "--user-id"}},
	}
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "CLI-IAM.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, stdout, _ := newSvcRoot(t, fixture)
			root.SetArgs([]string{"iam", tc.command, "--help"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			section := genDocsSection(t, string(page), tc.command)
			if tc.command == "get-user" {
				if strings.Contains(stdout.String(), "[IAM](IAM.md#google-mfa)") || !strings.Contains(stdout.String(), "See the IAM wiki page.") {
					t.Error("help should reference the IAM wiki in plain text")
				}
				if !strings.Contains(section, "[IAM](IAM.md#google-mfa)") {
					t.Error("wiki section should retain the IAM link")
				}
			}
			for _, want := range tc.wants {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help missing %q", want)
				}
				if !strings.Contains(section, want) {
					t.Errorf("wiki section missing %q", want)
				}
			}
			if n := fixture.requestCount(); n != 0 {
				t.Errorf("request count = %d, want 0", n)
			}
		})
	}
}
