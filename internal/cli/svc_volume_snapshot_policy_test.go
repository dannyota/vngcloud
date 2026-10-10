package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
)

// Synthetic rows exercise CLI routing and output without account data.
const snapshotBackendBody = `{"items":[{"id":"backend-1","name":"HCM-03"}]}`
const snapshotPolicyBody = `{"items":[{"id":"policy-1","name":"DEFAULT","policyType":"DEFAULT","config":{"weeklyEnabled":true,"monthlyEnabled":false}}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`

func newSnapshotReadRoot(t *testing.T, fixture *svcFixture) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	server := httptest.NewServer(fixture.mux)
	t.Cleanup(server.Close)
	withTestOptions(t,
		vngcloud.WithStaticToken("test-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{
			VServerBackup: server.URL + "/vserver/vbackup-gateway/",
		}),
		vngcloud.WithHTTPClient(&http.Client{Transport: refusingTransport{base: server.Client().Transport}}),
	)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return newRootCmd(strings.NewReader(""), stdout, stderr), stdout, stderr
}

func TestVolumeSnapshotReadsRouteAndOutput(t *testing.T) {
	tests := []struct {
		name, path, body, query, id string
		args                        []string
	}{
		{"backends", "/vserver/vbackup-gateway/v1/backends", snapshotBackendBody,
			"backend=HCM-03", "backend-1", []string{"list-snapshot-backends", "--name", "HCM-03"}},
		{"backends-json", "/vserver/vbackup-gateway/v1/backends", snapshotBackendBody,
			"backend=HCM-03", "backend-1", []string{"list-snapshot-backends", "--cli-input-json", `{"Name":"HCM-03"}`}},
		{"policies-defaults", "/vserver/vbackup-gateway/v1/snapshot-policies", snapshotPolicyBody,
			"backendId=backend-1&page=1&projectId=proj-1&size=10", "policy-1", []string{"list-snapshot-policies", "--backend-id", "backend-1"}},
		{"policies-page", "/vserver/vbackup-gateway/v1/snapshot-policies", snapshotPolicyBody,
			"backendId=backend-1&page=2&projectId=proj-1&size=3", "policy-1", []string{"list-snapshot-policies", "--backend-id", "backend-1", "--page", "2", "--size", "3"}},
		{"policies-json", "/vserver/vbackup-gateway/v1/snapshot-policies", snapshotPolicyBody,
			"backendId=backend-1&page=3&projectId=proj-1&size=4", "policy-1", []string{"list-snapshot-policies", "--cli-input-json", `{"BackendID":"backend-1","Page":3,"Size":4}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet || r.URL.Path != tt.path || r.URL.RawQuery != tt.query {
						t.Errorf("request = %s %s?%s, want GET %s?%s", r.Method, r.URL.Path, r.URL.RawQuery, tt.path, tt.query)
					}
					jsonHandler(http.StatusOK, tt.body)(w, r)
				},
			})
			root, stdout, stderr := newSnapshotReadRoot(t, fixture)
			args := []string{"--profile", "agent", "--output", "json"}
			if tt.name == "backends" || tt.name == "policies-defaults" {
				args = append(args, "--read-only")
			}
			args = append(args, "volume")
			root.SetArgs(append(args, tt.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
			}
			if got := fixture.requestCount(); got != 1 {
				t.Fatalf("requests = %d, want one page", got)
			}
			var out struct {
				Items                                []struct{ ID string }
				Page, PageSize, TotalPage, TotalItem int
			}
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("JSON output: %v", err)
			}
			if len(out.Items) != 1 || out.Items[0].ID != tt.id || !bytes.Contains(stdout.Bytes(), []byte(`"Items"`)) {
				t.Fatalf("output = %s, want Items with ID %s", stdout.String(), tt.id)
			}
			if strings.HasPrefix(tt.name, "policies") && (out.Page != 1 || out.PageSize != 10 || out.TotalPage != 1 || out.TotalItem != 1) {
				t.Fatalf("page fields = %+v, want the returned pagination", out)
			}
		})
	}
}

func TestVolumeSnapshotReadsRejectBeforeRequest(t *testing.T) {
	tests := []struct {
		name, region, message string
		args                  []string
	}{
		{"missing-name", "hcm-3", "--name", []string{"list-snapshot-backends"}},
		{"missing-backend-id", "hcm-3", "--backend-id", []string{"list-snapshot-policies"}},
		{"malformed-backend-id", "hcm-3", "BackendID", []string{"list-snapshot-policies", "--backend-id", "../backend"}},
		{"han-backends", "han-1", "supported snapshot region", []string{"list-snapshot-backends", "--name", "HCM-03"}},
		{"han-policies", "han-1", "supported snapshot region", []string{"list-snapshot-policies", "--backend-id", "backend-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			root, stdout, stderr := newSnapshotReadRoot(t, fixture)
			root.SetArgs(append([]string{"--profile", "agent", "--region", tt.region, "volume"}, tt.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 2 {
				t.Fatalf("error = %v, exit = %d, want 2", err, exitCode(err))
			}
			printError(stderr, err)
			if !strings.Contains(stderr.String(), tt.message) {
				t.Fatalf("stderr = %s, want %q", stderr.String(), tt.message)
			}
			if got := fixture.requestCount(); got != 0 || stdout.Len() != 0 {
				t.Fatalf("requests = %d, stdout = %s, want no request or output", got, stdout.String())
			}
		})
	}
}

func TestVolumeSnapshotReadsWithholdErrorBody(t *testing.T) {
	for _, command := range []struct {
		name string
		args []string
	}{
		{"list-snapshot-backends", []string{"--name", "HCM-03"}},
		{"list-snapshot-policies", []string{"--backend-id", "backend-1"}},
	} {
		for _, status := range []int{http.StatusForbidden, http.StatusOK} {
			t.Run(command.name+"/"+http.StatusText(status), func(t *testing.T) {
				const marker = "upstream-secret-marker"
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/": jsonHandler(status, `{"token":"`+marker+`"}`),
				})
				root, stdout, stderr := newSnapshotReadRoot(t, fixture)
				args := []string{"--profile", "agent", "--debug", "volume", command.name}
				root.SetArgs(append(args, command.args...))
				err := root.ExecuteContext(context.Background())
				if err == nil {
					t.Fatal("expected an error")
				}
				printError(stderr, err)
				if strings.Contains(err.Error()+stdout.String()+stderr.String(), marker) {
					t.Fatal("upstream body reached error or output")
				}
				if stdout.Len() != 0 || fixture.requestCount() != 1 {
					t.Fatalf("stdout=%s stderr=%s requests=%d", stdout.String(), stderr.String(), fixture.requestCount())
				}
				if status == http.StatusOK && !strings.Contains(stderr.String(), "body withheld") {
					t.Fatalf("stderr = %s, want body withheld", stderr.String())
				}
			})
		}
	}
}
