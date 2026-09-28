package tagging

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return New(testutil.NewConfig(t, handler))
}

// tagsJSON marshals tags as the bare JSON array the tag read and write
// responses both use.
func tagsJSON(t *testing.T, tags []Tag) string {
	t.Helper()
	if tags == nil {
		tags = []Tag{}
	}
	data, err := json.Marshal(tags)
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	return string(data)
}

func writeJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func decodePutBody(t *testing.T, r *http.Request) tagWriteBody {
	t.Helper()
	var body tagWriteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

var badPathIDs = []string{"..", ".", "a/b", "a?b", ""}

// --- ListResourceTags ---

func TestListResourceTagsRequest(t *testing.T) {
	current := []Tag{{Key: "env", Value: "prod", CreatedAt: "2026-01-01T00:00:00Z"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/project-1/tag/resource/res-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	out, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{ResourceID: "res-1"})
	if err != nil {
		t.Fatalf("ListResourceTags() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Key != "env" || out.Items[0].Value != "prod" {
		t.Fatalf("Items = %+v, want [{env prod}]", out.Items)
	}
}

func TestListResourceTagsEmptyArray(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, "[]")
	}))

	out, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{ResourceID: "res-1"})
	if err != nil {
		t.Fatalf("ListResourceTags() error = %v", err)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("Items = %#v, want a non-nil empty slice", out.Items)
	}
}

func TestListResourceTagsDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/tagging/list_resource_tags.json")
	}))

	out, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{ResourceID: "res-1"})
	if err != nil {
		t.Fatalf("ListResourceTags() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("Items = %+v, want 2 tags", out.Items)
	}
	if out.Items[0].Key != "env" || out.Items[0].Value != "prod" || out.Items[0].SystemTag {
		t.Fatalf("Items[0] = %+v, want {env prod false ...}", out.Items[0])
	}
	if out.Items[1].Key != "team" || out.Items[1].Value != "platform" {
		t.Fatalf("Items[1] = %+v, want {team platform ...}", out.Items[1])
	}
}

func TestListResourceTagsSystemTagFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/tagging/list_resource_tags_system.json")
	}))

	out, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{ResourceID: "res-1"})
	if err != nil {
		t.Fatalf("ListResourceTags() error = %v", err)
	}
	if len(out.Items) != 1 || !out.Items[0].SystemTag {
		t.Fatalf("Items = %+v, want one system tag", out.Items)
	}
}

func TestListResourceTagsRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("empty input err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.ListResourceTags(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestListResourceTagsPathIDRejection(t *testing.T) {
	for _, id := range badPathIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))
			if _, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{ResourceID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("ResourceID %q: err = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

func TestListResourceTagsErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"failed"}`))
			}))
			_, err := c.ListResourceTags(context.Background(), &ListResourceTagsInput{ResourceID: "res-1"})
			if err == nil {
				t.Fatal("err = nil, want an error")
			}
			var apiErr *core.APIError
			if status != http.StatusNotFound && (!errors.As(err, &apiErr) || apiErr.StatusCode != status) {
				t.Fatalf("err = %v, want an APIError with status %d", err, status)
			}
			if status == http.StatusNotFound && !vngcloud.IsNotFound(err) {
				t.Fatalf("err = %v, want IsNotFound", err)
			}
		})
	}
}

// --- TagResource ---

func TestTagResourceNewKeyRequestBody(t *testing.T) {
	current := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			// Confirm read: the tags after the PUT.
			writeJSON(t, w, tagsJSON(t, append(current, Tag{Key: "owner", Value: "sre"})))
		case http.MethodPut:
			if r.URL.Path != "/v2/project-1/tag/resource/res-1" {
				t.Fatalf("unexpected PUT path: %s", r.URL.Path)
			}
			body := decodePutBody(t, r)
			if body.ResourceID != "res-1" || body.ResourceType != "SERVER" {
				t.Fatalf("body resourceId/resourceType = %q/%q, want res-1/SERVER", body.ResourceID, body.ResourceType)
			}
			if len(body.TagRequestList) != 3 {
				t.Fatalf("TagRequestList = %+v, want 3 entries (2 existing + 1 new)", body.TagRequestList)
			}
			want := map[string]string{"env": "prod", "team": "platform", "owner": "sre"}
			for _, tr := range body.TagRequestList {
				if want[tr.Key] != tr.Value {
					t.Fatalf("TagRequestList entry %+v not in %v", tr, want)
				}
			}
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "owner", Value: "sre",
	})
	if err != nil {
		t.Fatalf("TagResource() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if out.Previous != nil {
		t.Fatalf("Previous = %v, want nil (key was absent)", *out.Previous)
	}
	if len(out.Tags) != 3 {
		t.Fatalf("Tags = %+v, want 3 tags from the confirm read", out.Tags)
	}
	if calls.Load() != 2 {
		t.Fatalf("GET calls = %d, want 2 (pre-write read and confirm)", calls.Load())
	}
}

func TestTagResourceReplacesExistingValue(t *testing.T) {
	current := []Tag{{Key: "env", Value: "staging"}, {Key: "team", Value: "platform"}}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			writeJSON(t, w, tagsJSON(t, []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}}))
		case http.MethodPut:
			body := decodePutBody(t, r)
			if len(body.TagRequestList) != 2 {
				t.Fatalf("TagRequestList = %+v, want 2 entries: nothing dropped", body.TagRequestList)
			}
			want := map[string]string{"env": "prod", "team": "platform"}
			for _, tr := range body.TagRequestList {
				if want[tr.Key] != tr.Value {
					t.Fatalf("TagRequestList entry %+v not in %v", tr, want)
				}
			}
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "env", Value: "prod",
	})
	if err != nil {
		t.Fatalf("TagResource() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if out.Previous == nil || *out.Previous != "staging" {
		t.Fatalf("Previous = %v, want \"staging\"", out.Previous)
	}
}

func TestTagResourceNoChangeSendsNothing(t *testing.T) {
	current := []Tag{{Key: "env", Value: "prod"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a no-change tag must send no PUT", r.Method)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	out, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "env", Value: "prod",
	})
	if err != nil {
		t.Fatalf("TagResource() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
	if out.Previous == nil || *out.Previous != "prod" {
		t.Fatalf("Previous = %v, want \"prod\"", out.Previous)
	}
	if len(out.Tags) != 1 {
		t.Fatalf("Tags = %+v, want the tags as read", out.Tags)
	}
}

func TestTagResourceSystemPrefixKeyRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected: a \"vng.\" Key is refused before any read")
	}))

	_, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "vng.owner", Value: "sre",
	})
	if !errors.Is(err, ErrSystemTag) {
		t.Fatalf("err = %v, want ErrSystemTag", err)
	}
}

func TestTagResourceExistingSystemKeyRefused(t *testing.T) {
	// A system tag under a key without the "vng." prefix, to prove the
	// refusal also checks the resource's own tags, not the prefix alone.
	current := []Tag{{Key: "legacy-system", Value: "true", SystemTag: true}, {Key: "env", Value: "prod"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a system Key must send no write", r.Method)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	_, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "legacy-system", Value: "false",
	})
	if !errors.Is(err, ErrSystemTag) {
		t.Fatalf("err = %v, want ErrSystemTag", err)
	}
}

// TestTagResourceCoexistsWithSystemTags writes a user tag on a resource
// that also carries a system tag, since every resource does (vng.zone,
// vng.region, vng.createdBy, confirmed live): the write must not refuse for
// that reason alone, and its PUT must carry only the user tags.
func TestTagResourceCoexistsWithSystemTags(t *testing.T) {
	current := []Tag{
		{Key: "vng.zone", Value: "hcm-3", SystemTag: true, CreatedAt: "2026-01-01T00:00:00Z"},
		{Key: "env", Value: "prod"},
	}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			writeJSON(t, w, tagsJSON(t, append(current, Tag{Key: "owner", Value: "sre"})))
		case http.MethodPut:
			body := decodePutBody(t, r)
			if len(body.TagRequestList) != 2 {
				t.Fatalf("TagRequestList = %+v, want 2 entries: the system tag must not be sent", body.TagRequestList)
			}
			want := map[string]string{"env": "prod", "owner": "sre"}
			for _, tr := range body.TagRequestList {
				if want[tr.Key] != tr.Value {
					t.Fatalf("TagRequestList entry %+v not in %v", tr, want)
				}
			}
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "VIRTUAL-IP-ADDRESS", Key: "owner", Value: "sre",
	})
	if err != nil {
		t.Fatalf("TagResource() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(out.Tags) != 3 {
		t.Fatalf("Tags = %+v, want the system tag plus both user tags", out.Tags)
	}
	found := false
	for _, tag := range out.Tags {
		if tag.Key == "vng.zone" && tag.SystemTag {
			found = true
		}
	}
	if !found {
		t.Fatalf("Tags = %+v, want the system tag still present", out.Tags)
	}
}

func TestTagResourceConfirmMismatchNotSettled(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, "[]")
				return
			}
			// Another writer changed the tags between the PUT and the confirm.
			writeJSON(t, w, tagsJSON(t, []Tag{{Key: "other", Value: "x"}}))
		case http.MethodPut:
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "env", Value: "prod",
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || len(out.Tags) != 1 || out.Tags[0].Key != "other" {
		t.Fatalf("out = %+v, want the confirm read's actual tags", out)
	}
}

func TestTagResourceConfirmReadFailureNotSettled(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, "[]")
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case http.MethodPut:
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "env", Value: "prod",
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || len(out.Tags) != 0 {
		t.Fatalf("out = %+v, want the last tags read before the write", out)
	}
}

func TestTagResourceRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []*TagResourceInput{
		{ResourceType: "SERVER", Key: "env", Value: "prod"},
		{ResourceID: "res-1", Key: "env", Value: "prod"},
		{ResourceID: "res-1", ResourceType: "SERVER", Value: "prod"},
	}
	for _, in := range cases {
		if _, err := c.TagResource(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("in = %+v: err = %v, want ErrInvalidInput", in, err)
		}
	}
	if _, err := c.TagResource(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestTagResourcePathIDRejection(t *testing.T) {
	for _, id := range badPathIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))
			_, err := c.TagResource(context.Background(), &TagResourceInput{ResourceID: id, ResourceType: "SERVER", Key: "env", Value: "prod"})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("ResourceID %q: err = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

func TestTagResourcePUTErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					writeJSON(t, w, "[]")
				case http.MethodPut:
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"failed"}`))
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))
			_, err := c.TagResource(context.Background(), &TagResourceInput{
				ResourceID: "res-1", ResourceType: "SERVER", Key: "env", Value: "prod",
			})
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
				t.Fatalf("err = %v, want an APIError with status %d", err, status)
			}
		})
	}
}

