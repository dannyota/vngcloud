package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/containerregistry"
)

// TestGoldenContainerRegistryListRepositories checks list-repositories'
// exact output shape: Repository is map[string]any, so it prints the API's
// own key names unchanged, the same rendering portal's models get.
func TestGoldenContainerRegistryListRepositories(t *testing.T) {
	v := &containerregistry.ListRepositoriesOutput{Items: []containerregistry.Repository{
		{"name": "repo-1", "visibility": "PRIVATE"},
	}, Page: 1, PageSize: 25, TotalPage: 1, TotalItem: 1}
	checkGolden(t, "containerregistry-list-repositories.json.golden", "json", "", v)
	checkGolden(t, "containerregistry-list-repositories.table.golden", "table", "", v)
}

// TestContainerRegistryCommandsMatchDesignTable checks the CLI reads
// design's "containerregistry" table: only list-repositories ships in this
// release, with its one flag; list-users stays out until the SDK types User
// from a live capture (see decision 4).
func TestContainerRegistryCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-repositories": {"access-level"},
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

// TestContainerRegistryListUsersIsHeld checks decision 4 of the CLI reads
// design directly: list-users must not appear in the operation table until
// the SDK types User from a live capture, even though the SDK method
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
// table) sends its request with no project id in the path, and that
// --access-level reaches the query string.
func TestContainerRegistryListRepositoriesUsesTheGlobalScopedPath(t *testing.T) {
	body := `{"data":[{"name":"repo-1","visibility":"PRIVATE"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
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
	if q != "accessLevel=PUBLIC" {
		t.Fatalf("query string = %q, want accessLevel=PUBLIC", q)
	}
	if got, ok := fixture.methodFor("/v1/repository"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"name": "repo-1"`) {
		t.Fatalf("stdout = %s, want the API's own repository keys", stdout.String())
	}
}

// TestContainerRegistryListRepositoriesRedactsSensitiveKeys runs the real
// list-repositories command against a fixture row with a key that looks
// like a secret, and checks that the CLI reads design's key redaction hides
// it end to end, per the "Secrets" section: Repository is map-backed, so
// redact_maps.go's redactMaps covers it the same as portal's models.
func TestContainerRegistryListRepositoriesRedactsSensitiveKeys(t *testing.T) {
	body := `{"data":[{"name":"repo-1","robotToken":"tok-super-secret"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-repositories"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "tok-super-secret") {
		t.Fatalf("stdout leaked robotToken's value:\n%s", out)
	}
	// json.Marshal HTML-escapes "<" and ">" by default, so the literal
	// redactedPlaceholder marker never survives JSON output unchanged;
	// "redacted" alone does.
	if !strings.Contains(out, "redacted") {
		t.Fatalf("stdout is missing the redacted marker:\n%s", out)
	}
	if !strings.Contains(out, "repo-1") {
		t.Fatalf("stdout dropped a non-sensitive value:\n%s", out)
	}
}

// TestContainerRegistryListRepositoriesRedactsSensitiveKeysUnderQuery checks
// that --query cannot pull the redacted value back out, per the CLI reads
// design's "A test feeds a map with each such key through json, table, text,
// and --query": the query runs on the same value redactMaps already
// mutated, so it can only ever see the placeholder.
func TestContainerRegistryListRepositoriesRedactsSensitiveKeysUnderQuery(t *testing.T) {
	body := `{"data":[{"name":"repo-1","robotToken":"tok-super-secret"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--query", "Items[0].robotToken", "containerregistry", "list-repositories"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "tok-super-secret") {
		t.Fatalf("--query output leaked robotToken's value:\n%s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Fatalf("--query output is missing the redacted marker:\n%s", out)
	}
}

// TestContainerRegistryListRepositoriesRedactsSensitiveKeysInTableAndText
// checks the same redaction end to end through the table and text
// renderers, per the same design line.
func TestContainerRegistryListRepositoriesRedactsSensitiveKeysInTableAndText(t *testing.T) {
	for _, format := range []string{outputTable, outputText} {
		t.Run(format, func(t *testing.T) {
			body := `{"data":[{"name":"repo-1","robotToken":"tok-super-secret"}],"page":1,"pageSize":25,"totalPage":1,"totalItem":1}`
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/repository": jsonHandler(http.StatusOK, body),
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--output", format, "containerregistry", "list-repositories"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
			}
			out := stdout.String()
			if strings.Contains(out, "tok-super-secret") {
				t.Fatalf("%s stdout leaked robotToken's value:\n%s", format, out)
			}
			if !strings.Contains(out, "redacted") {
				t.Fatalf("%s stdout is missing the redacted marker:\n%s", format, out)
			}
		})
	}
}
