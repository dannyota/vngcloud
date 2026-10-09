package storage

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListBucketsDecodesFixture(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/v1/ceph/projects/proj-1" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "1000" {
			t.Errorf("limit = %q", got)
		}
		if got := r.Header.Get("region_id"); got != "<region-id-2>" {
			t.Errorf("region_id = %q", got)
		}
		testutil.WriteFixture(t, w, fixtures+"list_buckets.json")
	}))
	out, err := c.ListBuckets(context.Background(), &ListBucketsInput{ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d", len(out.Items))
	}
	b := out.Items[0]
	if b.Name != "<bucket>" || b.ObjectCount != 12 || b.SizeBytes != 3456 || b.IsPublic || !b.IsVersioned ||
		b.CreatedDate != "2026-01-01T00:00:00Z" || b.LastModified != "2026-01-02T00:00:00Z" || b.Type != "ceph" {
		t.Fatalf("unexpected bucket: %+v", b)
	}
	if !out.Items[1].IsPublic {
		t.Fatalf("second bucket should be public")
	}
}

func TestListBucketsIsNextFails(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"list_buckets_next.json")
	}))
	_, err := c.ListBuckets(context.Background(), &ListBucketsInput{ProjectID: "proj-1"})
	if err == nil {
		t.Fatal("want error when isNext is true")
	}
}

func TestGetBucketDecodesFixture(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/internal/v1/ceph/projects/proj-1/my-bucket.v1/details" {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		testutil.WriteFixture(t, w, fixtures+"get_bucket.json")
	}))
	out, err := c.GetBucket(context.Background(), &GetBucketInput{ProjectID: "proj-1", Bucket: "my-bucket.v1", Region: "HCM04"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "<bucket>" || out.ObjectCount != 12 || !out.IsVersioned {
		t.Fatalf("unexpected bucket: %+v", out.Bucket)
	}
}

func TestGetBucketNotFoundEnvelope(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"error_envelope_not_found.json")
	}))
	_, err := c.GetBucket(context.Background(), &GetBucketInput{ProjectID: "proj-1", Bucket: "b"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestGetBucketWithoutDataFails(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"code":200}`))
	}))
	if _, err := c.GetBucket(context.Background(), &GetBucketInput{ProjectID: "proj-1", Bucket: "b"}); err == nil {
		t.Fatal("want error for a response without data")
	}
}

func TestBucketInputValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	ctx := context.Background()
	for _, p := range []string{"", "a/b", "..", ".", "a?b", "a%2Fb"} {
		if _, err := c.ListBuckets(ctx, &ListBucketsInput{ProjectID: p}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("ListBuckets ProjectID %q: err = %v", p, err)
		}
	}
	if _, err := c.ListBuckets(ctx, nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("ListBuckets nil: err = %v", err)
	}
	for _, b := range []string{"", ".", "..", "a/b", "a?b", "a%2Fb", ".hidden", "-x", "a b"} {
		if _, err := c.GetBucket(ctx, &GetBucketInput{ProjectID: "proj-1", Bucket: b}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("GetBucket Bucket %q: err = %v", b, err)
		}
	}
	if _, err := c.GetBucket(ctx, &GetBucketInput{Bucket: "b"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("GetBucket without project: err = %v", err)
	}
}
