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

// taggingTagJSON marshals tags as the bare JSON array both the tag read and
// write endpoints use, the same shape tagging's own tests build.
func taggingTagJSON(t *testing.T, tags []map[string]any) string {
	t.Helper()
	if tags == nil {
		tags = []map[string]any{}
	}
	data, err := json.Marshal(tags)
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	return string(data)
}

// TestTaggingListResourceTagsRequest checks that list-resource-tags sends
// one GET to the vServer gateway's tag endpoint and prints every tag it
// returns, system tags included.
func TestTaggingListResourceTagsRequest(t *testing.T) {
	body := taggingTagJSON(t, []map[string]any{
		{"key": "vng.zone", "value": "hcm-3", "systemTag": true},
		{"key": "env", "value": "prod", "systemTag": false},
	})
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "list-resource-tags", "--resource-id", "res-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-resource-tags: %v (stderr=%s)", err, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, `"Key": "vng.zone"`) || !strings.Contains(got, `"Key": "env"`) {
		t.Fatalf("stdout = %s, want both tags, system tag included", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1", n)
	}
}

// TestTaggingTagResourceRequestSent checks tag-resource's own read-merge-
// send-confirm shape end to end: it drives the real command against a
// fixture vServer server whose pre-write read shows one existing user tag,
// checks the PUT body carries both the existing and the new tag, and that
// the confirm read's own tags come back on stdout.
func TestTaggingTagResourceRequestSent(t *testing.T) {
	var putBody []byte
	var calls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{{"key": "env", "value": "prod"}})))
					return
				}
				_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{
					{"key": "env", "value": "prod"},
					{"key": "owner", "value": "sre"},
				})))
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				putBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("[]"))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "tag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS",
		"--key", "owner", "--value", "sre",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("tag-resource: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("PUT body is not valid JSON: %v (%s)", err, putBody)
	}
	if decoded["resourceId"] != "res-1" || decoded["resourceType"] != "VIRTUAL-IP-ADDRESS" {
		t.Fatalf("PUT body = %s, want resourceId=res-1 resourceType=VIRTUAL-IP-ADDRESS", putBody)
	}
	list, _ := decoded["tagRequestList"].([]any)
	if len(list) != 2 {
		t.Fatalf("PUT body tagRequestList = %v, want 2 entries (the existing tag plus the new one)", list)
	}

	got := stdout.String()
	if !strings.Contains(got, `"Key": "owner"`) || !strings.Contains(got, `"Value": "sre"`) {
		t.Fatalf("stdout = %s, want the confirm read's tags, including the new one", got)
	}
	if !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestTaggingTagResourceNoOpSendsNoPUT checks tag-resource's own no-op path:
// --key already set to --value among the user tags sends only the pre-write
// GET, no PUT, per the tagging design.
func TestTaggingTagResourceNoOpSendsNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s: a no-change tag must send no PUT", r.Method)
			}
			_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{{"key": "env", "value": "prod"}})))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "tag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS",
		"--key", "env", "--value", "prod",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("tag-resource: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", stdout.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-write read only)", n)
	}
}

// TestTaggingTagResourceSystemTagKeyRefusedBeforeAnyRequest checks the
// tagging.ErrSystemTag mapping: a --key with the "vng." prefix is refused
// before any request, with error code SystemTag and exit code 1.
func TestTaggingTagResourceSystemTagKeyRefusedBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "tag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS",
		"--key", "vng.owner", "--value", "sre",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for a vng. prefixed key")
	}
	if got := classify(err).Code; got != "SystemTag" {
		t.Fatalf("Code = %q, want SystemTag (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0: a vng. key is refused before any request", n)
	}
}

