package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// iamUserInfoJSON builds the auth/userinfo body every guard read and
// get-caller-identity decode: userID and userType identify the caller, the
// two fields every guard rule in iam/guard.go reasons about.
func iamUserInfoJSON(userID, userType string) string {
	return fmt.Sprintf(`{"userId":%q,"userType":%q,"username":"<account>","accountId":1}`, userID, userType)
}

// iamNoPoliciesJSON is an empty paged policy list, the shape
// ListServiceAccountPolicies returns for a service account with nothing
// attached: guardServiceAccountWrite's own protected check then reports
// false without ever calling GetPolicy.
const iamNoPoliciesJSON = `{"data":[]}`

// iamWriteActionsJSON is a one-action IAM action list whose label is
// "Write", the shape guardWriteActionNames needs to build its own write
// action name set.
const iamWriteActionsJSON = `[{"action":"CreatePolicy","label":"Write","resources":["*"]}]`

// iamPrivilegedPolicyAttachmentJSON and iamPrivilegedPolicyJSON together
// give a service account one attached policy whose only statement allows
// "iam:CreatePolicy", the exact write action iamWriteActionsJSON names:
// serviceAccountIsProtected reads the attachment list, then this policy, and
// reports the account protected.
const iamPrivilegedPolicyAttachmentJSON = `{"data":[{"id":"policy-1","name":"p","createdAt":1700000000000}]}`
const iamPrivilegedPolicyJSON = `{"id":"policy-1","name":"p","description":"","manager":"user","scope":"account","root":"1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"],"resources":["*"]}],"createdAt":1700000000000}`

// serviceAccountJSON builds one bare ServiceAccount object, the shape
// GetServiceAccount decodes directly (no "data" envelope) and every
// confirm read after a service account write uses. Every test below names
// the account "app", so that is the only Name this fixture ever needs.
func serviceAccountJSON(id, clientID string) string {
	return fmt.Sprintf(`{"id":%q,"clientId":%q,"name":"app","description":"d","accessTokenLifeSpan":3600,"createdAt":1700000000000,"enabled":true,"lastUse":0}`,
		id, clientID)
}

