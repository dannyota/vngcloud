package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// createSecurityGroupJSON builds CreateSecurityGroup's own response
// envelope, the shape network's create_security_group.json fixture
// decodes: an integer id and secgroupName alongside the group's own uuid,
// always group "secg-1" named "web" with no description, the only group
// every test in this file creates.
func createSecurityGroupJSON() string {
	body := map[string]any{
		"id":           101,
		"uuid":         "secg-1",
		"secgroupName": "web",
		"description":  "",
		"status":       "ACTIVE",
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// getSecurityGroupJSON builds GetSecurityGroup's own response envelope,
// always under id "secg-1", the only group ID every test in this file
// reads. system sets both isSystem and system, since GetSecurityGroup's
// own checks (UpdateSecurityGroup, DeleteSecurityGroup) read System, not
// IsSystem.
func getSecurityGroupJSON(name, description, status string, system bool) string {
	body := map[string]any{
		"id":          "secg-1",
		"name":        name,
		"description": description,
		"status":      status,
		"isSystem":    system,
		"system":      system,
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// createSecurityGroupRuleJSON builds CreateSecurityGroupRule's own response
// envelope, the shape network's create_security_group_rule.json fixture
// decodes: an integer ruleId alongside the rule's own uuid.
func createSecurityGroupRuleJSON(uuid, secgroupUUID string) string {
	body := map[string]any{
		"id":           501,
		"uuid":         uuid,
		"secgroupUuid": secgroupUUID,
		"ruleId":       501,
		"status":       "ACTIVE",
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// listSecurityGroupRulesJSON builds ListSecurityGroupRules' own response
// envelope from a list of rule IDs, the only field DeleteSecurityGroupRule's
// pre-delete check reads.
func listSecurityGroupRulesJSON(ruleIDs ...string) string {
	items := make([]map[string]any, len(ruleIDs))
	for i, id := range ruleIDs {
		items[i] = map[string]any{"id": id}
	}
	b, err := json.Marshal(map[string]any{"data": items})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestNetworkCreateSecurityGroupEndToEnd drives the real create-security-group
// command against a fixture whose confirm read already shows ACTIVE, so the
// SDK's post-create wait settles on its first read and this test never
// really sleeps: it checks the POST body (name and description only) and
// that the settled group comes back on stdout.
func TestNetworkCreateSecurityGroupEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(createSecurityGroupJSON()))
		},
		"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(getSecurityGroupJSON("web", "", "ACTIVE", false)))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-security-group", "--name", "web",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-security-group: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "web" || decoded["description"] != "" {
		t.Fatalf("body = %s, want name=web and description=\"\"", body)
	}

	got := stdout.String()
	if !strings.Contains(got, `"ID": "secg-1"`) || !strings.Contains(got, `"Status": "ACTIVE"`) {
		t.Fatalf("stdout = %s, want the settled ACTIVE group", got)
	}
}

// TestNetworkCreateSecurityGroupNoWaitSkipsConfirmRead checks that --no-wait
// sends only the create POST, with no confirm GET afterward.
func TestNetworkCreateSecurityGroupNoWaitSkipsConfirmRead(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups": jsonHandler(http.StatusCreated, createSecurityGroupJSON()),
		"/v2/proj-1/secgroups/secg-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-security-group", "--name", "web", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-security-group: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (--no-wait must send no confirm read)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"ID": "secg-1"`) {
		t.Fatalf("stdout = %s, want the mapped create response", got)
	}
}

// TestNetworkCreateSecurityGroupWriteFailedPrintsOutputOnStdout drives a
// real create-security-group call whose confirm read already shows ERROR,
// so the SDK's wait fails at once with no real sleep: this exercises the
// real network.ErrFailed path.
func TestNetworkCreateSecurityGroupWriteFailedPrintsOutputOnStdout(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups":        jsonHandler(http.StatusCreated, createSecurityGroupJSON()),
		"/v2/proj-1/secgroups/secg-1": jsonHandler(http.StatusOK, getSecurityGroupJSON("web", "", "ERROR", false)),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-security-group", "--name", "web",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "WriteFailed" {
		t.Fatalf("Code = %q, want WriteFailed", got)
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	got := stdout.String()
	if !strings.Contains(got, `"ID": "secg-1"`) || !strings.Contains(got, `"Status": "ERROR"`) {
		t.Fatalf("stdout = %s, want the ERROR group with its id printed alongside the error", got)
	}
}

// TestNetworkCreateSecurityGroupNotSettledOnCanceledContext drives a real
// create-security-group call whose POST succeeds and whose confirm read is
// interrupted by canceling the command's own context, mirroring a Ctrl-C
// during the post-create wait. This must surface as an error wrapping
// network.ErrNotSettled with the last group the SDK read as a non-nil
// Output, not the plain canceled-context path.
func TestNetworkCreateSecurityGroupNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups": jsonHandler(http.StatusCreated, createSecurityGroupJSON()),
		"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(getSecurityGroupJSON("web", "", "CREATING", false)))
			cancel()
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-security-group", "--name", "web",
	})
	err := root.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotSettled" {
		t.Fatalf("Code = %q, want NotSettled, not the plain canceled-context path (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"ID": "secg-1"`) {
		t.Fatalf("stdout = %s, want the Output with the group id", got)
	}
}

// TestNetworkUpdateSecurityGroupMergesUnchangedField checks
// UpdateSecurityGroup's read-merge contract from the CLI: a field left off
// the command line resends the group's current value, read fresh from
// GetSecurityGroup, unchanged.
func TestNetworkUpdateSecurityGroupMergesUnchangedField(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantBody map[string]any
	}{
		{
			"only name set",
			[]string{"--name", "new-name"},
			map[string]any{"name": "new-name", "description": "desc"},
		},
		{
			"only description set",
			[]string{"--description", "new-desc"},
			map[string]any{"name": "old-name", "description": "new-desc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(getSecurityGroupJSON("old-name", "desc", "ACTIVE", false)))
					case http.MethodPut:
						defer func() { _ = r.Body.Close() }()
						body, _ = io.ReadAll(r.Body)
						w.WriteHeader(http.StatusOK)
					default:
						t.Fatalf("unexpected method %s", r.Method)
					}
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			args := append([]string{
				"--region", "hcm-3", "--project-id", "proj-1",
				"network", "update-security-group", "--security-group-id", "secg-1",
			}, tt.args...)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("update-security-group: %v (stderr=%s)", err, stderr.String())
			}

			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("body is not valid JSON: %v (%s)", err, body)
			}
			if decoded["name"] != tt.wantBody["name"] || decoded["description"] != tt.wantBody["description"] {
				t.Fatalf("body = %s, want %v", body, tt.wantBody)
			}
		})
	}
}

