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

func TestTagResourceSystemTagRefused(t *testing.T) {
	current := []Tag{{Key: "vng:managed", Value: "true", SystemTag: true}, {Key: "env", Value: "prod"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a system-tagged resource must send no write", r.Method)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	_, err := c.TagResource(context.Background(), &TagResourceInput{
		ResourceID: "res-1", ResourceType: "SERVER", Key: "env", Value: "staging",
	})
	if !errors.Is(err, ErrSystemTag) {
		t.Fatalf("err = %v, want ErrSystemTag", err)
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
	if out == nil || len(out.Tags) != 1 || out.Tags[0].Key != "env" {
		t.Fatalf("out = %+v, want a fallback Output carrying the intended tag list", out)
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

// --- untagResource ---

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

	out, err := c.untagResource(context.Background(), &untagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if err != nil {
		t.Fatalf("untagResource() error = %v", err)
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

	out, err := c.untagResource(context.Background(), &untagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if err != nil {
		t.Fatalf("untagResource() error = %v", err)
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

func TestUntagResourceSystemTagRefused(t *testing.T) {
	current := []Tag{{Key: "vng:managed", Value: "true", SystemTag: true}, {Key: "env", Value: "prod"}}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a system-tagged resource must send no write", r.Method)
		}
		writeJSON(t, w, tagsJSON(t, current))
	}))

	_, err := c.untagResource(context.Background(), &untagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if !errors.Is(err, ErrSystemTag) {
		t.Fatalf("err = %v, want ErrSystemTag", err)
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

	out, err := c.untagResource(context.Background(), &untagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "env"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled: the confirm read still shows the key the PUT was sent to drop", err)
	}
	if out == nil || len(out.Tags) != 1 {
		t.Fatalf("out = %+v, want the confirm read's actual tags", out)
	}
}

func TestUntagResourceRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []*untagResourceInput{
		{ResourceType: "SERVER", Key: "env"},
		{ResourceID: "res-1", Key: "env"},
		{ResourceID: "res-1", ResourceType: "SERVER"},
	}
	for _, in := range cases {
		if _, err := c.untagResource(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("in = %+v: err = %v, want ErrInvalidInput", in, err)
		}
	}
	if _, err := c.untagResource(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestUntagResourcePathIDRejection(t *testing.T) {
	for _, id := range badPathIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))
			_, err := c.untagResource(context.Background(), &untagResourceInput{ResourceID: id, ResourceType: "SERVER", Key: "env"})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("ResourceID %q: err = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

// --- pure helpers ---

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
}
