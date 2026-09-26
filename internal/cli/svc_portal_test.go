package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/portal"
)

// TestGoldenPortalListZones and TestGoldenPortalGetQuota check the CLI reads
// design's map-backed output rendering: portal's models pass the API's own
// key names through unchanged, and a Get's wrapped resource renders as one
// compact-JSON cell in table.
func TestGoldenPortalListZones(t *testing.T) {
	v := &portal.ListZonesOutput{Items: []portal.Zone{
		{"id": "zone-1", "name": "example.com"},
	}}
	checkGolden(t, "portal-list-zones.json.golden", "json", "", v)
	checkGolden(t, "portal-list-zones.table.golden", "table", "", v)
}

func TestGoldenPortalGetQuota(t *testing.T) {
	v := &portal.GetQuotaOutput{Quota: portal.Quota{"quotaName": "vcpu", "used": 1, "limit": 10}}
	checkGolden(t, "portal-get-quota.json.golden", "json", "", v)
	checkGolden(t, "portal-get-quota.table.golden", "table", "", v)
}

// TestPortalCommandsMatchDesignTable checks the CLI reads design's "portal"
// table: the five command names, and that only get-quota takes a flag
// (--name).
func TestPortalCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"get-user-info":   nil,
		"list-zones":      nil,
		"list-quota-used": nil,
		"get-quota":       {"name"},
		"get-tag-quota":   nil,
	}
	if got := opNames(portalOps); len(got) != len(wantFlags) {
		t.Fatalf("portal ops = %v, want %d commands", got, len(wantFlags))
	}
	for _, op := range portalOps {
		want, ok := wantFlags[op.name]
		if !ok {
			t.Fatalf("unexpected portal command %q", op.name)
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

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPortalGetQuotaMissingNameStopsBeforeAnyRequest checks the CLI design's
// required-flag guard: get-quota's --name is vngcloud:"required", so a
// missing flag must refuse the command with exit code 2 before any request
// reaches the server.
func TestPortalGetQuotaMissingNameStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/quotas/quotaUsed": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "portal", "get-quota"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --name")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestPortalGetUserInfoRedactsSensitiveKeys runs the real get-user-info
// command against a fixture body shaped like the live API's own envelope,
// with a key that looks like a credential added to it, and checks that the
// CLI reads design's key redaction hides it, end to end, while an ordinary
// value survives.
func TestPortalGetUserInfoRedactsSensitiveKeys(t *testing.T) {
	body := `{"data":{"userId":100,"type":"iam-user","apiToken":"tok-super-secret"}}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/users/info": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "portal", "get-user-info"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "tok-super-secret") {
		t.Fatalf("stdout leaked apiToken's value:\n%s", out)
	}
	// json.Marshal HTML-escapes "<" and ">" by default, so the literal
	// redactedPlaceholder marker never survives JSON output unchanged;
	// "redacted" alone does.
	if !strings.Contains(out, "redacted") {
		t.Fatalf("stdout is missing the redacted marker:\n%s", out)
	}
	if !strings.Contains(out, "iam-user") {
		t.Fatalf("stdout dropped a non-sensitive value:\n%s", out)
	}
}

// TestPortalListZonesUsesTheProjectScopedPath checks that list-zones (a
// Project-scoped read, per the CLI reads design's scope table) sends its
// request under the given --project-id, and prints the API's own zone keys
// unchanged.
func TestPortalListZonesUsesTheProjectScopedPath(t *testing.T) {
	body := `{"zones":[{"id":"zone-1","name":"example.com"}]}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/zones": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "portal", "list-zones"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/proj-1/zones"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"id": "zone-1"`) {
		t.Fatalf("stdout = %s, want the API's own zone keys", stdout.String())
	}
}
