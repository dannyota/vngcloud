package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

const backupSecretMarker = "BACKUP-SECRET-MARKER"
const backupBackendsBody = `{"items":[{"id":"backend-1","name":"backend","token":"BACKUP-SECRET-MARKER","credentials":{"password":"BACKUP-SECRET-MARKER"}}],"page":null,"pageSize":null,"totalPages":1,"totalItems":1}`
const backupPoliciesBody = `{"items":[{"id":"policy-1","name":"policy","isDefault":true,"config":{"hourlyEnabled":true,"weeklyEnabled":true,"monthlyEnabled":true,"hourlyConfig":{"secret":"BACKUP-SECRET-MARKER"},"weeklyConfig":{"secret":"BACKUP-SECRET-MARKER"},"monthlyConfig":{"secret":"BACKUP-SECRET-MARKER"},"statusSendEmail":["BACKUP-SECRET-MARKER"]},"userId":"BACKUP-SECRET-MARKER","credentials":{"password":"BACKUP-SECRET-MARKER"},"errorMessage":"BACKUP-SECRET-MARKER"}],"page":1,"pageSize":200,"totalPages":2,"totalItems":3}`

func backupCLI(t *testing.T, handler http.Handler) func(...string) (int, string, string) {
	t.Helper()
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	withTestOptions(t,
		vngcloud.WithStaticToken("test-token"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{BackupCenter: server.URL + "/vbackup-gateway/"}),
		vngcloud.WithHTTPClient(&http.Client{Transport: refusingTransport{base: server.Client().Transport}}),
		vngcloud.WithRetry(0, 0),
	)
	return func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
}

func TestBackupLists(t *testing.T) {
	cases := []struct {
		name, route, query, body string
		args                     []string
		page, size               any
	}{
		{"backends", "/vbackup-gateway/v1/backends", "", backupBackendsBody, []string{"list-backends"}, nil, nil},
		{"policies defaults", "/vbackup-gateway/v1/backup-policies", "page=1&size=200", backupPoliciesBody, []string{"list-policies"}, float64(1), float64(200)},
		{"policies flags", "/vbackup-gateway/v1/backup-policies", "page=2&size=10", backupPoliciesBody, []string{"list-policies", "--page", "2", "--size", "10"}, float64(1), float64(200)},
		{"policies JSON", "/vbackup-gateway/v1/backup-policies", "page=3&size=20", backupPoliciesBody, []string{"list-policies", "--cli-input-json", `{"Page":3,"Size":20}`}, float64(1), float64(200)},
		{"backends JSON", "/vbackup-gateway/v1/backends", "", backupBackendsBody, []string{"list-backends", "--cli-input-json", `{}`}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			run := backupCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != tc.route || r.URL.RawQuery != tc.query {
					t.Errorf("request = %s %s, want GET %s?%s", r.Method, r.URL.RequestURI(), tc.route, tc.query)
				}
				if r.Header.Get("projectId") != "" {
					t.Error("project ID sent")
				}
				jsonHandler(http.StatusOK, tc.body)(w, r)
			}))
			args := append([]string{"--profile", "agent", "--read-only", "--project-id", "ignored-project", "--debug", "backup"}, tc.args...)
			code, stdout, stderr := run(args...)
			if code != 0 || calls != 1 {
				t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(stdout), &out); err != nil {
				t.Fatal(err)
			}
			items, ok := out["Items"].([]any)
			if !ok || len(items) != 1 || len(out) != 5 || out["Page"] != tc.page || out["PageSize"] != tc.size {
				t.Fatalf("output = %s", stdout)
			}
			item := items[0].(map[string]any)
			wantID := "policy-1"
			if tc.route == "/vbackup-gateway/v1/backends" {
				wantID = "backend-1"
			}
			if item["ID"] != wantID || out["TotalPage"] == nil || out["TotalItem"] == nil {
				t.Fatalf("output = %s", stdout)
			}
			if strings.Contains(stdout+stderr, backupSecretMarker) {
				t.Fatal("response secret escaped")
			}
		})
	}
}

func TestBackupLocalValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--region", "hcm-3", "backup", "list-policies", "--page", "-1"},
		{"--region", "hcm-3", "backup", "list-policies", "--size", "-1"},
		{"--region", "han-1", "backup", "list-policies"},
		{"--region", "han-1", "backup", "list-backends"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			run := backupCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			code, stdout, stderr := run(args...)
			if code != 2 || calls != 0 || stdout != "" {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, stdout, stderr)
			}
			want := "InvalidUsage"
			if args[1] == "han-1" {
				want = "InvalidConfig"
			}
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr = %s, want %s", stderr, want)
			}
		})
	}
}