// TestNetworkUpdateSecurityGroupEmptyUpdateIsUsageErrorWithZeroRequests
// checks that update-security-group with neither --name nor --description
// is refused before any request: the SDK's own check runs before its first
// GetSecurityGroup read.
func TestNetworkUpdateSecurityGroupEmptyUpdateIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "update-security-group", "--security-group-id", "secg-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkUpdateSecurityGroupRefusesSystemGroupWithNoPUT checks that a
// System group's own read stops update-security-group before any PUT.
func TestNetworkUpdateSecurityGroupRefusesSystemGroupWithNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(getSecurityGroupJSON("default", "", "ACTIVE", true)))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "update-security-group", "--security-group-id", "secg-1", "--description", "new",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "SystemSecurityGroup" {
		t.Fatalf("Code = %q, want SystemSecurityGroup (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the read only, no PUT)", n)
	}
}

// TestNetworkDeleteSecurityGroupRequiresYesWithZeroRequests checks the
// destructive --yes guard: without it, nothing is sent, not even the
// pre-delete read.
func TestNetworkDeleteSecurityGroupRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-security-group", "--security-group-id", "secg-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDeleteSecurityGroupWithYesSendsGetListDelete checks the
// success path's exact request sequence: the pre-delete group read, the
// servers-by-group read, then the DELETE.
func TestNetworkDeleteSecurityGroupWithYesSendsGetListDelete(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(getSecurityGroupJSON("web", "", "ACTIVE", false)))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/secgroups/secg-1/servers": jsonHandler(http.StatusOK, `{"data":[]}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-security-group", "--security-group-id", "secg-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-security-group: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 3 {
		t.Fatalf("requestCount = %d, want 3 (group read, servers read, delete)", n)
	}
}

