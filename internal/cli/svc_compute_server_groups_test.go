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

// serverGroupJSON builds one ServerGroup's own {"data": {...}} envelope, the
// shape GetServerGroup, CreateServerGroup, and UpdateServerGroup's confirm
// read all decode, always under uuid "sg-1", the only group ID every test in
// this file uses. servers is nil for a group with no members.
func serverGroupJSON(name, description, policyID string, servers []map[string]any) string {
	body := map[string]any{
		"uuid":        "sg-1",
		"name":        name,
		"description": description,
		"policyId":    policyID,
		"policyName":  "anti-affinity",
		"createdAt":   "2026-01-01T00:00:00Z",
		"servers":     servers,
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// serverGroupNotFoundJSON builds GetServerGroup's own response for an
// unknown or deleted group: status 200 with "data" null, confirmed live
// (see compute.GetServerGroup), never a 404.
const serverGroupNotFoundJSON = `{"data":null}`

// listServerGroupsJSON builds ListServerGroups' own envelope from one raw
// group body (as serverGroupJSON's "data" value), the shape
// DeleteServerGroup's pre-delete scan reads.
func listServerGroupsJSON(groups ...map[string]any) string {
	b, err := json.Marshal(map[string]any{
		"listData": groups, "page": 1, "pageSize": 10000, "totalPage": 1, "totalItem": len(groups),
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// rawServerGroup builds one raw group object (not wrapped in "data"), for
// use inside listServerGroupsJSON.
func rawServerGroup(uuid, name string, servers []map[string]any) map[string]any {
	return map[string]any{
		"uuid": uuid, "name": name, "description": "", "policyId": "policy-1",
		"policyName": "anti-affinity", "createdAt": "2026-01-01T00:00:00Z", "servers": servers,
	}
}

// TestComputeCreateServerGroupSendsNameAndPolicyID drives a real
// create-server-group call and checks the POST body: description is sent
// even when left empty, and the create response, which already carries the
// full group, is printed as is with no confirm read.
func TestComputeCreateServerGroupSendsNameAndPolicyID(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(serverGroupJSON("web", "", "policy-1", nil)))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"compute", "create-server-group", "--name", "web", "--policy-id", "policy-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-server-group: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "web" || decoded["policyId"] != "policy-1" || decoded["description"] != "" {
		t.Fatalf("body = %s, want name=web policyId=policy-1 description=\"\"", body)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (create sends no confirm read)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"Name": "web"`) || !strings.Contains(got, `"PolicyID": "policy-1"`) {
		t.Fatalf("stdout = %s, want the created group printed", got)
	}
}

// TestComputeCreateServerGroupCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests
// checks --cli-input-json's strictness: a key that names no Input field is
// refused before any request, rather than silently ignored.
func TestComputeCreateServerGroupCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"compute", "create-server-group", "--name", "web", "--policy-id", "policy-1",
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

// TestComputeUpdateServerGroupMergesUnchangedField checks that
// update-server-group reads the group first and resends whichever field the
// caller leaves unset, for each field in turn.
func TestComputeUpdateServerGroupMergesUnchangedField(t *testing.T) {
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
				"/v2/proj-1/serverGroups/sg-1": func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(serverGroupJSON("old-name", "desc", "policy-1", nil)))
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
				"compute", "update-server-group", "--server-group-id", "sg-1",
			}, tt.args...)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("update-server-group: %v (stderr=%s)", err, stderr.String())
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

// TestComputeUpdateServerGroupEmptyUpdateIsUsageErrorWithZeroRequests checks
// that update-server-group with neither --name nor --description is
// refused before any request: the SDK's own check runs before its first
// GetServerGroup read.
func TestComputeUpdateServerGroupEmptyUpdateIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups/sg-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"compute", "update-server-group", "--server-group-id", "sg-1",
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

// TestComputeGetServerGroupUnknownIDIsNotFound checks that get-server-group
// treats a 200 response with "data" null, the server's own shape for a
// deleted or unknown group, as NotFound rather than printing an empty
// group.
func TestComputeGetServerGroupUnknownIDIsNotFound(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups/sg-1": jsonHandler(http.StatusOK, serverGroupNotFoundJSON),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"compute", "get-server-group", "--server-group-id", "sg-1",
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
}

// TestComputeDeleteServerGroupRequiresYesWithZeroRequests checks the
// destructive --yes guard: without it, nothing is sent, not even the
// pre-delete list scan.
func TestComputeDeleteServerGroupRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"compute", "delete-server-group", "--server-group-id", "sg-1",
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

// TestComputeDeleteServerGroupWithYesSendsListThenDelete checks the success
// path's exact request sequence: the list scan, then the DELETE.
func TestComputeDeleteServerGroupWithYesSendsListThenDelete(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(listServerGroupsJSON(rawServerGroup("sg-1", "web", nil))))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/serverGroups/sg-1": func(w http.ResponseWriter, r *http.Request) {
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
		"compute", "delete-server-group", "--server-group-id", "sg-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-server-group: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (list scan, delete)", n)
	}
}

// TestComputeDeleteServerGroupInUseWithServersNoDelete checks that a group
// the pre-delete list scan finds with an attached server stops the delete
// before any DELETE, and that the CLI reports ServerGroupInUse.
func TestComputeDeleteServerGroupInUseWithServersNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups": jsonHandler(http.StatusOK, listServerGroupsJSON(
			rawServerGroup("sg-1", "web", []map[string]any{{"name": "srv-1", "uuid": "server-1"}}))),
		"/v2/proj-1/serverGroups/sg-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"compute", "delete-server-group", "--server-group-id", "sg-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ServerGroupInUse" {
		t.Fatalf("Code = %q, want ServerGroupInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the list scan only)", n)
	}
}

// TestComputeServerGroupWritesReadOnlyRefusedWithZeroRequests checks that
// read-only refuses every server group write before any request. Every case
// also carries --yes for delete-server-group, so the read-only refusal is
// unambiguously the reason, not a missing --yes.
func TestComputeServerGroupWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-server-group", []string{"create-server-group", "--name", "web", "--policy-id", "policy-1"}},
		{"update-server-group", []string{"update-server-group", "--server-group-id", "sg-1", "--description", "new"}},
		{"delete-server-group", []string{"delete-server-group", "--server-group-id", "sg-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/serverGroups": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/serverGroups/sg-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "compute"}, tc.args...))
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