func TestTagResourceQuotaSendsFullListEvenAtLimit(t *testing.T) {
	current := make([]Tag, 10)
	for i := range current {
		current[i] = Tag{Key: "k" + string(rune('a'+i)), Value: "v"}
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, tagsJSON(t, current))
		case http.MethodPut:
			body := decodePutBody(t, r)
			if len(body.TagRequestList) != 11 {
				t.Fatalf("TagRequestList has %d entries, want 11: the SDK must not cap the list client-side", len(body.TagRequestList))
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"tag quota exceeded"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "new", Value: "v",
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("err = %v, want the server's own quota refusal", err)
	}
}

// --- UntagResource ---

func TestUntagResourceRemovesKey(t *testing.T) {
	current := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			writeJSON(t, w, tagsJSON(t, []Tag{{Key: "team", Value: "platform"}}))
		case http.MethodPut:
			body := decodePutBody(t, r)
			if len(body.TagRequestList) != 1 || body.TagRequestList[0].Key != "team" {
				t.Fatalf("TagRequestList = %+v, want only team=platform", body.TagRequestList)
			}
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if err != nil {
		t.Fatalf("UntagResource() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if out.Previous == nil || *out.Previous != "prod" {
		t.Fatalf("Previous = %v, want \"prod\"", out.Previous)
	}
	if len(out.Tags) != 1 || out.Tags[0].Key != "team" {
		t.Fatalf("Tags = %+v, want only team", out.Tags)
	}
}

func TestUntagResourceAbsentKeyNoChange(t *testing.T) {
	current := []Tag{{Key: "team", Value: "platform"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: an absent key must send no PUT", r.Method)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	out, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if err != nil {
		t.Fatalf("UntagResource() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
	if out.Previous != nil {
		t.Fatalf("Previous = %v, want nil", *out.Previous)
	}
	if len(out.Tags) != 1 {
		t.Fatalf("Tags = %+v, want the tags as read, unchanged", out.Tags)
	}
}

func TestUntagResourceSystemPrefixKeyRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected: a \"vng.\" Key is refused before any read")
	}))

	_, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "vng.zone"})
	if !errors.Is(err, ErrSystemTag) {
		t.Fatalf("err = %v, want ErrSystemTag", err)
	}
}