// TestNetworkDeleteSecurityGroupRefusesSystemGroupWithNoDelete checks that
// the pre-delete read alone stops a system group's delete, without even
// checking for attached servers.
func TestNetworkDeleteSecurityGroupRefusesSystemGroupWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(getSecurityGroupJSON("default", "", "ACTIVE", true)))
		},
		"/v2/proj-1/secgroups/secg-1/servers": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-security-group", "--security-group-id", "secg-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "SystemSecurityGroup" {
		t.Fatalf("Code = %q, want SystemSecurityGroup (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the group read only)", n)
	}
}

// TestNetworkDeleteSecurityGroupInUseWithServersNoDelete checks that a
// group with an attached server stops the delete before any DELETE.
func TestNetworkDeleteSecurityGroupInUseWithServersNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(getSecurityGroupJSON("web", "", "ACTIVE", false)))
		},
		"/v2/proj-1/secgroups/secg-1/servers": jsonHandler(http.StatusOK, `{"data":[{"uuid":"server-1","name":"server-1"}]}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-security-group", "--security-group-id", "secg-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "SecurityGroupInUse" {
		t.Fatalf("Code = %q, want SecurityGroupInUse (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (group read, servers read, no delete)", n)
	}
}

// TestNetworkCreateSecurityGroupRuleWorldOpenIngressRequiresYes drives the
// refuseWorldOpenIngressWithoutYes guard (svc_network_write.go) through
// both the flag path and the --cli-input-json path, for IPv4 and IPv6,
// checking that a length-0 prefix is blocked without --yes for any
// direction that is not exactly "egress" (case-insensitively): an uppercase
// "INGRESS", a misspelled or padded value such as " ingress", and a
// length-0 prefix with host bits set such as "1.2.3.4/0" must all still be
// blocked, since the guard fails closed rather than open.
func TestNetworkCreateSecurityGroupRuleWorldOpenIngressRequiresYes(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantBlocked bool
	}{
		{
			"flag path, IPv4 world-open ingress, no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "ingress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "0.0.0.0/0"},
			true,
		},
		{
			"flag path, IPv4 world-open ingress, with --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "ingress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "0.0.0.0/0", "--yes"},
			false,
		},
		{
			"flag path, IPv6 world-open ingress, no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "ingress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "::/0"},
			true,
		},
		{
			"flag path, narrower ingress prefix needs no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "ingress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "10.0.0.0/8"},
			false,
		},
		{
			"flag path, world-open egress needs no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "egress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "0.0.0.0/0"},
			false,
		},
		{
			"flag path, uppercase INGRESS world-open, no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "INGRESS", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "0.0.0.0/0"},
			true,
		},
		{
			"flag path, direction with a leading space fails closed, no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", " ingress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "0.0.0.0/0"},
			true,
		},
		{
			"flag path, length-0 prefix with host bits set, no --yes",
			[]string{"--security-group-id", "secg-1", "--direction", "ingress", "--protocol", "tcp",
				"--port-range-min", "443", "--remote-ip-prefix", "1.2.3.4/0"},
			true,
		},
		{
			"cli-input-json path, IPv4 world-open ingress, no --yes",
			[]string{"--cli-input-json", `{"SecurityGroupID":"secg-1","Direction":"ingress","Protocol":"tcp","PortRangeMin":443,"RemoteIPPrefix":"0.0.0.0/0"}`},
			true,
		},
		{
			"cli-input-json path, IPv4 world-open ingress, with --yes",
			[]string{"--cli-input-json", `{"SecurityGroupID":"secg-1","Direction":"ingress","Protocol":"tcp","PortRangeMin":443,"RemoteIPPrefix":"0.0.0.0/0"}`, "--yes"},
			false,
		},
		{
			"cli-input-json path, IPv6 world-open ingress, no --yes",
			[]string{"--cli-input-json", `{"SecurityGroupID":"secg-1","Direction":"ingress","Protocol":"tcp","PortRangeMin":443,"RemoteIPPrefix":"::/0"}`},
			true,
		},
		{
			"cli-input-json path, uppercase INGRESS world-open, no --yes",
			[]string{"--cli-input-json", `{"SecurityGroupID":"secg-1","Direction":"INGRESS","Protocol":"tcp","PortRangeMin":443,"RemoteIPPrefix":"0.0.0.0/0"}`},
			true,
		},
		{
			"cli-input-json path, direction with a leading space fails closed, no --yes",
			[]string{"--cli-input-json", `{"SecurityGroupID":"secg-1","Direction":" ingress","Protocol":"tcp","PortRangeMin":443,"RemoteIPPrefix":"0.0.0.0/0"}`},
			true,
		},
		{
			"cli-input-json path, length-0 prefix with host bits set, no --yes",
			[]string{"--cli-input-json", `{"SecurityGroupID":"secg-1","Direction":"ingress","Protocol":"tcp","PortRangeMin":443,"RemoteIPPrefix":"1.2.3.4/0"}`},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/secgroups/secg-1/secgroupRules": jsonHandler(http.StatusCreated, createSecurityGroupRuleJSON("secr-1", "secg-1")),
			})
			root, _, stderr := newSvcRoot(t, fixture)
			args := append([]string{
				"--region", "hcm-3", "--project-id", "proj-1",
				"network", "create-security-group-rule",
			}, tt.args...)
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if tt.wantBlocked {
				if err == nil {
					t.Fatal("expected the guard to refuse this command")
				}
				if got := exitCode(err); got != 2 {
					t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
				}
				if n := fixture.requestCount(); n != 0 {
					t.Fatalf("requestCount = %d, want 0", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("create-security-group-rule: %v (stderr=%s)", err, stderr.String())
			}
			if n := fixture.requestCount(); n != 1 {
				t.Fatalf("requestCount = %d, want 1", n)
			}
		})
	}
}

// TestNetworkCreateSecurityGroupRuleEndToEnd checks the flag-to-body mapping
// for create-security-group-rule's own fields, and that the settled rule
// comes back on stdout.
func TestNetworkCreateSecurityGroupRuleEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1/secgroupRules": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(createSecurityGroupRuleJSON("secr-1", "secg-1")))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-security-group-rule",
		"--security-group-id", "secg-1", "--direction", "ingress", "--protocol", "tcp",
		"--port-range-min", "22", "--remote-ip-prefix", "203.0.113.0/24",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-security-group-rule: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	want := map[string]any{
		"direction": "ingress", "protocol": "tcp", "etherType": "IPv4",
		"portRangeMin": float64(22), "portRangeMax": float64(22),
		"remoteIpPrefix": "203.0.113.0/24", "securityGroupId": "secg-1",
	}
	for k, v := range want {
		if decoded[k] != v {
			t.Fatalf("body[%s] = %v, want %v (body=%s)", k, decoded[k], v, body)
		}
	}
	if got := stdout.String(); !strings.Contains(got, `"ID": "secr-1"`) {
		t.Fatalf("stdout = %s, want the created rule", got)
	}
}

// TestNetworkDeleteSecurityGroupRuleRequiresYesWithZeroRequests checks the
// destructive --yes guard: without it, nothing is sent, not even the
// pre-delete rule list.
func TestNetworkDeleteSecurityGroupRuleRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1/secGroupRules": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-security-group-rule",
		"--security-group-id", "secg-1", "--security-group-rule-id", "secr-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDeleteSecurityGroupRuleNotFoundInGroupWithNoDelete checks that
// a rule ID absent from the named group's own rule list stops the delete
// before any DELETE, returning NotFound.
func TestNetworkDeleteSecurityGroupRuleNotFoundInGroupWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1/secGroupRules": jsonHandler(http.StatusOK, listSecurityGroupRulesJSON("other-rule")),
		"/v2/proj-1/secgroups/secg-1/secgroupRules/secr-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-security-group-rule",
		"--security-group-id", "secg-1", "--security-group-rule-id", "secr-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotFound" {
		t.Fatalf("Code = %q, want NotFound (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 4 {
		t.Fatalf("exitCode = %d, want 4", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the rule list only)", n)
	}
}

// TestNetworkDeleteSecurityGroupRuleWithYesSendsListThenDelete checks the
// success path's exact request sequence: the rule list, then the DELETE.
func TestNetworkDeleteSecurityGroupRuleWithYesSendsListThenDelete(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups/secg-1/secGroupRules": jsonHandler(http.StatusOK, listSecurityGroupRulesJSON("secr-1")),
		"/v2/proj-1/secgroups/secg-1/secgroupRules/secr-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected method %s, want DELETE", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-security-group-rule",
		"--security-group-id", "secg-1", "--security-group-rule-id", "secr-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-security-group-rule: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (rule list, delete)", n)
	}
}

// TestNetworkSecurityGroupWritesReadOnlyRefusedWithZeroRequests checks that
// a profile's own read_only setting refuses all five network write
// commands, before any request. The two destructive commands also pass
// --yes, and create-security-group-rule uses a narrow prefix that the
// world-open guard lets through, so the read-only refusal is unambiguously
// the reason in every case, not a missing --yes or the guard.
func TestNetworkSecurityGroupWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-security-group", []string{"create-security-group", "--name", "web"}},
		{"update-security-group", []string{"update-security-group", "--security-group-id", "secg-1", "--description", "new"}},
		{"delete-security-group", []string{"delete-security-group", "--security-group-id", "secg-1", "--yes"}},
		{"create-security-group-rule", []string{"create-security-group-rule", "--security-group-id", "secg-1",
			"--direction", "ingress", "--protocol", "tcp", "--port-range-min", "22", "--remote-ip-prefix", "10.0.0.0/8"}},
		{"delete-security-group-rule", []string{"delete-security-group-rule", "--security-group-id", "secg-1",
			"--security-group-rule-id", "secr-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/secgroups": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/secgroups/secg-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/secgroups/secg-1/servers": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/secgroups/secg-1/secgroupRules": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/secgroups/secg-1/secGroupRules": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/secgroups/secg-1/secgroupRules/secr-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "network"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

// TestNetworkCreateSecurityGroupCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests
// checks --cli-input-json's strictness for a network write: a key that
// names no Input field is refused before any request, rather than silently
// ignored.
func TestNetworkCreateSecurityGroupCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/secgroups": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-security-group", "--name", "web",
		"--cli-input-json", `{"Bogus":"x"}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- route tables and routes ---

// routeTableGetJSON builds GetRouteTable's own response envelope: data.uuid,
// name, networkId, and routes, each holding only destinationCidrBlock and
// target, the two fields AddRoute's and RemoveRoute's read-merge reads back
// and resends. Every table it builds is ACTIVE and belongs to "vpc-1", the
// only VPC and the only status every test in this file needs, matching
// routeTableJSON in network's own test file.
func routeTableGetJSON(uuid, name string, routes ...[2]string) string {
	type routeJSON struct {
		DestinationCIDRBlock string `json:"destinationCidrBlock"`
		Target               string `json:"target"`
		Status               string `json:"status"`
	}
	rs := make([]routeJSON, len(routes))
	for i, r := range routes {
		rs[i] = routeJSON{DestinationCIDRBlock: r[0], Target: r[1], Status: "ACTIVE"}
	}
	body := map[string]any{"uuid": uuid, "name": name, "status": "ACTIVE", "networkId": "vpc-1", "routes": rs}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// scriptedRouteTableGetHandler answers each successive GET with the next
// body of bodies, holding on the last one once the script runs out, so a
// pre-write read and a post-write confirm read can each see a different
// snapshot of the same table.
func scriptedRouteTableGetHandler(bodies ...string) func(http.ResponseWriter, *http.Request) {
	i := 0
	return func(w http.ResponseWriter, _ *http.Request) {
		body := bodies[i]
		if i < len(bodies)-1 {
			i++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func TestNetworkGetRouteTableEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": jsonHandler(http.StatusOK,
			routeTableGetJSON("rt-1", "rt-web", [2]string{"10.251.200.0/24", "10.0.0.10"})),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "get-route-table", "--route-table-id", "rt-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-route-table: %v (stderr=%s)", err, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, `"UUID": "rt-1"`) || !strings.Contains(got, `"Name": "rt-web"`) ||
		!strings.Contains(got, `"DestinationCIDRBlock": "10.251.200.0/24"`) {
		t.Fatalf("stdout = %s, want the route table with its route", got)
	}
}

// TestNetworkCreateRouteTableEndToEnd checks the flag-to-body mapping
// (--vpc-id and --name become networkId and name) and that the settled,
// already-ACTIVE table comes back on stdout.
func TestNetworkCreateRouteTableEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-1"}`))
		},
		"/v2/proj-1/route-table/rt-1": jsonHandler(http.StatusOK, routeTableGetJSON("rt-1", "rt-web")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-route-table", "--vpc-id", "vpc-1", "--name", "rt-web",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-route-table: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "rt-web" || decoded["networkId"] != "vpc-1" {
		t.Fatalf("body = %s, want name=rt-web and networkId=vpc-1", body)
	}
	got := stdout.String()
	if !strings.Contains(got, `"UUID": "rt-1"`) || !strings.Contains(got, `"Status": "ACTIVE"`) {
		t.Fatalf("stdout = %s, want the settled ACTIVE table", got)
	}
}

// TestNetworkCreateRouteTableCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests
// checks --cli-input-json's strictness: a key that names no Input field is
// refused before any request.
func TestNetworkCreateRouteTableCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-route-table", "--vpc-id", "vpc-1", "--name", "rt-web",
		"--cli-input-json", `{"Bogus":"x"}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDeleteRouteTableRequiresYesWithZeroRequests checks the
// destructive --yes guard: without it, nothing is sent, not even the
// pre-delete read.
func TestNetworkDeleteRouteTableRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-route-table", "--route-table-id", "rt-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDeleteRouteTableWithYesSendsGetVPCSubnetsDeleteThenConfirms
// checks the success path's exact request sequence: the table read, the
// VPC read, the subnets read, the DELETE, then the confirm read that finds
// the table gone.
func TestNetworkDeleteRouteTableWithYesSendsGetVPCSubnetsDeleteThenConfirms(t *testing.T) {
	deleted := false
	confirmed := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				if !deleted {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(routeTableGetJSON("rt-1", "custom")))
					return
				}
				confirmed = true
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusAccepted)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/networks/vpc-1":         jsonHandler(http.StatusOK, `{"id":"vpc-1","routeTableId":"rt-main"}`),
		"/v2/proj-1/networks/vpc-1/subnets": jsonHandler(http.StatusOK, `[]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-route-table", "--route-table-id", "rt-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-route-table: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if !confirmed {
		t.Fatal("the post-delete confirm read never ran")
	}
	if n := fixture.requestCount(); n != 5 {
		t.Fatalf("requestCount = %d, want 5 (table read, vpc read, subnets read, delete, confirm read)", n)
	}
}