// TestTaggingTagResourceNotSettledPrintsOutputOnStdout checks the tagging
// NotSettled wiring end to end: the PUT succeeds, but the confirm read comes
// back mismatched (another writer changed the tags), so the write must not
// be repeated, and the CLI still prints the confirm read's own tags.
func TestTaggingTagResourceNotSettledPrintsOutputOnStdout(t *testing.T) {
	var calls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte("[]"))
					return
				}
				_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{{"key": "other", "value": "x"}})))
			case http.MethodPut:
				_, _ = w.Write([]byte("[]"))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "tag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS",
		"--key", "env", "--value", "prod",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotSettled" {
		t.Fatalf("Code = %q, want NotSettled", got)
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"Key": "other"`) {
		t.Fatalf("stdout = %s, want the confirm read's own mismatched tags", got)
	}
}

// TestTaggingUntagResourceRequestSent checks untag-resource's own read-
// merge-send-confirm shape: the PUT must carry every remaining user tag,
// and the confirm read's own tags come back on stdout.
func TestTaggingUntagResourceRequestSent(t *testing.T) {
	var putBody []byte
	var calls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{
						{"key": "env", "value": "prod"},
						{"key": "team", "value": "platform"},
					})))
					return
				}
				_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{{"key": "team", "value": "platform"}})))
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				putBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte("[]"))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "untag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "env",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("untag-resource: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("PUT body is not valid JSON: %v (%s)", err, putBody)
	}
	list, _ := decoded["tagRequestList"].([]any)
	if len(list) != 1 {
		t.Fatalf("PUT body tagRequestList = %v, want only the surviving tag", list)
	}
	first, _ := list[0].(map[string]any)
	if first["key"] != "team" {
		t.Fatalf("PUT body tagRequestList[0] = %v, want key=team", first)
	}

	got := stdout.String()
	if !strings.Contains(got, `"Key": "team"`) || strings.Contains(got, `"Key": "env"`) {
		t.Fatalf("stdout = %s, want only the remaining team tag", got)
	}
}

// TestTaggingUntagResourceNoOpSendsNoPUT checks untag-resource's own no-op
// path: removing a --key already absent from the user tags sends only the
// pre-write GET, no PUT.
func TestTaggingUntagResourceNoOpSendsNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s: an absent key must send no PUT", r.Method)
			}
			_, _ = w.Write([]byte(taggingTagJSON(t, []map[string]any{{"key": "team", "value": "platform"}})))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "untag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "env",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("untag-resource: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", stdout.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-write read only)", n)
	}
}

// TestTaggingUntagResourceSystemTagKeyRefusedBeforeAnyRequest checks the
// tagging.ErrSystemTag mapping for untag-resource: a --key with the "vng."
// prefix is refused before any request, with error code SystemTag.
func TestTaggingUntagResourceSystemTagKeyRefusedBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"tagging", "untag-resource",
		"--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "vng.zone",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for a vng. prefixed key")
	}
	if got := classify(err).Code; got != "SystemTag" {
		t.Fatalf("Code = %q, want SystemTag (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0: a vng. key is refused before any request", n)
	}
}

// TestTaggingWritesNeedNoYes checks the tagging design's own decision: unlike
// every destructive write elsewhere in this CLI, tag-resource and
// untag-resource are Write but not Destructive, so neither needs --yes.
func TestTaggingWritesNeedNoYes(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"tag-resource", []string{"tag-resource", "--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "env", "--value", "staging"}},
		{"untag-resource", []string{"untag-resource", "--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "env"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			// current holds the fixture's own state, so the PUT this test
			// sends is reflected back by the very next GET (the confirm
			// read), the same way the real vServer gateway would settle it:
			// without this, a static response would make the confirm read
			// disagree with what was just sent and turn every real write
			// into a spurious tagging.ErrNotSettled.
			current := []map[string]any{{"key": "env", "value": "prod"}}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						_, _ = w.Write([]byte(taggingTagJSON(t, current)))
					case http.MethodPut:
						var body struct {
							TagRequestList []map[string]string `json:"tagRequestList"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatalf("decode PUT body: %v", err)
						}
						next := make([]map[string]any, len(body.TagRequestList))
						for i, tr := range body.TagRequestList {
							next[i] = map[string]any{"key": tr["key"], "value": tr["value"]}
						}
						current = next
						_, _ = w.Write([]byte("[]"))
					default:
						t.Fatalf("unexpected method %s", r.Method)
					}
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "tagging"}, tc.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("%s without --yes: %v (stderr=%s)", tc.op, err, stderr.String())
			}
		})
	}
}

// TestTaggingWritesReadOnlyRefusedWithZeroRequests checks that a read-only
// profile refuses tag-resource and untag-resource with exit code 2 before
// any request, the same rule every other Write operation follows.
func TestTaggingWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"tag-resource", []string{"tag-resource", "--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "env", "--value", "prod"}},
		{"untag-resource", []string{"untag-resource", "--resource-id", "res-1", "--resource-type", "VIRTUAL-IP-ADDRESS", "--key", "env"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/tag/resource/res-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "tagging"}, tc.args...))
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
