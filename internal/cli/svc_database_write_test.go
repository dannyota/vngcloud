package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/database"
)

const databaseCanary = "Canary_Pass5521"

var databaseCreateArgs = []string{"database", "create-database-instance", "--name", "demo-db", "--zone-id", "zone-1", "--flavor-id", "211", "--storage-type-id", "Gen2-NVMe2-IOPS3000", "--storage-size", "20", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1", "--database-name", "appdb", "--username", "admin_user"}

func databasePasswordFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(databaseCanary+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func runDatabase(t *testing.T, stdin string, args ...string) (stdout, stderr string, requests int, err error) {
	t.Helper()
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, out, errOut := newSvcRoot(t, fixture)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"--region", "hcm-3"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), fixture.requestCount(), err
}

func TestDatabaseCreateRequiresConsent(t *testing.T) {
	path := databasePasswordFile(t, 0o600)
	args := append(append([]string{}, databaseCreateArgs...), "--password-file", path)
	_, _, requests, err := runDatabase(t, "", args...)
	if err == nil || !strings.Contains(err.Error(), "--yes") || requests != 0 || classify(err).Code != "InvalidUsage" {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestDatabaseWritesRefusedInReadOnly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-read")
	create := append(append([]string{"--read-only", "--yes"}, databaseCreateArgs...), "--password-file", missing)
	_, _, requests, err := runDatabase(t, "", create...)
	if err == nil || classify(err).Code != "ReadOnly" || requests != 0 {
		t.Fatalf("create err=%v requests=%d", err, requests)
	}
	_, _, requests, err = runDatabase(t, "", "--read-only", "--yes", "database", "delete-database-instance", "--id", "db-1")
	if err == nil || classify(err).Code != "ReadOnly" || requests != 0 {
		t.Fatalf("delete err=%v requests=%d", err, requests)
	}
}

func TestDatabaseDeleteRequiresConsent(t *testing.T) {
	_, _, requests, err := runDatabase(t, "", "database", "delete-database-instance", "--id", "db-1")
	if err == nil || !strings.Contains(err.Error(), "--yes") || requests != 0 {
		t.Fatalf("err=%v requests=%d", err, requests)
	}
}

func TestDatabaseCreatePasswordInput(t *testing.T) {
	cases := []struct {
		name  string
		args  func(t *testing.T) []string
		stdin string
		want  string
	}{
		{"missing flag", func(*testing.T) []string { return nil }, "", "--password-file is required"},
		{"group readable file", func(t *testing.T) []string { return []string{"--password-file", databasePasswordFile(t, 0o644)} }, "", "private"},
		{"missing file", func(t *testing.T) []string {
			return []string{"--password-file", filepath.Join(t.TempDir(), "absent")}
		}, "", "cannot open"},
		{"empty stdin", func(*testing.T) []string { return []string{"--password-file", "-"} }, "", "non-empty"},
		{"control character on stdin", func(*testing.T) []string { return []string{"--password-file", "-"} }, "ab\x01cd\n", "controls"},
		{"stdin with file json", func(*testing.T) []string {
			return []string{"--password-file", "-", "--cli-input-json", "file://x.json"}
		}, databaseCanary, "cannot share"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{"--yes"}, databaseCreateArgs...), tc.args(t)...)
			stdout, stderr, requests, err := runDatabase(t, tc.stdin, args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || requests != 0 || classify(err).Code != "InvalidUsage" {
				t.Fatalf("err=%v requests=%d", err, requests)
			}
			if strings.Contains(err.Error()+stdout+stderr, databaseCanary) {
				t.Fatal("password echoed")
			}
		})
	}
}

func TestDatabaseCreatePasswordSourcesReachTheSDKWithoutLeaking(t *testing.T) {
	path := databasePasswordFile(t, 0o600)
	for name, tc := range map[string]struct {
		args  []string
		stdin string
	}{
		"file":  {[]string{"--password-file", path}, ""},
		"stdin": {[]string{"--password-file", "-"}, databaseCanary + "\r\n"},
	} {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{"--yes"}, databaseCreateArgs...), tc.args...)
			stdout, stderr, requests, err := runDatabase(t, tc.stdin, args...)
			if err == nil || classify(err).Code != "InvalidConfig" || requests != 0 {
				t.Fatalf("err=%v requests=%d", err, requests)
			}
			if strings.Contains(err.Error()+stdout+stderr, databaseCanary) {
				t.Fatal("password echoed")
			}
		})
	}
}