func TestBackupBodiesWithheld(t *testing.T) {
	for _, command := range []string{"list-backends", "list-policies"} {
		for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(command+http.StatusText(status), func(t *testing.T) {
				run := backupCLI(t, http.HandlerFunc(jsonHandler(status, `{"code":"BACKUP-SECRET-MARKER","message":"BACKUP-SECRET-MARKER"}`)))
				code, stdout, stderr := run("--region", "hcm-3", "--debug", "backup", command)
				if code == 0 || stdout != "" {
					t.Fatalf("exit=%d stdout=%s", code, stdout)
				}
				if strings.Contains(stdout+stderr, backupSecretMarker) {
					t.Fatal("body secret escaped")
				}
				if !strings.Contains(stderr, "backup.List") {
					t.Fatalf("missing operation: %s", stderr)
				}
			})
		}
	}
}

func TestBackupPolicyQuery(t *testing.T) {
	run := backupCLI(t, http.HandlerFunc(jsonHandler(http.StatusOK, backupPoliciesBody)))
	code, stdout, stderr := run("--region", "hcm-3", "backup", "list-policies",
		"--query", "Items[].{ID:ID,Name:Name,Default:IsDefault}", "--output", "table")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	for _, value := range []string{"ID", "Name", "Default", "policy-1", "policy", "true"} {
		if !strings.Contains(stdout, value) {
			t.Fatalf("table lacks %q: %s", value, stdout)
		}
	}
}

func TestBackupOutputSecrecy(t *testing.T) {
	for _, tc := range []struct {
		command, route, body, id string
	}{
		{"list-backends", "/vbackup-gateway/v1/backends", backupBackendsBody, "backend-1"},
		{"list-policies", "/vbackup-gateway/v1/backup-policies", backupPoliciesBody, "policy-1"},
	} {
		for _, output := range []string{"json", "table", "text"} {
			for _, query := range []bool{false, true} {
				for _, debug := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/query=%t/debug=%t", tc.command, output, query, debug), func(t *testing.T) {
						calls := 0
						run := backupCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls++
							if r.Method != http.MethodGet || r.URL.Path != tc.route {
								t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, tc.route)
							}
							jsonHandler(http.StatusOK, tc.body)(w, r)
						}))
						args := []string{"--profile", "agent", "--output", output, "backup", tc.command}
						if query {
							args = append(args, "--query", "Items")
						}
						if debug {
							args = append(args, "--debug")
						}
						code, stdout, stderr := run(args...)
						if code != 0 || calls != 1 || !strings.Contains(stdout, tc.id) {
							t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, stdout, stderr)
						}
						if strings.Contains(stdout, backupSecretMarker) || strings.Contains(stderr, backupSecretMarker) {
							t.Fatal("response secret escaped")
						}
						if debug && !strings.Contains(stderr, "request") {
							t.Fatalf("missing debug request: %s", stderr)
						}
					})
				}
			}
		}
	}
}

func TestBackupErrorTokenSecrecy(t *testing.T) {
	for _, command := range []string{"list-backends", "list-policies"} {
		for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			for _, debug := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/debug=%t", command, status, debug), func(t *testing.T) {
					calls := 0
					var token string
					run := backupCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						authorization := r.Header.Get("Authorization")
						token = strings.TrimPrefix(authorization, "Bearer ")
						if token == "" || token == authorization {
							t.Error("missing bearer token")
						}
						body, err := json.Marshal(map[string]string{
							"code": "code-" + token, "message": "message-" + token, "body": "body-" + token,
						})
						if err != nil {
							t.Error(err)
							return
						}
						jsonHandler(status, string(body))(w, r)
					}))
					args := []string{"--profile", "agent", "backup", command}
					if debug {
						args = append(args, "--debug")
					}
					code, stdout, stderr := run(args...)
					if code == 0 || calls != 1 || token == "" || stdout != "" {
						t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, stdout, stderr)
					}
					if strings.Contains(stdout, token) || strings.Contains(stderr, token) {
						t.Fatal("bearer token escaped")
					}
					wantCode := "Forbidden"
					if status == http.StatusInternalServerError {
						wantCode = "ServerError"
					}
					if !strings.Contains(stderr, wantCode) || !strings.Contains(stderr, "backup.List") {
						t.Fatalf("missing error code or operation: %s", stderr)
					}
					if debug && !strings.Contains(stderr, "request") {
						t.Fatalf("missing debug request: %s", stderr)
					}
				})
			}
		}
	}
}
