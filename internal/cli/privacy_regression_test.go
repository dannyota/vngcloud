package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

func TestPrivacyQueryFailureWithholdsCause(t *testing.T) {
	for _, write := range []bool{false, true} {
		for _, format := range []string{outputJSON, outputText, outputTable} {
			err := renderOutput(io.Discard, format, "abs(Secret)", map[string]string{"Secret": "synthetic-credential"}, write)
			var stderr bytes.Buffer
			printError(&stderr, err)
			if strings.Contains(err.Error()+stderr.String(), "synthetic-credential") || errors.Unwrap(err) != nil {
				t.Fatalf("query error retains cause: %v", err)
			}
			if write && !strings.Contains(err.Error(), "write succeeded") {
				t.Fatalf("missing warning: %v", err)
			}
		}
	}
}

func TestPrivacyProjectAmbiguousClassification(t *testing.T) {
	if got := classify(vngcloud.ErrProjectAmbiguous).Code; got != "InvalidConfig" {
		t.Fatalf("class = %s", got)
	}
}

func TestPrivacyQuotaQueryFailure(t *testing.T) {
	for _, format := range []string{outputJSON, outputText, outputTable} {
		op := monitorQuotaView(Write[monitor.Client, monitor.CreateNotificationQuotaInput, monitor.CreateNotificationQuotaOutput]("create-notification-quota", (*monitor.Client).CreateNotificationQuota))
		var stdout, stderr bytes.Buffer
		err := op.render(&stdout, format, "length(`42`)", &monitor.CreateNotificationQuotaOutput{OrderID: "order-1"})
		printError(&stderr, err)
		var q *queryFailedError
		if !errors.As(err, &q) || !q.writeSucceeded || exitCode(err) != 1 || classify(err).Code != "QueryFailed" || stdout.Len() != 0 || !strings.Contains(stderr.String(), "write succeeded") {
			t.Errorf("%s: %v exit=%d stdout=%q stderr=%q", format, err, exitCode(err), stdout.String(), stderr.String())
		}
	}
}

func TestPrivacyMonitorLocationsOverride(t *testing.T) {
	var body struct {
		Locations []string `json:"locations"`
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes": func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(monitorUptimesJSON("chk-9", monitor.StatusEnabled)))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "create-check", "--name", "test", "--url", "https://example.com", "--cli-input-json", `{"Locations":["old"]}`, "--locations", "new-1", "--locations", "new-2"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if len(body.Locations) != 2 || body.Locations[0] != "new-1" || body.Locations[1] != "new-2" {
		t.Fatalf("locations: %v", body.Locations)
	}
}

func TestPrivacyACLNullSendsNothing(t *testing.T) {
	for _, input := range []string{`{}`, `{"Priority":null}`} {
		fixture := newSvcFixture(nil)
		root, _, _ := newSvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--yes", "network", "remove-network-acl-rule", "--network-acl-id", "acl-1", "--direction", "inbound", "--cli-input-json", input})
		err := root.ExecuteContext(context.Background())
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
			t.Fatalf("input=%s err=%v requests=%d", input, err, fixture.requestCount())
		}
	}
}

func TestACLInputFileChangedBeforeGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"Priority":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := newSvcFixture(nil)
	idx := -1
	for i := range networkOps {
		if networkOps[i].name == "remove-network-acl-rule" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("missing operation")
	}
	original := networkOps[idx].guard
	networkOps[idx].guard = func(cmd *cobra.Command, in any) error {
		if err := os.WriteFile(path, []byte(`{"Priority":10}`), 0600); err != nil {
			t.Fatal(err)
		}
		return original(cmd, in)
	}
	root, _, _ := newSvcRoot(t, fixture)
	networkOps[idx].guard = original
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--yes", "network", "remove-network-acl-rule", "--network-acl-id", "acl-1", "--direction", "inbound", "--cli-input-json", "file://" + path})
	err := root.ExecuteContext(context.Background())
	if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestPrivacyACLPriorityNull(t *testing.T) {
	for _, tc := range []struct {
		input string
		valid bool
	}{{`{}`, false}, {`{"Priority":null}`, false}, {`{"Priority":0}`, true}} {
		cmd := &cobra.Command{}
		cmd.Flags().Int("priority", 0, "")
		cmd.Flags().Bool("yes", true, "")
		cmd.Flags().String("cli-input-json", tc.input, "")
		presence := make(map[string]bool)
		var input struct{ Priority int }
		if err := applyCLIInputJSON(tc.input, &input, presence); err != nil {
			t.Fatal(err)
		}
		cmd.SetContext(context.WithValue(context.Background(), inputPresenceKey{}, presence))
		err := requireYesAndPriorityToRemoveACLRule(cmd, nil)
		if (err == nil) != tc.valid {
			t.Errorf("input %s: %v", tc.input, err)
		}
	}
}
