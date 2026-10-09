package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const (
	bucketsPath = "/internal/v1/ceph/projects/proj-1/buckets/my-bucket"
	detailsPath = "/internal/v1/ceph/projects/proj-1/my-bucket/details"
)

// bucketServer answers the bucket details read with a bucket holding count
// objects, and every write with the given status and body. It records the
// writes it saw.
type bucketServer struct {
	count    int
	status   int
	body     string
	detail   func(w http.ResponseWriter)
	writes   atomic.Int32
	lastBody atomic.Value
	deleted  atomic.Bool
}

func (s *bucketServer) handler(t *testing.T) http.Handler {
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == detailsPath:
			if s.detail != nil {
				s.detail(w)
				return
			}
			if s.deleted.Load() {
				testutil.WriteFixture(t, w, fixtures+"error_envelope_not_found.json")
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"name":"my-bucket","count":` +
				strconv.Itoa(s.count) + `,"size":0,"type":"ceph"}}`))
		case r.URL.Path == bucketsPath:
			s.writes.Add(1)
			if r.Method == http.MethodDelete && s.status < 300 {
				s.deleted.Store(true)
			}
			b, _ := io.ReadAll(r.Body)
			s.lastBody.Store(string(b))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte(s.body))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

const okEnvelope = `{"code":200,"success":true}`

func TestCreateBucketRequestAndReadBack(t *testing.T) {
	var sawPost bool
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == bucketsPath:
			sawPost = true
			if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q", ct)
			}
			b, _ := io.ReadAll(r.Body)
			if string(b) != `{"status":"Disabled"}` {
				t.Errorf("body = %s", b)
			}
			_, _ = w.Write([]byte(okEnvelope))
		case r.Method == http.MethodGet && r.URL.Path == detailsPath:
			if !sawPost {
				t.Error("read before the create")
			}
			testutil.WriteFixture(t, w, fixtures+"get_bucket.json")
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	out, err := newTestClient(t, h).CreateBucket(context.Background(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "<bucket>" || !sawPost {
		t.Fatalf("out = %+v, post = %v", out, sawPost)
	}
}

func TestCreateBucketStatuses(t *testing.T) {
	tests := []struct {
		status  int
		wantErr bool
		want    error
	}{
		{http.StatusOK, false, nil},
		{http.StatusCreated, false, nil},
		{http.StatusBadRequest, true, nil},
		{http.StatusForbidden, true, vngcloud.ErrPermission},
		{http.StatusNotFound, true, vngcloud.ErrNotFound},
		{http.StatusConflict, true, nil},
		{http.StatusInternalServerError, true, nil},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			s := &bucketServer{status: tt.status, body: okEnvelope}
			if tt.wantErr {
				s.body = `{"message":"x"}`
			}
			_, err := newTestClient(t, s.handler(t)).CreateBucket(context.Background(),
				&CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
			if !tt.wantErr {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v, want *APIError with status %d", err, tt.status)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.status == http.StatusConflict && vngcloud.IsNotFound(err) {
				t.Fatal("409 matched NotFound")
			}
			if tt.status >= 500 && !strings.Contains(apiErr.Message, "GetBucket") {
				t.Fatalf("message %q does not name the GetBucket check", apiErr.Message)
			}
			if tt.status < 500 && strings.Contains(apiErr.Message, "GetBucket") {
				t.Fatalf("message %q names the check for a status that proves no create", apiErr.Message)
			}
		})
	}
}

func TestCreateBucketEnvelopeFailure(t *testing.T) {
	s := &bucketServer{status: 200, body: `{"code":409,"success":false,"errorMsg":"<message>"}`}
	_, err := newTestClient(t, s.handler(t)).CreateBucket(context.Background(),
		&CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 200 || apiErr.Code != "409" || apiErr.Message != "<message>" {
		t.Fatalf("err = %v, want envelope *APIError", err)
	}
}

func TestCreateBucketEmptyResponseSaysItMayHaveHappened(t *testing.T) {
	for _, body := range []string{"", "<html>", "{}"} {
		s := &bucketServer{status: 200, body: body}
		_, err := newTestClient(t, s.handler(t)).CreateBucket(context.Background(),
			&CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" || !strings.Contains(apiErr.Message, "may have") {
			t.Fatalf("body %q: err = %v", body, err)
		}
	}
}

func TestCreateBucketReadBackFailureSaysCreated(t *testing.T) {
	s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	}}
	_, err := newTestClient(t, s.handler(t)).CreateBucket(context.Background(),
		&CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if err == nil || !strings.Contains(err.Error(), "was created") {
		t.Fatalf("err = %v, want a message saying the bucket was created", err)
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the read's *APIError kept", err)
	}
}

func TestCreateBucketIsNotRetriedAfter502(t *testing.T) {
	s := &bucketServer{status: http.StatusBadGateway, body: `{"message":"x"}`}
	cfg := testutil.NewRetryConfig(t, s.handler(t))
	_, err := New(cfg).CreateBucket(context.Background(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if err == nil {
		t.Fatal("want error")
	}
	if got := s.writes.Load(); got != 1 {
		t.Fatalf("POST sent %d times, want 1", got)
	}
}

func TestDeleteBucketRequest(t *testing.T) {
	var sawDelete bool
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == detailsPath:
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"name":"my-bucket","count":0}}`))
		case r.Method == http.MethodDelete && r.URL.Path == bucketsPath:
			sawDelete = true
			_, _ = w.Write([]byte(okEnvelope))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	if _, err := newDeleteClient(t, h).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true}); err != nil {
		t.Fatal(err)
	}
	if !sawDelete {
		t.Fatal("no DELETE sent")
	}
}