func TestDatabaseCreatePasswordIsNotAFlagOrJSONField(t *testing.T) {
	path := databasePasswordFile(t, 0o600)
	for _, extra := range [][]string{{"--password", databaseCanary}, {"--cli-input-json", `{"Password":"` + databaseCanary + `"}`}} {
		args := append(append([]string{"--yes"}, databaseCreateArgs...), "--password-file", path)
		stdout, stderr, requests, err := runDatabase(t, "", append(args, extra...)...)
		if err == nil || requests != 0 || strings.Contains(err.Error()+stdout+stderr, databaseCanary) {
			t.Fatalf("extra=%v err=%v requests=%d", extra, err, requests)
		}
	}
}

func TestDatabaseCreateHelp(t *testing.T) {
	stdout, _, _, err := runDatabase(t, "", "database", "create-database-instance", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"credits", "single-node", "private", "backup off", "--password-file", "never from argv", "monthlyCost", "catalog estimate, not a quote", "--yes", "never resent"} {
		if !strings.Contains(stdout, word) {
			t.Errorf("help lacks %q", word)
		}
	}
	if strings.Contains(stdout, "--password ") || strings.Contains(stdout, "--max-price") {
		t.Error("help shows a password or quote flag")
	}
}

func TestDatabaseDeleteHelp(t *testing.T) {
	stdout, _, _, err := runDatabase(t, "", "database", "delete-database-instance", "--help")
	if err != nil || !strings.Contains(stdout, "no backups") || !strings.Contains(stdout, "--yes") {
		t.Fatalf("err=%v help=%s", err, stdout)
	}
}

func TestDatabaseUnsettledClassification(t *testing.T) {
	if got := classify(database.ErrNotSettled).Code; got != "NotSettled" || exitCode(database.ErrNotSettled) != 1 {
		t.Fatalf("code=%s", got)
	}
	if got := classify(database.ErrFailed).Code; got != "WriteFailed" || exitCode(database.ErrFailed) != 1 {
		t.Fatalf("code=%s", got)
	}
}

func TestDatabasePartialOutputOnStdout(t *testing.T) {
	for name, want := range map[string]error{"NotSettled": database.ErrNotSettled, "WriteFailed": database.ErrFailed} {
		t.Run(name, func(t *testing.T) {
			h := newFakeHarness(t)
			op := databaseCreateOp()
			op.call = func(*cobra.Command, *database.Client, context.Context, any) (any, error) {
				return &database.CreateDatabaseInstanceOutput{Instance: database.Instance{ID: "db-1"}}, want
			}
			root := newTestRoot(h.e)
			root.AddCommand(Service(h.e, "database", "test", database.New, op))
			err := execCmd(t, root, append(append([]string{"--output", "json", "--yes"}, databaseCreateArgs...), "--password-file", databasePasswordFile(t, 0o600)))
			if classify(err).Code != name || exitCode(err) != 1 || !strings.Contains(h.stdout.String(), `"db-1"`) {
				t.Fatalf("err=%v stdout=%s", err, h.stdout)
			}
		})
	}
	h := newFakeHarness(t)
	op := databaseDeleteOp()
	op.call = func(*cobra.Command, *database.Client, context.Context, any) (any, error) {
		return &database.DeleteDatabaseInstanceOutput{ID: "db-1"}, database.ErrNotSettled
	}
	root := newTestRoot(h.e)
	root.AddCommand(Service(h.e, "database", "test", database.New, op))
	err := execCmd(t, root, []string{"--output", "json", "--yes", "database", "delete-database-instance", "--id", "db-1"})
	if classify(err).Code != "NotSettled" || !strings.Contains(h.stdout.String(), `"db-1"`) || strings.Contains(h.stdout.String(), `"Deleted": true`) {
		t.Fatalf("err=%v stdout=%s", err, h.stdout)
	}
}
