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
	"danny.vn/vngcloud/containerregistry"
)

// listPermissionsJSON builds the bare-array body list-permissions decodes,
// with the server's own three action names (per the vCR writes design's
// Source section).
func listPermissionsJSON() string {
	return `[{"uuid":"policy-1","action":"Pull Images"},` +
		`{"uuid":"policy-2","action":"Push Images"},` +
		`{"uuid":"policy-3","action":"All"}]`
}

// userListJSON builds a {"data": [...]} user list envelope holding zero or
// more rows named name, each with a distinct uuid derived from its index, for
// the post-create lookup and list-users/list-repository-users end-to-end
// tests below.
func userListJSON(names ...string) string {
	rows := make([]map[string]any, len(names))
	for i, name := range names {
		rows[i] = map[string]any{"uuid": fmt.Sprintf("ra-%d", i+1), "name": name}
	}
	b, err := json.Marshal(map[string]any{"data": rows, "page": 1, "pageSize": 10000, "totalPage": 1, "totalItem": len(rows)})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// exampleRepository builds the same sanitized RepositoryDto shape the SDK's
// own fixtures decode (testdata/containerregistry), reused here so the
// golden files below exercise a realistic Repository rather than an empty
// struct. There is no status field: no repository response carries one.
func exampleRepository() containerregistry.Repository {
	return containerregistry.Repository{
		ID:            "repo-1",
		Name:          "app-test",
		BackendName:   "app-test",
		AccessLevel:   "PRIVATE",
		RegistryURL:   "<hostname>/app-test",
		QuotaLimitGB:  1,
		QuotaUsed:     0,
		ImageCount:    0,
		AttachedUsers: 0,
		CreatedAt:     "2026-01-01T00:00:00Z",
	}
}

// TestGoldenContainerRegistryListRepositories checks list-repositories'
// exact output shape: Repository is a typed struct, so the CLI's own JSON
// prints its Go field names, in declaration order, not the API's wire
// spelling.
func TestGoldenContainerRegistryListRepositories(t *testing.T) {
	v := &containerregistry.ListRepositoriesOutput{
		Items: []containerregistry.Repository{exampleRepository()},
		Page:  1, PageSize: 25, TotalPage: 1, TotalItem: 1,
	}
	checkGolden(t, "containerregistry-list-repositories.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-list-repositories.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryGetRepository checks get-repository's exact
// output shape, {"Repository": {...}}.
func TestGoldenContainerRegistryGetRepository(t *testing.T) {
	v := &containerregistry.GetRepositoryOutput{Repository: exampleRepository()}
	checkGolden(t, "containerregistry-get-repository.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-get-repository.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryCreateRepository checks create-repository's
// exact output shape, the same {"Repository": {...}} shape get-repository
// uses.
func TestGoldenContainerRegistryCreateRepository(t *testing.T) {
	v := &containerregistry.CreateRepositoryOutput{Repository: exampleRepository()}
	checkGolden(t, "containerregistry-create-repository.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-create-repository.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryDeleteRepository checks delete-repository's
// exact output shape: an empty object, since DeleteRepositoryOutput carries
// no fields.
func TestGoldenContainerRegistryDeleteRepository(t *testing.T) {
	v := &containerregistry.DeleteRepositoryOutput{}
	checkGolden(t, "containerregistry-delete-repository.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-delete-repository.table.golden", "table", "", v)
}

// exampleUser builds the same sanitized RobotAccountDto shape the SDK's own
// fixtures decode, reused here so the golden files below exercise a
// realistic User rather than an empty struct.
func exampleUser() containerregistry.User {
	return containerregistry.User{
		ID:                   "ra-1",
		Name:                 "app-ci",
		BackendName:          "app-ci",
		Description:          "CI pull user",
		Disabled:             false,
		ExpiredAt:            "2026-06-01T00:00:00Z",
		CreatedAt:            "2026-01-01T00:00:00Z",
		NumberOfRepositories: 1,
		UserID:               "1001",
		Repositories: []containerregistry.RepositoryPermission{
			{
				RepositoryID:          "repo-1",
				RepositoryName:        "app-test",
				BackendRepositoryName: "app-test",
				Policies:              []containerregistry.Permission{{ID: "policy-1", Action: "Pull Images"}},
			},
		},
	}
}

// TestGoldenContainerRegistryListUsers checks list-users' exact output
// shape: User is a typed struct, so the CLI's own JSON prints its Go field
// names, in declaration order, not the API's wire spelling.
func TestGoldenContainerRegistryListUsers(t *testing.T) {
	v := &containerregistry.ListUsersOutput{
		Items: []containerregistry.User{exampleUser()},
		Page:  1, PageSize: 25, TotalPage: 1, TotalItem: 1,
	}
	checkGolden(t, "containerregistry-list-users.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-list-users.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryListRepositoryUsers checks list-repository-users'
// exact output shape, the same PagedList[User] shape list-users uses.
func TestGoldenContainerRegistryListRepositoryUsers(t *testing.T) {
	v := &containerregistry.ListRepositoryUsersOutput{
		Items: []containerregistry.User{exampleUser()},
		Page:  1, PageSize: 25, TotalPage: 1, TotalItem: 1,
	}
	checkGolden(t, "containerregistry-list-repository-users.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-list-repository-users.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryListPermissions checks list-permissions' exact
// output shape: a bare Items list, with no page fields, since the API itself
// answers a bare JSON array with no envelope.
func TestGoldenContainerRegistryListPermissions(t *testing.T) {
	v := &containerregistry.ListPermissionsOutput{Items: []containerregistry.Permission{
		{ID: "policy-1", Action: "Pull Images"},
		{ID: "policy-2", Action: "Push Images"},
		{ID: "policy-3", Action: "All"},
	}}
	checkGolden(t, "containerregistry-list-permissions.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-list-permissions.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryCreateUser checks create-user's exact output
// shape: User and the redacted SecretKey flattened to the top level,
// alongside SecretFile, the path the new value was written to.
func TestGoldenContainerRegistryCreateUser(t *testing.T) {
	v := &createUserOutput{
		CreateUserOutput: containerregistry.CreateUserOutput{User: exampleUser(), SecretKey: vngcloud.Secret("s3cr3t-value")},
		SecretFile:       "/home/agent/vcr-secret",
	}
	checkGolden(t, "containerregistry-create-user.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-create-user.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryDeleteUser checks delete-user's exact output
// shape: an empty object, since DeleteUserOutput carries no fields.
func TestGoldenContainerRegistryDeleteUser(t *testing.T) {
	v := &containerregistry.DeleteUserOutput{}
	checkGolden(t, "containerregistry-delete-user.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-delete-user.table.golden", "table", "", v)
}

// TestContainerRegistryCommandsMatchDesignTable checks the vCR writes
// design's "CLI" table: every R1 and R2 command is registered, each with the
// flags its Input's exported fields derive. create-user's --secret-file is
// not among them: it comes from extraFlags, which flagSpecsFor (reflecting
// only over the Input struct) never sees, the same reason compute's
// create-ssh-key test never lists --secret-file either.
func TestContainerRegistryCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-repositories":     {"access-level", "name"},
		"get-repository":        {"repository-id"},
		"create-repository":     {"name", "quota-limit-gb", "no-wait"},
		"delete-repository":     {"repository-id", "no-wait"},
		"list-users":            {"name", "page", "size"},
		"list-repository-users": {"repository-id", "name", "page", "size"},
		"list-permissions":      {},
		"create-user":           {"name", "description", "duration-days"},
		"delete-user":           {"user-id"},
	}
	if got := opNames(containerRegistryOps); len(got) != len(wantFlags) {
		t.Fatalf("containerregistry ops = %v, want %d commands", got, len(wantFlags))
	}
	for _, op := range containerRegistryOps {
		want, ok := wantFlags[op.name]
		if !ok {
			t.Fatalf("unexpected containerregistry command %q", op.name)
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatalf("%s: flagSpecsFor: %v", op.name, err)
		}
		var got []string
		for _, s := range specs {
			got = append(got, s.flagName)
		}
		if !equalStringSlices(got, want) {
			t.Errorf("%s flags = %v, want %v", op.name, got, want)
		}
	}
}

// TestContainerRegistryListRepositoriesUsesTheGlobalScopedPath checks that
// list-repositories (a Global-scoped read, per the CLI reads design's scope
// table) sends its request with no project id in the path, that
// --access-level reaches the query string, and that the decoded Repository
// prints under its own Go field names.
func TestContainerRegistryListRepositoriesUsesTheGlobalScopedPath(t *testing.T) {
	body := `{"data":[{"uuid":"repo-1","name":"app-test","accessLevel":"PRIVATE"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-repositories", "--access-level", "PUBLIC"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/v1/repository")
	if !ok {
		t.Fatalf("no request observed")
	}
	if q != "accessLevel=PUBLIC&name=" {
		t.Fatalf("query string = %q, want accessLevel=PUBLIC&name= (ListRepositories always sets both)", q)
	}
	if got, ok := fixture.methodFor("/v1/repository"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"ID": "repo-1"`) {
		t.Fatalf("stdout = %s, want the decoded repository's Go-named ID field", stdout.String())
	}
}

// TestContainerRegistryListRepositoriesDropsUnknownKeys checks that a
// secret-looking key the API might send alongside a repository row, such as
// a robot token, never reaches CLI output: Repository is a typed struct
// carrying only the API reference's own fields (per the vCR writes design's
// typed-model decision), so JSON decoding drops any key it does not declare
// before a result ever reaches renderOutput or redactMaps. This replaces the
// former key-redaction tests, which drove redactMaps against a map-backed
// Repository that no longer exists.
func TestContainerRegistryListRepositoriesDropsUnknownKeys(t *testing.T) {
	body := `{"data":[{"uuid":"repo-1","name":"repo-1","accessLevel":"PRIVATE","robotToken":"tok-super-secret"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-repositories"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "tok-super-secret") || strings.Contains(out, "obotToken") {
		t.Fatalf("stdout carried an undeclared key from the API response:\n%s", out)
	}
	if !strings.Contains(out, "repo-1") {
		t.Fatalf("stdout dropped a declared field's value:\n%s", out)
	}
}

// repoJSON builds a flat RepositoryDto JSON object with no images and no
// status field (no repository response carries one), the shape
// GetRepository, CreateRepository, and DeleteRepository all decode directly,
// with no "data" envelope, unlike list-repositories; always under id
// "repo-1", the only id every test below uses.
func repoJSON() string {
	body := map[string]any{
		"uuid":         "repo-1",
		"name":         "app-test",
		"backendName":  "app-test",
		"accessLevel":  "PRIVATE",
		"registryUrl":  "<hostname>/app-test",
		"quotaLimit":   1,
		"quotaUsed":    0,
		"imageCount":   0,
		"attachedUser": 0,
		"createdAt":    "2026-01-01T00:00:00Z",
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestContainerRegistryGetRepositoryEndToEnd drives the real get-repository
// command against a fixture vCR server.
func TestContainerRegistryGetRepositoryEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository/repo-1": jsonHandler(http.StatusOK, repoJSON()),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "get-repository", "--repository-id", "repo-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-repository: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/repository/repo-1"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"ID": "repo-1"`) {
		t.Fatalf("stdout = %s, want the decoded repository", stdout.String())
	}
}

// TestContainerRegistryCreateRepositoryEndToEnd drives the real
// create-repository command against a fixture vCR server whose very first
// confirm read already shows the repository, so the SDK's post-write wait
// (there is no status to wait on) settles at once and this test never really
// sleeps: it checks the POST body (repoName, quotaLimit, and isPublic always
// false) and that the confirmed repository comes back on stdout.
func TestContainerRegistryCreateRepositoryEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoJSON()))
		},
		"/v1/repository/repo-1": jsonHandler(http.StatusOK, repoJSON()),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "containerregistry", "create-repository",
		"--name", "app-test", "--quota-limit-gb", "1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-repository: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["repoName"] != "app-test" || decoded["quotaLimit"] != float64(1) || decoded["isPublic"] != false {
		t.Fatalf("body = %s, want repoName=app-test quotaLimit=1 isPublic=false", body)
	}

	if !strings.Contains(stdout.String(), `"ID": "repo-1"`) {
		t.Fatalf("stdout = %s, want the confirmed repository", stdout.String())
	}
}

// TestContainerRegistryCreateRepositoryNotSettledOnCanceledContext drives a
// real create-repository call whose POST succeeds but whose confirm read
// never succeeds: it answers 404 (tolerated, so the wait keeps polling) and
// cancels the command's own context as it does, mirroring a Ctrl-C during
// the post-write wait. The next poll iteration's sleep then returns the
// canceled context's own error at once, with no real delay. Per the vCR
// writes design, that failure must still surface as an error wrapping
// containerregistry.ErrNotSettled with the repository the create response
// itself carried as a non-nil Output, not as the plain canceled-context path
// the CLI otherwise falls back to.
func TestContainerRegistryCreateRepositoryNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoJSON()))
		},
		"/v1/repository/repo-1": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			cancel()
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "containerregistry", "create-repository",
		"--name", "app-test", "--quota-limit-gb", "1",
	})
	err := root.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotSettled" {
		t.Fatalf("Code = %q, want NotSettled (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"ID": "repo-1"`) {
		t.Fatalf("stdout = %s, want the Output with the new repository ID", got)
	}
}