// TestIAMReadsPrintGoldenJSON drives every read the IAM writes design lists,
// except list-policy-attachments (which makes three requests, tested
// separately below), through the real Service-built commands against a
// fixture, and checks the exact path and method the SDK built plus one
// distinguishing field in the printed output.
func TestIAMReadsPrintGoldenJSON(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		args       []string
		body       string
		wantSubstr string
	}{
		{
			"get-caller-identity", "/accounts-api/v1/auth/userinfo",
			[]string{"iam", "get-caller-identity"},
			iamUserInfoJSON("user-1", "iam-user"),
			`"UserID": "user-1"`,
		},
		{
			"list-users", "/accounts-api/v1/iam-users",
			[]string{"iam", "list-users"},
			`{"data":[{"id":"user-1","username":"acct-1","createdAt":"2026-01-01T00:00:00Z"}]}`,
			`"Username": "acct-1"`,
		},
		{
			"list-actions", "/policies-api/v1/actions",
			[]string{"iam", "list-actions"},
			iamWriteActionsJSON,
			`"Action": "CreatePolicy"`,
		},
		{
			"list-service-accounts", "/accounts-api/v1/service-accounts",
			[]string{"iam", "list-service-accounts"},
			`{"data":[` + serviceAccountJSON("sa-1", "client-1") + `]}`,
			`"ClientID": "client-1"`,
		},
		{
			"get-service-account", "/accounts-api/v1/service-accounts/sa-1",
			[]string{"iam", "get-service-account", "--service-account-id", "sa-1"},
			serviceAccountJSON("sa-1", "client-1"),
			`"Name": "app"`,
		},
		{
			"list-service-account-policies", "/policies-api/v1/user-attachments/service-accounts/sa-1/policies",
			[]string{"iam", "list-service-account-policies", "--service-account-id", "sa-1"},
			`{"data":[{"id":"policy-1","name":"read-only","createdAt":1700000000000}]}`,
			`"Name": "read-only"`,
		},
		{
			"list-policies", "/policies-api/v1/policies",
			[]string{"iam", "list-policies"},
			`{"data":[{"id":"policy-1","name":"read-only","createdAt":1700000000000}]}`,
			`"ID": "policy-1"`,
		},
		{
			"get-policy", "/policies-api/v1/policies/policy-1",
			[]string{"iam", "get-policy", "--policy-id", "policy-1"},
			`{"id":"policy-1","name":"read-only","description":"d","manager":"user","scope":"account","root":"123456","statements":[{"effect":"allow","actions":["vserver:List*"],"resources":["*"]}],"createdAt":1700000000000}`,
			`"Manager": "user"`,
		},
		{
			"list-groups", "/policies-api/v1/groups",
			[]string{"iam", "list-groups"},
			`[{"id":"group-1","name":"g","createdAt":1700000000000}]`,
			`"Name": "g"`,
		},
		{
			"get-group", "/policies-api/v1/groups/group-1",
			[]string{"iam", "get-group", "--group-id", "group-1"},
			`{"id":"group-1","name":"g","description":"d","mode":"iam","root":"123","iamUsers":["user-1"],"policies":["policy-1"],"createdAt":1700000000000}`,
			`"Mode": "iam"`,
		},
		{
			"list-group-policies", "/policies-api/v1/groups/group-1/policies",
			[]string{"iam", "list-group-policies", "--group-id", "group-1"},
			`{"data":[{"id":"policy-1","name":"read-only","createdAt":1700000000000}]}`,
			`"ID": "policy-1"`,
		},
		{
			"list-user-groups", "/policies-api/v1/user-attachments/iam-users/user-1/groups",
			[]string{"iam", "list-user-groups", "--user-id", "user-1"},
			`[{"id":"group-1","name":"g","description":"d","mode":"iam","iamUsers":[],"policies":[],"createdAt":1700000000000}]`,
			`"Mode": "iam"`,
		},
		{
			"list-user-policies", "/policies-api/v1/user-attachments/iam-users/user-1/policies",
			[]string{"iam", "list-user-policies", "--user-id", "user-1"},
			`{"data":[{"id":"policy-1","name":"read-only","createdAt":1700000000000}]}`,
			`"ID": "policy-1"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				tt.path: jsonHandler(http.StatusOK, tt.body),
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3"}, tt.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
			}
			if got, ok := fixture.methodFor(tt.path); !ok || got != http.MethodGet {
				t.Fatalf("method for %s = %q, ok=%v, want GET", tt.path, got, ok)
			}
			if !strings.Contains(stdout.String(), tt.wantSubstr) {
				t.Fatalf("stdout = %s, want it to contain %q", stdout.String(), tt.wantSubstr)
			}
		})
	}
}

// TestIAMListUsersPagingFlags checks that --page and --size reach the
// accounts API as pageNumber and pageSize, and that page 0 (the default) is
// sent as is rather than floored the way core.PageQuery floors an ordinary
// service's page 0 to its first page.
func TestIAMListUsersPagingFlags(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/iam-users": jsonHandler(http.StatusOK, `{"data":[]}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "list-users", "--page", "2", "--size", "5"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-users: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/accounts-api/v1/iam-users")
	if !ok {
		t.Fatal("no request observed")
	}
	if !strings.Contains(q, "pageNumber=2") || !strings.Contains(q, "pageSize=5") {
		t.Fatalf("query = %q, want pageNumber=2 and pageSize=5", q)
	}
}

// TestIAMListPolicyAttachmentsMakesThreeRequests checks that
// list-policy-attachments queries the groups, iam-users, and
// service-accounts attachment lists of one policy and prints all three.
func TestIAMListPolicyAttachmentsMakesThreeRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/policies-api/v1/policies/policy-1/groups":           jsonHandler(http.StatusOK, `[{"id":"group-1","name":"g","createdAt":1700000000000}]`),
		"/policies-api/v1/policies/policy-1/iam-users":        jsonHandler(http.StatusOK, `["user-1"]`),
		"/policies-api/v1/policies/policy-1/service-accounts": jsonHandler(http.StatusOK, `["sa-1"]`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "list-policy-attachments", "--policy-id", "policy-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-policy-attachments: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 3 {
		t.Fatalf("requestCount = %d, want 3", n)
	}
	out := stdout.String()
	for _, want := range []string{`"user-1"`, `"sa-1"`, `"Name": "g"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %s, want it to contain %q", out, want)
		}
	}
}

// TestIAMReadOnlyRefusesEveryWriteWithZeroRequests checks the CLI design's
// read-only rule for every one of iam's four writes: a profile with
// read_only set refuses each before any request, including the SDK's own
// guard reads. --secret-file names a path that passes checkSecretFilePath
// (a fresh path under a real, existing directory), and --yes is given for
// the two Destructive writes, so read-only is the only refusal each case can
// hit; without --yes the destructive check would refuse first, and without a
// valid --secret-file the secret file guard would.
func TestIAMReadOnlyRefusesEveryWriteWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-service-account", []string{"create-service-account", "--name", "app", "--secret-file", "/does-not-matter"}},
		{"update-service-account", []string{"update-service-account", "--service-account-id", "sa-1", "--description", "x"}},
		{"delete-service-account", []string{"delete-service-account", "--service-account-id", "sa-1", "--yes"}},
		{"reset-service-account-secret", []string{"reset-service-account-secret", "--service-account-id", "sa-1", "--secret-file", "/does-not-matter", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/accounts-api/v1/auth/userinfo": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/accounts-api/v1/service-accounts": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/accounts-api/v1/service-accounts/sa-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/accounts-api/v1/service-accounts/sa-1/reset-secret": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "iam"}, tc.args...))
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

// TestIAMServiceAccountWriteGuardsRefuseBeforeAnyWrite checks that
// update-service-account, delete-service-account, and
// reset-service-account-secret each refuse, with no write request sent, when
// the SDK's own guard (iam/guard.go) reports the target is the caller itself
// (SelfChange) or holds a policy granting an IAM write right
// (PrivilegedChange). The guard runs inside the SDK call, so this exercises
// the full path from internal/cli/errors.go's classify back through the SDK
// sentinel, not just the CLI's own error mapping (which errors_test.go
// checks directly).
func TestIAMServiceAccountWriteGuardsRefuseBeforeAnyWrite(t *testing.T) {
	writeCases := []struct {
		op          string
		writePath   string
		writeMethod string
		buildArgs   func(secretFile string) []string
	}{
		{
			"update-service-account", "/accounts-api/v1/service-accounts/sa-1", http.MethodPatch,
			func(string) []string {
				return []string{"update-service-account", "--service-account-id", "sa-1", "--description", "x"}
			},
		},
		{
			"delete-service-account", "/accounts-api/v1/service-accounts/sa-1", http.MethodDelete,
			func(string) []string {
				return []string{"delete-service-account", "--service-account-id", "sa-1", "--yes"}
			},
		},
		{
			"reset-service-account-secret", "/accounts-api/v1/service-accounts/sa-1/reset-secret", http.MethodPost,
			func(secretFile string) []string {
				return []string{"reset-service-account-secret", "--service-account-id", "sa-1", "--secret-file", secretFile, "--yes"}
			},
		},
	}
	guardCases := []struct {
		name         string
		callerUserID string
		callerType   string
		wantCode     string
	}{
		{"self change", "sa-1", "user-sa", "SelfChange"},
		{"privileged change", "user-1", "iam-user", "PrivilegedChange"},
	}

	for _, wc := range writeCases {
		for _, gc := range guardCases {
			t.Run(wc.op+"/"+gc.name, func(t *testing.T) {
				routes := map[string]func(http.ResponseWriter, *http.Request){
					"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON(gc.callerUserID, gc.callerType)),
					wc.writePath: func(_ http.ResponseWriter, r *http.Request) {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					},
				}
				if gc.wantCode == "PrivilegedChange" {
					routes["/policies-api/v1/actions"] = jsonHandler(http.StatusOK, iamWriteActionsJSON)
					routes["/policies-api/v1/user-attachments/service-accounts/sa-1/policies"] = jsonHandler(http.StatusOK, iamPrivilegedPolicyAttachmentJSON)
					routes["/policies-api/v1/policies/policy-1"] = jsonHandler(http.StatusOK, iamPrivilegedPolicyJSON)
				}
				fixture := newSvcFixture(routes)
				root, _, stderr := newSvcRoot(t, fixture)
				secretFile := filepath.Join(t.TempDir(), "secret")
				root.SetArgs(append([]string{"--region", "hcm-3", "iam"}, wc.buildArgs(secretFile)...))
				err := root.ExecuteContext(context.Background())
				if err == nil {
					t.Fatalf("expected a %s error", gc.wantCode)
				}
				if got := classify(err).Code; got != gc.wantCode {
					t.Fatalf("Code = %q, want %q (stderr=%s)", got, gc.wantCode, stderr.String())
				}
				if got := exitCode(err); got != 1 {
					t.Fatalf("exitCode = %d, want 1 (stderr=%s)", got, stderr.String())
				}
				if got, ok := fixture.methodFor(wc.writePath); ok {
					t.Fatalf("the guarded write was sent: %s %s", got, wc.writePath)
				}
			})
		}
	}
}

// TestIAMUpdateServiceAccountSendsOnlyChangedField checks that
// update-service-account's own guard passes for an unprotected target and
// that the PATCH body carries only the field the caller set.
func TestIAMUpdateServiceAccountSendsOnlyChangedField(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/accounts-api/v1/service-accounts/sa-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPatch:
				defer func() { _ = r.Body.Close() }()
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			case http.MethodGet:
				jsonHandler(http.StatusOK, serviceAccountJSON("sa-1", "client-1"))(w, r)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "update-service-account", "--service-account-id", "sa-1", "--description", "new-desc"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-service-account: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 1 || decoded["description"] != "new-desc" {
		t.Fatalf("body = %s, want only description=new-desc", body)
	}
	if !strings.Contains(stdout.String(), `"Name": "app"`) {
		t.Fatalf("stdout = %s, want the updated service account printed", stdout.String())
	}
}

// TestIAMDeleteServiceAccountRequiresYesWithZeroRequests checks that
// delete-service-account, Write and Destructive, refuses before any request,
// including the SDK's own guard reads, when --yes is missing.
func TestIAMDeleteServiceAccountRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/accounts-api/v1/service-accounts/sa-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "delete-service-account", "--service-account-id", "sa-1"})
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

// TestIAMDeleteServiceAccountWithYes checks the success path: the guard's
// own reads pass for an unprotected target and the DELETE is sent.
func TestIAMDeleteServiceAccountWithYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/accounts-api/v1/service-accounts/sa-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "delete-service-account", "--service-account-id", "sa-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-service-account: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/accounts-api/v1/service-accounts/sa-1"); !ok || got != http.MethodDelete {
		t.Fatalf("method = %q, ok=%v, want DELETE", got, ok)
	}
}

// TestIAMCreateServiceAccountRequiresSecretFile checks that
// create-service-account refuses to run, before any request, when
// --secret-file is missing.
func TestIAMCreateServiceAccountRequiresSecretFile(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-service-account", "--name", "app"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --secret-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMCreateServiceAccountRefusesExistingSecretFile checks that
// create-service-account refuses an already-existing --secret-file path
// before any request, and TestIAMCreateServiceAccountRefusesSymlinkSecretFile
// checks the same for a symlink, even one whose target does not exist:
// checkSecretFilePath (secretfile.go) is shared with create-ssh-key and
// reset-service-account-secret, so this proves the wiring rather than
// re-testing that function's own logic.
func TestIAMCreateServiceAccountRefusesExistingSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-service-account", "--name", "app", "--secret-file", path})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an existing --secret-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMCreateServiceAccountWritesSecretFileAndRedactsOutput drives a real
// create-service-account call, with --debug on, and checks: the file lands
// at mode 0600 holding exactly the client secret the fixture returned plus a
// trailing newline, stdout's ClientSecret field reads "[redacted]", stdout
// carries a SecretFile field naming the path and the service account's own
// ClientID unredacted, and the secret text itself appears nowhere in stdout
// or stderr, --debug's own write started/finished lines included.
func TestIAMCreateServiceAccountWritesSecretFileAndRedactsOutput(t *testing.T) {
	const clientSecret = "s3cr3t-material"
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":"sa-2","clientSecret":%q}`, clientSecret)
		},
		"/accounts-api/v1/service-accounts/sa-2": jsonHandler(http.StatusOK, serviceAccountJSON("sa-2", "client-2")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--debug", "iam", "create-service-account", "--name", "app", "--secret-file", path})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-service-account: %v (stderr=%s)", err, stderr.String())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != clientSecret+"\n" {
		t.Fatalf("secret file content = %q, want %q", data, clientSecret+"\n")
	}

	out := stdout.String()
	if !strings.Contains(out, `"ClientSecret": "[redacted]"`) {
		t.Fatalf("stdout = %s, want ClientSecret redacted", out)
	}
	if !strings.Contains(out, `"SecretFile": "`+path+`"`) {
		t.Fatalf("stdout = %s, want SecretFile %q", out, path)
	}
	if !strings.Contains(out, `"ClientID": "client-2"`) {
		t.Fatalf("stdout = %s, want the unredacted ClientID printed", out)
	}
	if strings.Contains(out, clientSecret) || strings.Contains(stderr.String(), clientSecret) {
		t.Fatalf("the client secret leaked into output: stdout=%s stderr=%s", out, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write started") || !strings.Contains(stderr.String(), "write finished") {
		t.Fatalf("stderr = %s, want --debug's write started/write finished lines", stderr.String())
	}
}