func TestDeleteBucketWithObjectsSendsNoDelete(t *testing.T) {
	s := &bucketServer{count: 3, status: 200, body: okEnvelope}
	_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if !errors.Is(err, ErrBucketNotEmpty) {
		t.Fatalf("err = %v, want ErrBucketNotEmpty", err)
	}
	if got := s.writes.Load(); got != 0 {
		t.Fatalf("%d writes sent, want 0", got)
	}
}

func TestDeleteBucketMissingBucketSendsNoDelete(t *testing.T) {
	s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
		testutil.WriteFixture(t, w, fixtures+"error_envelope_not_found.json")
	}}
	_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
	if got := s.writes.Load(); got != 0 {
		t.Fatalf("%d writes sent, want 0", got)
	}
}

func TestDeleteBucketStatuses(t *testing.T) {
	tests := []struct {
		status  int
		wantErr bool
		want    error
	}{
		{http.StatusOK, false, nil},
		{http.StatusNoContent, false, nil},
		{http.StatusBadRequest, true, nil},
		{http.StatusForbidden, true, vngcloud.ErrPermission},
		{http.StatusNotFound, true, vngcloud.ErrNotFound},
		{http.StatusConflict, true, nil},
		{http.StatusInternalServerError, true, nil},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			s := &bucketServer{status: tt.status, body: okEnvelope}
			if tt.wantErr {
				s.body = `{"message":"x"}`
			}
			_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(),
				&DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
			if !tt.wantErr {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v, want *APIError with status %d", err, tt.status)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDeleteBucketEnvelopeFailure(t *testing.T) {
	s := &bucketServer{status: 200, body: `{"code":114,"success":false,"errorMsg":"<message>"}`}
	_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(),
		&DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "114" || apiErr.StatusCode != 200 {
		t.Fatalf("err = %v, want envelope *APIError", err)
	}
}

func TestDeleteBucketEmptyResponse(t *testing.T) {
	s := &bucketServer{status: 200, body: ""}
	_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(),
		&DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" {
		t.Fatalf("err = %v, want EmptyResponse", err)
	}
}

func TestBucketWritesRejectUnsafeInputBeforeAnyRequest(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	ctx := context.Background()
	for _, b := range []string{"", ".", "..", "/", "a/b", "a?b", "%", "a%2Fb", "-x"} {
		if _, err := c.CreateBucket(ctx, &CreateBucketInput{ProjectID: "proj-1", Bucket: b}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("CreateBucket Bucket %q: err = %v", b, err)
		}
		if _, err := c.DeleteBucket(ctx, &DeleteBucketInput{ProjectID: "proj-1", Bucket: b}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("DeleteBucket Bucket %q: err = %v", b, err)
		}
	}
	for _, p := range []string{"", ".", "..", "a/b", "a?b", "a%2Fb"} {
		if _, err := c.CreateBucket(ctx, &CreateBucketInput{ProjectID: p, Bucket: "b"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("CreateBucket ProjectID %q: err = %v", p, err)
		}
		if _, err := c.DeleteBucket(ctx, &DeleteBucketInput{ProjectID: p, Bucket: "b"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("DeleteBucket ProjectID %q: err = %v", p, err)
		}
	}
	if _, err := c.CreateBucket(ctx, nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("CreateBucket nil: err = %v", err)
	}
	if _, err := c.DeleteBucket(ctx, nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("DeleteBucket nil: err = %v", err)
	}
}

func TestBucketWritesUnmappedRegionSendsNothing(t *testing.T) {
	var calls atomic.Int32
	c := newUnmappedRegionClient(t, &calls)
	ctx := context.Background()
	if _, err := c.CreateBucket(ctx, &CreateBucketInput{ProjectID: "proj-1", Bucket: "b"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("CreateBucket: err = %v", err)
	}
	if _, err := c.DeleteBucket(ctx, &DeleteBucketInput{ProjectID: "proj-1", Bucket: "b"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("DeleteBucket: err = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("request was sent")
	}
}

// newUnmappedRegionClient builds a Client whose config region has no default
// vStorage region, counting every request its server receives.
func newUnmappedRegionClient(t *testing.T, calls *atomic.Int32) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("sg-1"), vngcloud.WithStaticToken("x"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

// fixtureServer answers the create and delete with the named fixtures and the
// bucket read with getFixture until a delete arrives, then with not found. It
// counts the writes.
func fixtureServer(t *testing.T, getFixture, writeFixture string, writes *atomic.Int32) http.Handler {
	var deleted atomic.Bool
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == detailsPath:
			if deleted.Load() {
				testutil.WriteFixture(t, w, fixtures+"error_envelope_not_found.json")
				return
			}
			testutil.WriteFixture(t, w, fixtures+getFixture)
		case r.URL.Path == bucketsPath:
			writes.Add(1)
			if r.Method == http.MethodDelete {
				deleted.Store(true)
			}
			testutil.WriteFixture(t, w, fixtures+writeFixture)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func TestCreateBucketDecodesFixtures(t *testing.T) {
	var writes atomic.Int32
	c := newTestClient(t, fixtureServer(t, "get_bucket_created.json", "create_bucket.json", &writes))
	in := &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"}
	// The server accepts a repeated create with the same answer.
	for range 2 {
		out, err := c.CreateBucket(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if out.Name != "<bucket>" || out.ObjectCount != 0 || out.SizeBytes != 0 || out.IsPublic || out.IsVersioned {
			t.Fatalf("unexpected bucket: %+v", out.Bucket)
		}
	}
	if writes.Load() != 2 {
		t.Fatalf("writes = %d, want 2", writes.Load())
	}
}

func TestCreateBucketInvalidNameEnvelope(t *testing.T) {
	var writes atomic.Int32
	c := newTestClient(t, fixtureServer(t, "get_bucket_created.json", "error_envelope_invalid_name.json", &writes))
	_, err := c.CreateBucket(context.Background(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 200 || apiErr.Code != "112" || !strings.Contains(apiErr.Message, "lowercase") {
		t.Fatalf("err = %v, want envelope code 112", err)
	}
	if vngcloud.IsNotFound(err) || vngcloud.IsPermissionDenied(err) || strings.Contains(apiErr.Message, "GetBucket") {
		t.Fatalf("err = %v, want a plain rejection", err)
	}
}

func TestDeleteBucketDecodesFixture(t *testing.T) {
	var writes atomic.Int32
	c := newDeleteClient(t, fixtureServer(t, "get_bucket_created.json", "delete_bucket.json", &writes))
	if _, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", writes.Load())
	}
}

func TestDeleteBucketNotFoundOnDelete(t *testing.T) {
	var writes atomic.Int32
	c := newDeleteClient(t, fixtureServer(t, "get_bucket_created.json", "error_envelope_not_found.json", &writes))
	_, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
}

// The server answers a read made while a delete settles with code -1; the
// read's error must stop DeleteBucket before it sends a second DELETE.
func TestDeleteBucketReadDuringDeletionSendsNoDelete(t *testing.T) {
	var writes atomic.Int32
	c := newDeleteClient(t, fixtureServer(t, "error_envelope_unknown.json", "delete_bucket.json", &writes))
	_, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if vngcloud.ErrorCode(err) != "-1" {
		t.Fatalf("err = %v, want code -1", err)
	}
	if writes.Load() != 0 {
		t.Fatalf("writes = %d, want 0", writes.Load())
	}
}