// TestNetworkDeleteRouteTableNamedBySubnetExitsResourceInUseWithNoDelete
// checks the ErrInUse guard end to end: a subnet naming this table as its
// own stops the delete before any DELETE, with the ResourceInUse code.
func TestNetworkDeleteRouteTableNamedBySubnetExitsResourceInUseWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-2":       jsonHandler(http.StatusOK, routeTableGetJSON("rt-2", "custom")),
		"/v2/proj-1/networks/vpc-1":         jsonHandler(http.StatusOK, `{"id":"vpc-1","routeTableId":"rt-1"}`),
		"/v2/proj-1/networks/vpc-1/subnets": jsonHandler(http.StatusOK, `[{"uuid":"sub-1","routeTableUuid":"rt-2"}]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-route-table", "--route-table-id", "rt-2",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 3 {
		t.Fatalf("requestCount = %d, want 3 (table read, vpc read, subnets read, no delete)", n)
	}
}

// TestNetworkDeleteRouteTableMainTableWithDependentSubnetExitsDefaultResourceWithNoDelete
// checks the ErrDefaultResource guard end to end: a VPC's main route table,
// with a subnet that names no table of its own and so relies on it, stops
// the delete before any DELETE, with the DefaultResource code.
func TestNetworkDeleteRouteTableMainTableWithDependentSubnetExitsDefaultResourceWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1":       jsonHandler(http.StatusOK, routeTableGetJSON("rt-1", "main")),
		"/v2/proj-1/networks/vpc-1":         jsonHandler(http.StatusOK, `{"id":"vpc-1","routeTableId":"rt-1"}`),
		"/v2/proj-1/networks/vpc-1/subnets": jsonHandler(http.StatusOK, `[{"uuid":"sub-1","routeTableUuid":""}]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-route-table", "--route-table-id", "rt-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "DefaultResource" {
		t.Fatalf("Code = %q, want DefaultResource (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 3 {
		t.Fatalf("requestCount = %d, want 3 (table read, vpc read, subnets read, no delete)", n)
	}
}

// TestNetworkAddRouteRequiresYesWithZeroRequests checks
// requireYesToChangeRoutes: without --yes, add-route sends nothing, not
// even the pre-write read.
func TestNetworkAddRouteRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "add-route", "--route-table-id", "rt-1",
		"--destination-cidr", "10.251.200.0/24", "--target", "10.0.0.10",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkAddRouteEndToEndSendsExistingPlusNewRoute checks the read-merge
// body: the PUT holds the route the pre-write read found plus the one
// --target names, and the settled table (from the post-write confirm read)
// comes back with Changed true.
func TestNetworkAddRouteEndToEndSendsExistingPlusNewRoute(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": scriptedRouteTableGetHandler(
			routeTableGetJSON("rt-1", "rt-web", [2]string{"10.251.100.0/24", "10.0.0.5"}),
			routeTableGetJSON("rt-1", "rt-web",
				[2]string{"10.251.100.0/24", "10.0.0.5"}, [2]string{"10.251.200.0/24", "10.0.0.10"}),
		),
		"/v2/proj-1/route-table/rt-1/routes": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "add-route", "--route-table-id", "rt-1",
		"--destination-cidr", "10.251.200.0/24", "--target", "10.0.0.10",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("add-route: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		Routes []struct {
			DestinationCIDRBlock string `json:"destinationCidrBlock"`
			Target               string `json:"target"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.Routes) != 2 {
		t.Fatalf("routes in body = %+v, want 2 entries (the existing route plus the new one)", decoded.Routes)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkAddRouteNoOpWhenAlreadyPresentSendsNoPUT checks the design's
// no-op rule: adding a route that already exists with the same target sends
// no PUT and reports Changed false.
func TestNetworkAddRouteNoOpWhenAlreadyPresentSendsNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": jsonHandler(http.StatusOK,
			routeTableGetJSON("rt-1", "rt-web", [2]string{"10.251.200.0/24", "10.0.0.10"})),
		"/v2/proj-1/route-table/rt-1/routes": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s (a no-op add must send no PUT)", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "add-route", "--route-table-id", "rt-1",
		"--destination-cidr", "10.251.200.0/24", "--target", "10.0.0.10",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("add-route: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", got)
	}
}

// TestNetworkRemoveRouteRequiresYesWithZeroRequests checks
// requireYesToChangeRoutes for remove-route: without --yes, nothing is
// sent, not even the pre-write read.
func TestNetworkRemoveRouteRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "remove-route", "--route-table-id", "rt-1", "--destination-cidr", "10.251.200.0/24",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkRemoveRouteEndToEndSendsRemainingRoutes checks the read-merge
// body: the PUT holds every route the pre-write read found except the one
// named, and the settled table comes back with Changed true.
func TestNetworkRemoveRouteEndToEndSendsRemainingRoutes(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": scriptedRouteTableGetHandler(
			routeTableGetJSON("rt-1", "rt-web",
				[2]string{"10.251.100.0/24", "10.0.0.5"}, [2]string{"10.251.200.0/24", "10.0.0.10"}),
			routeTableGetJSON("rt-1", "rt-web", [2]string{"10.251.100.0/24", "10.0.0.5"}),
		),
		"/v2/proj-1/route-table/rt-1/routes": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-route", "--route-table-id", "rt-1", "--destination-cidr", "10.251.200.0/24",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("remove-route: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		Routes []struct {
			DestinationCIDRBlock string `json:"destinationCidrBlock"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.Routes) != 1 || decoded.Routes[0].DestinationCIDRBlock != "10.251.100.0/24" {
		t.Fatalf("routes in body = %+v, want only 10.251.100.0/24", decoded.Routes)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkRemoveRouteNotFoundWhenMissingSendsNoPUT checks the design's
// missing-route rule: removing a destination the table does not have sends
// no PUT and returns NotFound.
func TestNetworkRemoveRouteNotFoundWhenMissingSendsNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/route-table/rt-1": jsonHandler(http.StatusOK,
			routeTableGetJSON("rt-1", "rt-web", [2]string{"10.251.100.0/24", "10.0.0.5"})),
		"/v2/proj-1/route-table/rt-1/routes": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s (a missing route must send no PUT)", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-route", "--route-table-id", "rt-1", "--destination-cidr", "10.251.200.0/24",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotFound" {
		t.Fatalf("Code = %q, want NotFound (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 4 {
		t.Fatalf("exitCode = %d, want 4", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-write read only)", n)
	}
}

// TestNetworkRouteTableWritesReadOnlyRefusedWithZeroRequests checks that a
// profile's own read_only setting refuses create-route-table,
// delete-route-table, add-route, and remove-route, before any request. The
// destructive and --yes-guarded commands also pass --yes, so the read-only
// refusal is unambiguously the reason in every case.
func TestNetworkRouteTableWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-route-table", []string{"create-route-table", "--vpc-id", "vpc-1", "--name", "rt-web"}},
		{"delete-route-table", []string{"delete-route-table", "--route-table-id", "rt-1", "--yes"}},
		{"add-route", []string{"add-route", "--route-table-id", "rt-1",
			"--destination-cidr", "10.251.200.0/24", "--target", "10.0.0.10", "--yes"}},
		{"remove-route", []string{"remove-route", "--route-table-id", "rt-1",
			"--destination-cidr", "10.251.200.0/24", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/route-table": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/route-table/rt-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/route-table/rt-1/routes": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "network"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}
