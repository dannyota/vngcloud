package cli

import (
	"context"
	"strings"
	"testing"
)

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