// TestContainerRegistryDeleteRepositoryRequiresYes checks the vCR writes
// design's --yes rule for delete-repository: it is Write and Destructive, so
// it fails with exit code 2 and sends no request unless --yes is given.
func TestContainerRegistryDeleteRepositoryRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository/repo-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "delete-repository", "--repository-id", "repo-1"})
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

// TestContainerRegistryDeleteRepositoryWithYesAndNoWait checks that --yes
// together with --no-wait sends exactly the pre-delete image-count read and
// the DELETE, with no confirm list afterward, per DeleteRepositoryInput.NoWait.
func TestContainerRegistryDeleteRepositoryWithYesAndNoWait(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository/repo-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				if deleted {
					t.Fatal("unexpected GET after DELETE: --no-wait must send no confirm read")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(repoJSON()))
			case http.MethodDelete:
				deleted = true
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(repoJSON()))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "containerregistry", "delete-repository",
		"--repository-id", "repo-1", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-repository: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	// One pre-delete GET (the image-count guard) and the DELETE itself;
	// --no-wait must add no confirm list after it.
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2", n)
	}
}

// TestContainerRegistryWritesReadOnlyRefusedWithZeroRequests checks the vCR
// writes design's read-only rule: every containerregistry Write operation is
// refused with exit 2 before any request under a read-only profile.
// delete-repository and delete-user also pass --yes, and create-user also
// passes --name, --secret-file, and Permissions, so the read-only refusal is
// unambiguously the reason, not a missing --yes or a missing required field:
// the profile's own read_only key is only checked once loadConfig has run,
// after every other pre-request check in runOp (op.go).
func TestContainerRegistryWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	secretFilePath := filepath.Join(t.TempDir(), "vcr-secret")
	tests := []struct {
		op   string
		args []string
	}{
		{"create-repository", []string{"create-repository", "--name", "app", "--quota-limit-gb", "1"}},
		{"delete-repository", []string{"delete-repository", "--repository-id", "repo-1", "--yes"}},
		{"create-user", []string{
			"create-user", "--name", "app-ci", "--secret-file", secretFilePath,
			"--cli-input-json", `{"Permissions":[{"RepositoryID":"repo-1","Actions":["Pull Images"]}]}`,
		}},
		{"delete-user", []string{"delete-user", "--user-id", "ra-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/repository": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v1/repository/repo-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v1/user": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v1/user/permissions": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v1/user/ra-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "containerregistry"}, tc.args...))
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

