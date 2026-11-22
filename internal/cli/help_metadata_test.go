package cli

import (
	"context"
	"strings"
	"testing"
)

func TestRequiredFlagHelpMatchesReference(t *testing.T) {
	for _, tc := range []struct {
		service, command, flag string
		page                   string
	}{
		{"storage", "get-bucket-encryption", "project-id", renderServicePage(buildDocService("storage", storageOps))},
		{"network", "remove-network-acl-rule", "priority", renderServicePage(buildDocService("network", networkOps))},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root, out, _ := newSvcRoot(t, newSvcFixture(nil))
			root.SetArgs([]string{tc.service, tc.command, "--help"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			var line string
			for _, candidate := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(strings.TrimSpace(candidate), "--"+tc.flag+" ") {
					line = candidate
					break
				}
			}
			if !strings.Contains(line, "required") {
				t.Errorf("help flag line is not required: %q", line)
			}
			section := strings.SplitN(tc.page, "## "+tc.command+"\n", 2)
			if len(section) != 2 {
				t.Fatal("missing reference section")
			}
			section = strings.SplitN(section[1], "\n## ", 2)
			var row string
			for _, candidate := range strings.Split(section[0], "\n") {
				if strings.HasPrefix(candidate, "| `--"+tc.flag+"` |") {
					row = candidate
					break
				}
			}
			if !strings.HasSuffix(row, " | yes |") {
				t.Errorf("reference flag row is not required: %q", row)
			}
			if tc.flag == "project-id" {
				flag := root.PersistentFlags().Lookup("project-id")
				if strings.Contains(flag.Usage, "required") {
					t.Errorf("root flag changed: %q", flag.Usage)
				}
			}
		})
	}
}

func TestOperationHelpMetadata(t *testing.T) {
	for _, tc := range []struct {
		service, command string
		want             []string
	}{
		{"network", "create-nat-instance", []string{"Input type: string; required", "--max-price", "consent", "VND"}},
		{"network", "delete-nat-instance", []string{"--yes", "egress"}},
		{"network", "remove-network-acl-rule", []string{"--priority", "priority 0"}},
		{"pricing", "estimate", []string{"QuotaGB", "floating-ip", "defaults to 500"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root, out, _ := newSvcRoot(t, newSvcFixture(nil))
			root.SetArgs([]string{tc.service, tc.command, "--help"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help lacks %q", want)
				}
			}
			note := docOpNotes[tc.service+" "+tc.command]
			if note == "" || !strings.Contains(out.String(), note) {
				t.Error("help lacks the generated reference note")
			}
		})
	}
}