func TestUntagResourceExistingSystemKeyRefused(t *testing.T) {
	current := []Tag{{Key: "legacy-system", Value: "true", SystemTag: true}, {Key: "env", Value: "prod"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a system Key must send no write", r.Method)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	_, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "legacy-system"})
	if !errors.Is(err, ErrSystemTag) {
		t.Fatalf("err = %v, want ErrSystemTag", err)
	}
}

// TestUntagResourceCoexistsWithSystemTags removes a user tag from a
// resource that also carries a system tag: the removal must not refuse for
// that reason alone, and its PUT must carry only the remaining user tags.
func TestUntagResourceCoexistsWithSystemTags(t *testing.T) {
	current := []Tag{
		{Key: "vng.zone", Value: "hcm-3", SystemTag: true, CreatedAt: "2026-01-01T00:00:00Z"},
		{Key: "env", Value: "prod"},
		{Key: "owner", Value: "sre"},
	}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			writeJSON(t, w, tagsJSON(t, []Tag{current[0], current[1]}))
		case http.MethodPut:
			body := decodePutBody(t, r)
			if len(body.TagRequestList) != 1 || body.TagRequestList[0].Key != "env" {
				t.Fatalf("TagRequestList = %+v, want only env=prod: the system tag must not be sent", body.TagRequestList)
			}
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.UntagResource(context.Background(), &UntagResourceInput{
		ResourceID: "res-1", ResourceType: "VIRTUAL-IP-ADDRESS", Key: "owner",
	})
	if err != nil {
		t.Fatalf("UntagResource() error = %v", err)
	}
	if !out.Changed || out.Previous == nil || *out.Previous != "sre" {
		t.Fatalf("Changed/Previous = %v/%v, want true/\"sre\"", out.Changed, out.Previous)
	}
	if len(out.Tags) != 2 {
		t.Fatalf("Tags = %+v, want the system tag plus the remaining user tag", out.Tags)
	}
	found := false
	for _, tag := range out.Tags {
		if tag.Key == "vng.zone" && tag.SystemTag {
			found = true
		}
	}
	if !found {
		t.Fatalf("Tags = %+v, want the system tag still present", out.Tags)
	}
}

func TestUntagResourceConfirmMismatchNotSettled(t *testing.T) {
	current := []Tag{{Key: "env", Value: "prod"}}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := calls.Add(1)
			if n == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			// Upsert semantics: the key the SDK dropped is still present.
			writeJSON(t, w, tagsJSON(t, current))
		case http.MethodPut:
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled: the confirm read still shows the key the PUT was sent to drop", err)
	}
	if out == nil || len(out.Tags) != 1 {
		t.Fatalf("out = %+v, want the confirm read's actual tags", out)
	}
}

func TestUntagResourceConfirmReadFailureNotSettled(t *testing.T) {
	current := []Tag{{Key: "env", Value: "prod"}}
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if calls.Add(1) == 1 {
				writeJSON(t, w, tagsJSON(t, current))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case http.MethodPut:
			writeJSON(t, w, "[]")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || len(out.Tags) != 1 || out.Tags[0] != current[0] {
		t.Fatalf("out = %+v, want the last tags read before the write", out)
	}
}

func TestUntagResourceRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []*UntagResourceInput{
		{ResourceType: "SERVER", Key: "env"},
		{ResourceID: "res-1", Key: "env"},
		{ResourceID: "res-1", ResourceType: "SERVER"},
	}
	for _, in := range cases {
		if _, err := c.UntagResource(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("in = %+v: err = %v, want ErrInvalidInput", in, err)
		}
	}
	if _, err := c.UntagResource(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestUntagResourcePathIDRejection(t *testing.T) {
	for _, id := range badPathIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))
			_, err := c.UntagResource(context.Background(), &UntagResourceInput{ResourceID: id, ResourceType: "SERVER", Key: "env"})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("ResourceID %q: err = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

// --- pure helpers ---

func TestSplitTags(t *testing.T) {
	tags := []Tag{
		{Key: "vng.zone", Value: "hcm-3", SystemTag: true},
		{Key: "env", Value: "prod"},
		{Key: "vng.region", Value: "hcm", SystemTag: true},
	}
	system, user := splitTags(tags)
	if len(system) != 2 || system[0].Key != "vng.zone" || system[1].Key != "vng.region" {
		t.Fatalf("system = %+v, want vng.zone and vng.region in order", system)
	}
	if len(user) != 1 || user[0].Key != "env" {
		t.Fatalf("user = %+v, want only env", user)
	}
}

func TestSystemTagKey(t *testing.T) {
	system := []Tag{{Key: "legacy-system", Value: "x", SystemTag: true}}
	if !systemTagKey(system, "legacy-system") {
		t.Fatal("systemTagKey = false, want true for a matching key")
	}
	if systemTagKey(system, "env") {
		t.Fatal("systemTagKey = true, want false for an unrelated key")
	}
	if systemTagKey(nil, "legacy-system") {
		t.Fatal("systemTagKey = true, want false with no system tags")
	}
}

func TestApplyTagKeepsUnnamedKeys(t *testing.T) {
	current := []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}, {Key: "c", Value: "3"}}

	next, previous, changed := applyTag(current, "b", "20")
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if previous == nil || *previous != "2" {
		t.Fatalf("previous = %v, want \"2\"", previous)
	}
	if len(next) != 3 {
		t.Fatalf("next = %+v, want 3 entries: a and c must survive unchanged", next)
	}
	values := map[string]string{}
	for _, tag := range next {
		values[tag.Key] = tag.Value
	}
	if values["a"] != "1" || values["b"] != "20" || values["c"] != "3" {
		t.Fatalf("next = %+v, want a=1 b=20 c=3", next)
	}
	// current must not have been mutated in place.
	if current[1].Value != "2" {
		t.Fatalf("current was mutated: %+v", current)
	}
}

func TestApplyTagNewKeyAppends(t *testing.T) {
	current := []Tag{{Key: "a", Value: "1"}}
	next, previous, changed := applyTag(current, "b", "2")
	if !changed || previous != nil {
		t.Fatalf("changed = %v, previous = %v, want true, nil", changed, previous)
	}
	if len(next) != 2 {
		t.Fatalf("next = %+v, want 2 entries", next)
	}
}

func TestApplyUntagKeepsUnnamedKeys(t *testing.T) {
	current := []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}, {Key: "c", Value: "3"}}
	next, previous, changed := applyUntag(current, "b")
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if previous == nil || *previous != "2" {
		t.Fatalf("previous = %v, want \"2\"", previous)
	}
	if len(next) != 2 {
		t.Fatalf("next = %+v, want 2 entries: a and c must survive", next)
	}
	for _, tag := range next {
		if tag.Key == "b" {
			t.Fatalf("next = %+v, want b removed", next)
		}
	}
}

func TestApplyUntagAbsentKeyNoChange(t *testing.T) {
	current := []Tag{{Key: "a", Value: "1"}}
	next, previous, changed := applyUntag(current, "missing")
	if changed || previous != nil {
		t.Fatalf("changed = %v, previous = %v, want false, nil", changed, previous)
	}
	if len(next) != 1 {
		t.Fatalf("next = %+v, want the original list", next)
	}
}

func TestTagsEqual(t *testing.T) {
	a := []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	b := []Tag{{Key: "b", Value: "2"}, {Key: "a", Value: "1"}}
	if !tagsEqual(a, b) {
		t.Fatal("tagsEqual = false, want true for the same pairs in a different order")
	}
	if tagsEqual(a, []Tag{{Key: "a", Value: "1"}}) {
		t.Fatal("tagsEqual = true, want false for a different length")
	}
	if tagsEqual(a, []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "different"}}) {
		t.Fatal("tagsEqual = true, want false for a different value")
	}
	if tagsEqual(a, []Tag{{Key: "a", Value: "1"}, {Key: "a", Value: "1"}}) {
		t.Fatal("tagsEqual = true, want false when the second list repeats a key")
	}
}
