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
	"danny.vn/vngcloud/containerregistry"
)

// exampleRepository builds the same sanitized RepositoryDto shape the SDK's
// own fixtures decode (testdata/containerregistry), reused here so the
// golden files below exercise a realistic Repository rather than an empty
// struct.
func exampleRepository(status string) containerregistry.Repository {
	return containerregistry.Repository{
		ID:            "repo-1",
		Name:          "<account>-app",
		BackendName:   "<account>-app",
		AccessLevel:   "PRIVATE",
		RegistryURL:   "<hostname>/<account>-app",
		QuotaLimitGB:  1,
		QuotaUsed:     0,
		ImageCount:    0,
		AttachedUsers: 0,
		Status:        status,
		CreatedAt:     "2026-01-01T00:00:00Z",
	}
}

// TestGoldenContainerRegistryListRepositories checks list-repositories'
// exact output shape: Repository is a typed struct, so the CLI's own JSON
// prints its Go field names, in declaration order, not the API's wire
// spelling.
func TestGoldenContainerRegistryListRepositories(t *testing.T) {
	v := &containerregistry.ListRepositoriesOutput{
		Items: []containerregistry.Repository{exampleRepository("ACTIVE")},
		Page:  1, PageSize: 25, TotalPage: 1, TotalItem: 1,
	}
	checkGolden(t, "containerregistry-list-repositories.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-list-repositories.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryGetRepository checks get-repository's exact
// output shape, {"Repository": {...}}.
func TestGoldenContainerRegistryGetRepository(t *testing.T) {
	v := &containerregistry.GetRepositoryOutput{Repository: exampleRepository("ACTIVE")}
	checkGolden(t, "containerregistry-get-repository.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-get-repository.table.golden", "table", "", v)
}

// TestGoldenContainerRegistryCreateRepository checks create-repository's
// exact output shape, the same {"Repository": {...}} shape get-repository
// uses.
func TestGoldenContainerRegistryCreateRepository(t *testing.T) {
	v := &containerregistry.CreateRepositoryOutput{Repository: exampleRepository("ACTIVE")}
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

// TestContainerRegistryCommandsMatchDesignTable checks the vCR writes
// design's "CLI" table: list-repositories, get-repository,
// create-repository, and delete-repository ship in this release, each with
// the flags its Input's exported fields derive; list-users and the other R2
// commands stay out until the SDK types User from a live capture (see
// TestContainerRegistryListUsersIsHeld).
func TestContainerRegistryCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-repositories": {"access-level", "name"},
		"get-repository":    {"repository-id"},
		"create-repository": {"name", "quota-limit-gb", "no-wait"},
		"delete-repository": {"repository-id", "no-wait"},
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

// TestContainerRegistryListUsersIsHeld checks the CLI reads design's
// decision directly: list-users must not appear in the operation table
// until the SDK types User from a live capture, even though the SDK method
// already exists.
func TestContainerRegistryListUsersIsHeld(t *testing.T) {
	for _, op := range containerRegistryOps {
		if op.name == "list-users" {
			t.Fatalf("list-users is registered; it must stay held until containerregistry.User is typed from a live capture")
		}
	}
}

// TestContainerRegistryListRepositoriesUsesTheGlobalScopedPath checks that
// list-repositories (a Global-scoped read, per the CLI reads design's scope
// table) sends its request with no project id in the path, that
// --access-level reaches the query string, and that the decoded Repository
// prints under its own Go field names.
func TestContainerRegistryListRepositoriesUsesTheGlobalScopedPath(t *testing.T) {
	body := `{"data":[{"uuid":"repo-1","name":"<account>-app","accessLevel":"PRIVATE"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
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

// repoJSON builds a flat RepositoryDto JSON object with no images, the shape
// GetRepository, CreateRepository, and DeleteRepository all decode directly,
// with no "data" envelope, unlike list-repositories; always under id
// "repo-1", the only id every test below uses.
func repoJSON(status string) string {
	body := map[string]any{
		"uuid":         "repo-1",
		"name":         "<account>-app",
		"backendName":  "<account>-app",
		"accessLevel":  "PRIVATE",
		"registryUrl":  "<hostname>/<account>-app",
		"quotaLimit":   1,
		"quotaUsed":    0,
		"imageCount":   0,
		"attachedUser": 0,
		"status":       status,
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
		"/v1/repository/repo-1": jsonHandler(http.StatusOK, repoJSON("ACTIVE")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "get-repository", "--repository-id", "repo-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-repository: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/repository/repo-1"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"Status": "ACTIVE"`) {
		t.Fatalf("stdout = %s, want the decoded repository", stdout.String())
	}
}

// TestContainerRegistryCreateRepositoryEndToEnd drives the real
// create-repository command against a fixture vCR server whose very first
// confirm read already shows the repository ACTIVE, so the SDK's post-write
// wait settles at once and this test never really sleeps: it checks the
// POST body (repoName, quotaLimit, and isPublic always false) and that the
// settled repository comes back on stdout.
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
			_, _ = w.Write([]byte(repoJSON("CREATING")))
		},
		"/v1/repository/repo-1": jsonHandler(http.StatusOK, repoJSON("ACTIVE")),
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

	if !strings.Contains(stdout.String(), `"Status": "ACTIVE"`) {
		t.Fatalf("stdout = %s, want the settled repository", stdout.String())
	}
}

// TestContainerRegistryCreateRepositoryNotSettledOnCanceledContext drives a
// real create-repository call whose POST succeeds and whose settle GET is
// interrupted by canceling the command's own context, mirroring a Ctrl-C
// during the post-write wait. Per the vCR writes design, that failure must
// still surface as an error wrapping containerregistry.ErrNotSettled with
// the last repository the SDK read as a non-nil Output, not as the plain
// canceled-context path the CLI otherwise falls back to.
func TestContainerRegistryCreateRepositoryNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoJSON("CREATING")))
		},
		"/v1/repository/repo-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(repoJSON("CREATING")))
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
				_, _ = w.Write([]byte(repoJSON("ACTIVE")))
			case http.MethodDelete:
				deleted = true
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(repoJSON("ACTIVE")))
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
// writes design's read-only rule: create-repository and delete-repository
// are both Write operations, so a read-only profile refuses each with exit 2
// before any request. delete-repository also passes --yes, so the read-only
// refusal is unambiguously the reason, not a missing --yes.
func TestContainerRegistryWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-repository", []string{"create-repository", "--name", "app", "--quota-limit-gb", "1"}},
		{"delete-repository", []string{"delete-repository", "--repository-id", "repo-1", "--yes"}},
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
