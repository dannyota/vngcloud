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

func newVKSRoot(t *testing.T, fixture *svcFixture, profile bool) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := withCleanEnv(t)
	if profile {
		writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	}
	server := httptest.NewServer(fixture.mux)
	t.Cleanup(server.Close)
	withTestOptions(t,
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VKS: server.URL + "/vks-api/"}),
		vngcloud.WithHTTPClient(server.Client()),
		vngcloud.WithStaticToken("test-token"),
	)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return newRootCmd(strings.NewReader(""), stdout, stderr), stdout, stderr
}

var vksReadCases = []struct {
	command string
	route   string
	body    string
	want    string
}{
	{"list-clusters", "clusters", `{"items":[{"id":"cls-1","name":"example","status":"RUNNING","version":"v1.32.0"}],"page":0,"pageSize":10,"total":1}`, `{"Items":[{"ID":"cls-1","Name":"example","Status":"RUNNING","Version":"v1.32.0"}],"Page":0,"PageSize":10,"TotalItem":1,"TotalPage":1}`},
	{"list-cluster-versions", "cluster-versions", `[{"version":"v1.32.0","enable":false,"stage":"STABLE"}]`, `{"Items":[{"Version":"v1.32.0","Enable":false,"Stage":"STABLE"}]}`},
	{"get-quota", "quota", `{"maxClusters":5,"numClusters":0,"maxNodeGroupsPerCluster":10,"maxNodesPerNodeGroup":20}`, `{"Quota":{"MaxClusters":5,"NumClusters":0,"MaxNodeGroupsPerCluster":10,"MaxNodesPerNodeGroup":20}}`},
}

func TestVKSReads(t *testing.T) {
	for _, tc := range vksReadCases {
		for _, profile := range []bool{false, true} {
			name := tc.command + "/flag"
			if profile {
				name = tc.command + "/profile"
			}
			t.Run(name, func(t *testing.T) {
				path := "/vks-api/v1/" + tc.route
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){path: jsonHandler(http.StatusOK, tc.body)})
				root, stdout, stderr := newVKSRoot(t, fixture, profile)
				args := []string{"--region", "hcm-3", "--read-only"}
				if profile {
					args = []string{"--profile", "agent"}
				}
				root.SetArgs(append(args, "--project-id", "unused-project", "vks", tc.command))
				if err := root.ExecuteContext(context.Background()); err != nil {
					t.Fatalf("execute: %v (stderr=%s)", err, stderr)
				}
				if method, ok := fixture.methodFor(path); !ok || method != http.MethodGet {
					t.Fatalf("method = %q, present=%v, want GET", method, ok)
				}
				wantQuery := ""
				if tc.command == "list-clusters" {
					wantQuery = "page=0&pageSize=10"
				}
				if query, _ := fixture.queryFor(path); query != wantQuery {
					t.Fatalf("query = %q, want %q", query, wantQuery)
				}
				if fixture.requestCount() != 1 {
					t.Fatalf("requests = %d, want 1", fixture.requestCount())
				}
				var got, want map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
					t.Fatalf("decode output: %v", err)
				}
				if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
					t.Fatal(err)
				}
				assertVKSJSONContains(t, got, want)
			})
		}
	}
}

func assertVKSJSONContains(t *testing.T, got, want any) {
	t.Helper()
	switch want := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("got %v, want object", got)
		}
		for key, value := range want {
			assertVKSJSONContains(t, object[key], value)
		}
	case []any:
		items, ok := got.([]any)
		if !ok || len(items) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i, value := range want {
			assertVKSJSONContains(t, items[i], value)
		}
	default:
		if got != want {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestVKSClusterPagination(t *testing.T) {
	for _, args := range [][]string{
		{"--page", "1", "--size", "2"},
		{"--cli-input-json", `{"Page":1,"Size":2}`},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := "/vks-api/v1/clusters"
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				path: jsonHandler(http.StatusOK, `{"items":[],"page":1,"pageSize":2,"total":0}`),
			})
			root, stdout, _ := newVKSRoot(t, fixture, false)
			root.SetArgs(append([]string{"--region", "han-1", "vks", "list-clusters"}, args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if query, _ := fixture.queryFor(path); query != "page=1&pageSize=2" {
				t.Fatalf("query = %q, want page=1&pageSize=2", query)
			}
			if !strings.Contains(stdout.String(), `"Items": []`) {
				t.Fatalf("output = %s, want empty Items array", stdout)
			}
		})
	}
}

func TestVKSValidationBeforeRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code string
	}{
		{"negative-page", []string{"--region", "hcm-3", "vks", "list-clusters", "--page", "-1"}, "InvalidUsage"},
	}
	for _, tc := range vksReadCases {
		tests = append(tests, struct {
			name string
			args []string
			code string
		}{tc.command + "/region", []string{"--region", "other", "vks", tc.command}, "InvalidConfig"})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			root, _, _ := newVKSRoot(t, fixture, false)
			root.SetArgs(tc.args)
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 2 || classify(err).Code != tc.code {
				t.Fatalf("error = %v, exit=%d, code=%s, want exit 2 and %s", err, exitCode(err), classify(err).Code, tc.code)
			}
			if tc.name == "negative-page" && !strings.Contains(err.Error(), "Page must be") {
				t.Fatalf("error = %v, want page validation", err)
			}
			if fixture.requestCount() != 0 {
				t.Fatalf("requests = %d, want 0", fixture.requestCount())
			}
		})
	}
}

func TestVKSUnknownSecretFieldsDropped(t *testing.T) {
	const marker = "vks-cli-credential-marker"
	for _, tc := range vksReadCases {
		body := vksBodyWithCredentials(t, tc.body, marker)
		wholeItemQuery := "Items"
		if tc.command == "get-quota" {
			wholeItemQuery = "Quota"
		}
		for _, output := range []string{"json", "table", "text"} {
			for _, query := range []string{"", wholeItemQuery} {
				for _, debug := range []bool{false, true} {
					name := tc.command + "/" + output + "/full"
					if query != "" {
						name = tc.command + "/" + output + "/" + query
					}
					if debug {
						name += "/debug"
					}
					t.Run(name, func(t *testing.T) {
						path := "/vks-api/v1/" + tc.route
						fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){path: jsonHandler(http.StatusOK, body)})
						root, stdout, stderr := newVKSRoot(t, fixture, false)
						args := []string{"--region", "hcm-3", "--output", output, "vks", tc.command}
						if query != "" {
							args = append(args, "--query", query)
						}
						if debug {
							args = append(args, "--debug")
						}
						root.SetArgs(args)
						if err := root.ExecuteContext(context.Background()); err != nil {
							t.Fatalf("execute: %v", err)
						}
						if fixture.requestCount() != 1 {
							t.Fatalf("requests = %d, want 1", fixture.requestCount())
						}
						if stdout.Len() == 0 {
							t.Fatal("missing inventory output")
						}
						if strings.Contains(stdout.String(), marker) || strings.Contains(stderr.String(), marker) {
							t.Fatal("output leaked credential marker")
						}
					})
				}
			}
		}
	}
}

func vksBodyWithCredentials(t *testing.T, body, marker string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatal(err)
	}
	var item map[string]any
	switch value := value.(type) {
	case []any:
		item = value[0].(map[string]any)
	case map[string]any:
		item = value
		if items, ok := value["items"].([]any); ok {
			item = items[0].(map[string]any)
		}
	default:
		t.Fatal("expected inventory object or array")
	}
	nested := make(map[string]any)
	for _, field := range []string{"kubeconfig", "token", "accessToken", "clientSecret", "privateKey", "certificate", "caCert", "password"} {
		item[field] = marker
		nested[field] = marker
	}
	item["config"] = nested
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVKSErrorBodyWithheld(t *testing.T) {
	const marker = "vks-error-credential-marker"
	for _, tc := range vksReadCases {
		for _, debug := range []bool{false, true} {
			name := tc.command
			if debug {
				name += "/debug"
			}
			t.Run(name, func(t *testing.T) {
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/vks-api/v1/" + tc.route: jsonHandler(http.StatusForbidden, `{"error":{"message":"`+marker+`","code":"`+marker+`"},"message":"`+marker+`","code":"`+marker+`","body":"`+marker+`"}`),
				})
				root, stdout, stderr := newVKSRoot(t, fixture, false)
				args := []string{"--region", "hcm-3", "vks", tc.command}
				if debug {
					args = append(args, "--debug")
				}
				root.SetArgs(args)
				err := root.ExecuteContext(context.Background())
				if err == nil || exitCode(err) != 1 {
					t.Fatalf("error = %v, exit=%d, want exit 1", err, exitCode(err))
				}
				printError(stderr, err)
				if fixture.requestCount() != 1 {
					t.Fatalf("requests = %d, want 1", fixture.requestCount())
				}
				if stdout.Len() != 0 || strings.Contains(stdout.String(), marker) || strings.Contains(stderr.String()+err.Error(), marker) {
					t.Fatal("error output leaked credential marker or success output")
				}
				if !strings.Contains(stderr.String(), "withheld") {
					t.Fatalf("stderr = %s, want withholding message", stderr)
				}
			})
		}
	}
}
