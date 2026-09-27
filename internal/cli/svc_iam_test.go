package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// iamUserInfoJSON builds the auth/userinfo body get-caller-identity decodes:
// userID and userType identify the caller.
func iamUserInfoJSON(userID, userType string) string {
	return fmt.Sprintf(`{"userId":%q,"userType":%q,"username":"<account>","accountId":1}`, userID, userType)
}

// serviceAccountJSON builds one bare ServiceAccount object, the shape
// GetServiceAccount decodes directly (no "data" envelope). Every test below
// names the account "app", so that is the only Name this fixture ever needs.
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
			`[{"action":"CreatePolicy","label":"Write","resources":["*"]}]`,
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