// TestIAMCreateServiceAccountNoSecretKeepsAccountAndWritesNoFile checks the
// design's own rule: a create response with no client secret at all keeps
// the new service account (no delete is sent), writes no file, and exits 1
// naming reset-service-account-secret.
func TestIAMCreateServiceAccountNoSecretKeepsAccountAndWritesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"sa-3"}`))
		},
		"/accounts-api/v1/service-accounts/sa-3": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				t.Fatal("the service account must not be deleted when the create response held no secret")
			}
			jsonHandler(http.StatusOK, serviceAccountJSON("sa-3", "client-3"))(w, r)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-service-account", "--name", "app", "--secret-file", path})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "reset-service-account-secret") {
		t.Fatalf("error = %q, want it to name reset-service-account-secret", err.Error())
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("a secret file was written at %s even though the response held no secret", path)
	}
}

// TestIAMCreateServiceAccountCleansUpOnUnwritableSecretFile checks that a
// --secret-file write failure after a create that did return a secret
// deletes the new service account through the SDK and reports
// SecretFileFailed, the same cleanup rule create-ssh-key follows.
func TestIAMCreateServiceAccountCleansUpOnUnwritableSecretFile(t *testing.T) {
	const clientSecret = "s3cr3t-material"
	path := filepath.Join(unwritableSecretFileDir(t), "secret")

	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		// The cleanup delete runs through DeleteServiceAccount, which is
		// itself guarded: these three reads must succeed and report sa-4
		// unprotected before the DELETE below is ever reached.
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-4/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/accounts-api/v1/service-accounts": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":"sa-4","clientSecret":%q}`, clientSecret)
		},
		"/accounts-api/v1/service-accounts/sa-4": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, serviceAccountJSON("sa-4", "client-4"))(w, r)
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-service-account", "--name", "app", "--secret-file", path})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if !deleted {
		t.Fatal("the orphaned service account was never deleted")
	}
	if strings.Contains(stdout.String(), clientSecret) || strings.Contains(stderr.String(), clientSecret) {
		t.Fatalf("the client secret leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

// TestIAMResetServiceAccountSecretRequiresYesWithZeroRequests checks that
// reset-service-account-secret, Write and Destructive, refuses before any
// request when --yes is missing, even with a valid --secret-file.
func TestIAMResetServiceAccountSecretRequiresYesWithZeroRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts/sa-1/reset-secret": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "reset-service-account-secret", "--service-account-id", "sa-1", "--secret-file", path})
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

// TestIAMResetServiceAccountSecretRequiresSecretFile checks that
// reset-service-account-secret refuses before any request when
// --secret-file is missing, even with --yes.
func TestIAMResetServiceAccountSecretRequiresSecretFile(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts/sa-1/reset-secret": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "reset-service-account-secret", "--service-account-id", "sa-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --secret-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMResetServiceAccountSecretWritesSecretFileAndRedactsOutput mirrors
// TestIAMCreateServiceAccountWritesSecretFileAndRedactsOutput for the reset
// path: the guard's own reads pass for an unprotected target, the new secret
// lands in the file, and it never reaches stdout, stderr, or --debug.
func TestIAMResetServiceAccountSecretWritesSecretFileAndRedactsOutput(t *testing.T) {
	const newSecret = "n3w-s3cr3t"
	path := filepath.Join(t.TempDir(), "secret")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/accounts-api/v1/service-accounts/sa-1/reset-secret": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"clientSecret":%q}`, newSecret)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "--debug", "iam", "reset-service-account-secret", "--service-account-id", "sa-1", "--secret-file", path})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("reset-service-account-secret: %v (stderr=%s)", err, stderr.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != newSecret+"\n" {
		t.Fatalf("secret file content = %q, want %q", data, newSecret+"\n")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}

	out := stdout.String()
	if !strings.Contains(out, `"ClientSecret": "[redacted]"`) {
		t.Fatalf("stdout = %s, want ClientSecret redacted", out)
	}
	if !strings.Contains(out, `"SecretFile": "`+path+`"`) {
		t.Fatalf("stdout = %s, want SecretFile %q", out, path)
	}
	if strings.Contains(out, newSecret) || strings.Contains(stderr.String(), newSecret) {
		t.Fatalf("the new secret leaked into output: stdout=%s stderr=%s", out, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write started") || !strings.Contains(stderr.String(), "write finished") {
		t.Fatalf("stderr = %s, want --debug's write started/write finished lines", stderr.String())
	}
}

// TestIAMResetServiceAccountSecretWriteFailureCannotUndo checks the design's
// own rule for a reset: unlike create, there is nothing to delete, so a
// --secret-file write failure just reports SecretFileFailed and tells the
// caller to reset again; the new secret is already live either way.
func TestIAMResetServiceAccountSecretWriteFailureCannotUndo(t *testing.T) {
	const newSecret = "n3w-s3cr3t"
	path := filepath.Join(unwritableSecretFileDir(t), "secret")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/accounts-api/v1/service-accounts/sa-1/reset-secret": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"clientSecret":%q}`, newSecret)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "reset-service-account-secret", "--service-account-id", "sa-1", "--secret-file", path})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "reset-service-account-secret again") {
		t.Fatalf("error = %q, want it to say to reset again", err.Error())
	}
	if strings.Contains(stdout.String(), newSecret) || strings.Contains(stderr.String(), newSecret) {
		t.Fatalf("the new secret leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}