// TestContainerRegistryListUsersEndToEnd drives a real list-users call and
// checks that --name reaches the query string and that the decoded User
// prints under its own Go field names.
func TestContainerRegistryListUsersEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user": jsonHandler(http.StatusOK, userListJSON("app-ci")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-users", "--name", "app-ci"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-users: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/v1/user")
	if !ok {
		t.Fatalf("no request observed")
	}
	if q != "name=app-ci&page=1&size=10000" {
		t.Fatalf("query string = %q, want name=app-ci&page=1&size=10000", q)
	}
	if !strings.Contains(stdout.String(), `"Name": "app-ci"`) {
		t.Fatalf("stdout = %s, want the decoded user's Go-named Name field", stdout.String())
	}
}

// TestContainerRegistryListRepositoryUsersEndToEnd drives a real
// list-repository-users call and checks that --repository-id reaches the
// URL path.
func TestContainerRegistryListRepositoryUsersEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository/repo-1/user": jsonHandler(http.StatusOK, userListJSON("app-ci")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-repository-users", "--repository-id", "repo-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-repository-users: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/repository/repo-1/user"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"Name": "app-ci"`) {
		t.Fatalf("stdout = %s, want the decoded user's Go-named Name field", stdout.String())
	}
}

// TestContainerRegistryListPermissionsEndToEnd drives a real list-permissions
// call against a bare JSON array response, with no "data"/"listData"
// envelope, and checks that every action decodes.
func TestContainerRegistryListPermissionsEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user/permissions": jsonHandler(http.StatusOK, listPermissionsJSON()),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-permissions"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-permissions: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	for _, action := range []string{"Pull Images", "Push Images", "All"} {
		if !strings.Contains(out, action) {
			t.Fatalf("stdout = %s, want action %q", out, action)
		}
	}
}

// containerRegistryTestSecret is the fake secret every create-user fixture
// below returns from its create response: one shared value, so every test
// checks it reaches --secret-file, and only it, rather than a value that
// differs per test for no reason.
const containerRegistryTestSecret = "vcr-test-secret-not-a-real-value"

// containerRegistryUserFixture builds the fixture create-user's own tests
// share: permissions, create, and post-create list handlers, plus an
// optional delete handler. listNames names the rows the post-create list
// answers with, so a test can drive either a single exact match or the
// UserNotFound case (no match) by passing zero or several names.
func containerRegistryUserFixture(t *testing.T, listNames []string, deleteHandler func(http.ResponseWriter, *http.Request)) (*svcFixture, *[]byte) {
	t.Helper()
	var createBody []byte
	routes := map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user/permissions": jsonHandler(http.StatusOK, listPermissionsJSON()),
		"/v1/user": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				defer func() { _ = r.Body.Close() }()
				createBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				b, err := json.Marshal(map[string]string{"secretKey": containerRegistryTestSecret})
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				_, _ = w.Write(b)
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(userListJSON(listNames...)))
			default:
				t.Fatalf("unexpected method %s for /v1/user", r.Method)
			}
		},
	}
	if deleteHandler != nil {
		routes["/v1/user/ra-1"] = deleteHandler
	}
	return newSvcFixture(routes), &createBody
}

// containerRegistryCreateUserArgs returns the argv every create-user test
// below shares beyond --region and --secret-file: a valid --name and a
// single-repository, single-action Permissions value.
func containerRegistryCreateUserArgs(secretFilePath string) []string {
	return []string{
		"--region", "hcm-3", "containerregistry", "create-user",
		"--name", "app-ci", "--secret-file", secretFilePath,
		"--cli-input-json", `{"Permissions":[{"RepositoryID":"repo-1","Actions":["Pull Images"]}]}`,
	}
}

// TestContainerRegistryCreateUserWritesSecretFileAndRedactsOutput drives a
// real create-user call, with --debug on, and checks: the POST body resolves
// "Pull Images" to its policy id, the file lands at mode 0600 holding
// exactly the secret plus one trailing newline, stdout's SecretKey field
// reads "[redacted]" with a SecretFile field naming the path, and the secret
// text itself appears nowhere in stdout or stderr, --debug's own write
// started/finished lines included.
func TestContainerRegistryCreateUserWritesSecretFileAndRedactsOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vcr-secret")

	fixture, createBody := containerRegistryUserFixture(t, []string{"app-ci"}, nil)
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--debug"}, containerRegistryCreateUserArgs(path)...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-user: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(*createBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, *createBody)
	}
	if decoded["name"] != "app-ci" {
		t.Fatalf("body name = %v, want app-ci", decoded["name"])
	}
	perms, _ := decoded["permissionRequestList"].([]any)
	if len(perms) != 1 {
		t.Fatalf("permissionRequestList = %+v, want 1 entry", perms)
	}
	entry, _ := perms[0].(map[string]any)
	if entry["repoId"] != "repo-1" {
		t.Fatalf("repoId = %v, want repo-1", entry["repoId"])
	}
	ids, _ := entry["policyIdList"].([]any)
	if len(ids) != 1 || ids[0] != "policy-1" {
		t.Fatalf("policyIdList = %+v, want [policy-1] (\"Pull Images\" resolved)", ids)
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
	if string(data) != containerRegistryTestSecret+"\n" {
		t.Fatalf("secret file content = %q, want %q", data, containerRegistryTestSecret+"\n")
	}

	out := stdout.String()
	if !strings.Contains(out, `"SecretKey": "[redacted]"`) {
		t.Fatalf("stdout = %s, want SecretKey redacted", out)
	}
	if !strings.Contains(out, `"SecretFile": `+`"`+path+`"`) {
		t.Fatalf("stdout = %s, want SecretFile %q", out, path)
	}
	if strings.Contains(out, containerRegistryTestSecret) || strings.Contains(stderr.String(), containerRegistryTestSecret) {
		t.Fatalf("the secret leaked into output: stdout=%s stderr=%s", out, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write started") || !strings.Contains(stderr.String(), "write finished") {
		t.Fatalf("stderr = %s, want --debug's write started/write finished lines", stderr.String())
	}
}

// TestContainerRegistryCreateUserRequiresSecretFile checks that create-user
// refuses to run, before any request, when --secret-file is missing.
func TestContainerRegistryCreateUserRequiresSecretFile(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/v1/user/permissions": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "containerregistry", "create-user", "--name", "app-ci",
		"--cli-input-json", `{"Permissions":[{"RepositoryID":"repo-1","Actions":["Pull Images"]}]}`,
	})
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

// TestContainerRegistryCreateUserRefusesExistingSecretFile checks that
// create-user refuses an already-existing --secret-file path before any
// request.
func TestContainerRegistryCreateUserRefusesExistingSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vcr-secret")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/v1/user/permissions": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(containerRegistryCreateUserArgs(path))
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

// TestContainerRegistryCreateUserNotFoundStillWritesSecretFile checks the vCR
// writes design's create-user step 4 and errors table: when the create
// itself succeeds but the post-create list finds no row named exactly like
// the input (here, an empty list), the secret is still written to
// --secret-file, the command exits 1 with error code UserNotFound, and the
// Output (with the redacted secret and SecretFile) still prints on stdout,
// the same shape NotSettled and WriteFailed already get.
func TestContainerRegistryCreateUserNotFoundStillWritesSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vcr-secret")

	fixture, _ := containerRegistryUserFixture(t, nil, nil)
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(containerRegistryCreateUserArgs(path))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a UserNotFound error")
	}
	if got := classify(err).Code; got != "UserNotFound" {
		t.Fatalf("Code = %q, want UserNotFound (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != containerRegistryTestSecret+"\n" {
		t.Fatalf("secret file content = %q, want %q", data, containerRegistryTestSecret+"\n")
	}

	out := stdout.String()
	if !strings.Contains(out, `"SecretKey": "[redacted]"`) || !strings.Contains(out, `"SecretFile": `+`"`+path+`"`) {
		t.Fatalf("stdout = %s, want the Output with SecretKey redacted and SecretFile set", out)
	}
	if strings.Contains(out, containerRegistryTestSecret) || strings.Contains(stderr.String(), containerRegistryTestSecret) {
		t.Fatalf("the secret leaked into output: stdout=%s stderr=%s", out, stderr.String())
	}
}

// TestContainerRegistryCreateUserSecretFileFailureDeletesUser checks the vCR
// writes design's create-user step 3: when --secret-file cannot be written
// after a plain create success (the user's id is known), the CLI deletes the
// new user through the SDK and reports SecretFileFailed, naming the user by
// its id since the cleanup delete itself succeeded, and the secret never
// reaches stdout or stderr either.
func TestContainerRegistryCreateUserSecretFileFailureDeletesUser(t *testing.T) {
	path := filepath.Join(unwritableSecretFileDir(t), "vcr-secret")

	deleted := false
	fixture, _ := containerRegistryUserFixture(t, []string{"app-ci"}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		deleted = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(containerRegistryCreateUserArgs(path))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if !deleted {
		t.Fatal("the orphaned user was never deleted")
	}
	if !strings.Contains(err.Error(), "ra-1") {
		t.Fatalf("error = %q, want it to name the user by its id (ra-1)", err.Error())
	}
	if strings.Contains(stdout.String(), containerRegistryTestSecret) || strings.Contains(stderr.String(), containerRegistryTestSecret) {
		t.Fatalf("the secret leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("a partial secret file was left behind at %s", path)
	}
}

// TestContainerRegistryCreateUserSecretFileFailureNamesUserByNameWhenDeleteFails
// checks the vCR writes design's create-user step 3 for its own exceptional
// case: when the cleanup delete itself also fails, even though the user's id
// was known, the error names the user only by --name, not by id, so a person
// can find and delete it with list-users --name <name>.
func TestContainerRegistryCreateUserSecretFileFailureNamesUserByNameWhenDeleteFails(t *testing.T) {
	path := filepath.Join(unwritableSecretFileDir(t), "vcr-secret")

	fixture, _ := containerRegistryUserFixture(t, []string{"app-ci"}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"internal error"}`))
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(containerRegistryCreateUserArgs(path))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "app-ci") {
		t.Fatalf("error = %q, want it to name the user by --name (app-ci)", err.Error())
	}
	if strings.Contains(err.Error(), "ra-1") {
		t.Fatalf("error = %q, want it to name the user only by --name, not its id", err.Error())
	}
	if strings.Contains(stdout.String(), containerRegistryTestSecret) || strings.Contains(stderr.String(), containerRegistryTestSecret) {
		t.Fatalf("the secret leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

// TestContainerRegistryCreateUserSecretFileFailureWithNoIDNamesByName checks
// the vCR writes design's create-user step 3 for its other exceptional case:
// when the post-create list never confirmed the user (UserNotFound, so its
// id is unknown) and the --secret-file write also fails, the CLI sends no
// delete at all (there is no id to send one to) and the error names the user
// only by --name.
func TestContainerRegistryCreateUserSecretFileFailureWithNoIDNamesByName(t *testing.T) {
	path := filepath.Join(unwritableSecretFileDir(t), "vcr-secret")

	fixture, _ := containerRegistryUserFixture(t, nil, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s (no user id was ever confirmed to delete)", r.Method, r.URL.Path)
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(containerRegistryCreateUserArgs(path))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "app-ci") {
		t.Fatalf("error = %q, want it to name the user by --name (app-ci)", err.Error())
	}
	if strings.Contains(stdout.String(), containerRegistryTestSecret) || strings.Contains(stderr.String(), containerRegistryTestSecret) {
		t.Fatalf("the secret leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

// TestContainerRegistryDeleteUserRequiresYes checks the vCR writes design's
// --yes rule for delete-user: it is Write and Destructive, so it fails with
// exit code 2 and sends no request unless --yes is given.
func TestContainerRegistryDeleteUserRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user/ra-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "delete-user", "--user-id", "ra-1"})
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

// TestContainerRegistryDeleteUserWithYes drives a real delete-user call and
// checks the DELETE reaches the user's id in the path.
func TestContainerRegistryDeleteUserWithYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user/ra-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "containerregistry", "delete-user", "--user-id", "ra-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-user: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1", n)
	}
}
