package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait. The fake clock advances by each sleep's duration, so the wait reaches
// its bound after the same number of reads a real clock would allow.
func withInstantSleep(c *Client) (*Client, *atomic.Int32) {
	var sleeps atomic.Int32
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps.Add(1)
		clock = clock.Add(d)
		return ctx.Err()
	}
	return c, &sleeps
}

func newDeleteClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	c, _ := withInstantSleep(newTestClient(t, h))
	return c
}

// deleteWaitServer answers the pre-delete read with an empty bucket, the
// DELETE with an accepted envelope, and each later read with the next entry
// of reads, repeating the last. An entry is a response body; "" is an empty
// 200.
func deleteWaitServer(t *testing.T, reads []string) (http.Handler, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var deletes, gets atomic.Int32
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == bucketsPath:
			deletes.Add(1)
			_, _ = w.Write([]byte(okEnvelope))
		case r.Method == http.MethodGet && r.URL.Path == detailsPath:
			n := int(gets.Add(1))
			if n == 1 {
				_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"name":"my-bucket","count":0,"size":0,"usedCapacity":0}}`))
				return
			}
			i := min(n-2, len(reads)-1)
			_, _ = w.Write([]byte(reads[i]))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	return h, &deletes, &gets
}

const (
	presentBody  = `{"code":200,"success":true,"data":{"name":"my-bucket","count":0}}`
	unknownBody  = `{"code":-1,"success":false,"errorMsg":"Unknown error"}`
	notFoundBody = `{"code":404,"success":false,"errorMsg":"not found"}`
)

func TestDeleteBucketWaitPollsUntilNotFound(t *testing.T) {
	h, deletes, gets := deleteWaitServer(t, []string{presentBody, unknownBody, "", notFoundBody})
	c, sleeps := withInstantSleep(newTestClient(t, h))
	out, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if err != nil || out == nil {
		t.Fatalf("out = %v, err = %v", out, err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("DELETE sent %d times, want 1", deletes.Load())
	}
	if gets.Load() != 5 || sleeps.Load() != 3 {
		t.Fatalf("reads = %d, sleeps = %d, want 5 and 3", gets.Load(), sleeps.Load())
	}
}

func TestDeleteBucketWaitReturnsOtherErrorAtOnce(t *testing.T) {
	for _, body := range []string{
		`{"code":114,"success":false,"errorMsg":"x"}`,
		`{"code":403,"success":false,"errorMsg":"x"}`,
	} {
		h, deletes, gets := deleteWaitServer(t, []string{presentBody, body})
		c, _ := withInstantSleep(newTestClient(t, h))
		_, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
		if err == nil || !strings.Contains(err.Error(), "the delete was accepted") {
			t.Fatalf("err = %v, want a note that the delete was accepted", err)
		}
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want the read's *APIError kept", err)
		}
		if errors.Is(err, ErrNotSettled) || deletes.Load() != 1 || gets.Load() != 3 {
			t.Fatalf("err = %v, deletes = %d, reads = %d", err, deletes.Load(), gets.Load())
		}
	}
}

func TestDeleteBucketWaitBoundReturnsNotSettled(t *testing.T) {
	h, deletes, gets := deleteWaitServer(t, []string{presentBody})
	c, sleeps := withInstantSleep(newTestClient(t, h))
	_, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !strings.Contains(err.Error(), "do not send it again") {
		t.Fatalf("err = %v, want a note not to repeat the delete", err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("DELETE sent %d times, want 1", deletes.Load())
	}
	// One pre-delete read, then a read at 0 s through 30 s.
	if gets.Load() != 32 || sleeps.Load() != 30 {
		t.Fatalf("reads = %d, sleeps = %d, want 32 and 30", gets.Load(), sleeps.Load())
	}
}

func TestDeleteBucketWaitCanceledContextSaysAccepted(t *testing.T) {
	h, _, _ := deleteWaitServer(t, []string{presentBody})
	c, _ := withInstantSleep(newTestClient(t, h))
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := c.DeleteBucket(ctx, &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "the delete was accepted") {
		t.Fatalf("err = %v, want a canceled error saying the delete was accepted", err)
	}
}

func TestDeleteBucketNoWaitSendsOneDeleteAndNoPoll(t *testing.T) {
	h, deletes, gets := deleteWaitServer(t, []string{presentBody})
	c, sleeps := withInstantSleep(newTestClient(t, h))
	if _, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true}); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 || gets.Load() != 1 || sleeps.Load() != 0 {
		t.Fatalf("deletes = %d, reads = %d, sleeps = %d, want 1, 1, 0", deletes.Load(), gets.Load(), sleeps.Load())
	}
}

func TestDeleteBucketAccepts204(t *testing.T) {
	var deletes atomic.Int32
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == detailsPath:
			if deletes.Load() > 0 {
				_, _ = w.Write([]byte(notFoundBody))
				return
			}
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"name":"my-bucket","count":0}}`))
		case r.Method == http.MethodDelete && r.URL.Path == bucketsPath:
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	if _, err := newDeleteClient(t, h).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("DELETE sent %d times, want 1", deletes.Load())
	}
}

func TestDeleteBucketPreDeleteReadDoesNotAbsorbCodeMinusOne(t *testing.T) {
	for _, body := range []string{unknownBody, ""} {
		var writes atomic.Int32
		h := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				writes.Add(1)
				return
			}
			_, _ = w.Write([]byte(body))
		})
		_, err := newDeleteClient(t, h).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
		if err == nil || !strings.Contains(err.Error(), "storage.DeleteBucket: reading the bucket before the delete: ") {
			t.Fatalf("body %q: err = %v, want the pre-delete read named", body, err)
		}
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || writes.Load() != 0 {
			t.Fatalf("body %q: err = %v, writes = %d", body, err, writes.Load())
		}
	}
}

func TestDeleteBucketRefusesAnyBucketNotProvablyEmpty(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"null count", `{"name":"my-bucket","count":null,"size":0,"usedCapacity":0}`, "did not report an object count"},
		{"absent count", `{"name":"my-bucket","size":0}`, "did not report an object count"},
		{"size above 0", `{"name":"my-bucket","count":0,"size":10}`, "stored data"},
		{"used capacity above 0", `{"name":"my-bucket","count":0,"size":0,"usedCapacity":0.5}`, "stored data"},
		{"objects", `{"name":"my-bucket","count":2}`, "holds 2 objects"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"code":200,"success":true,"data":` + tt.data + `}`))
			}}
			_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
			if !errors.Is(err, ErrBucketNotEmpty) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want ErrBucketNotEmpty containing %q", err, tt.want)
			}
			if got := s.writes.Load(); got != 0 {
				t.Fatalf("%d writes sent, want 0", got)
			}
		})
	}
}

func TestDeleteBucketAcceptsNullSizeAndUsedCapacity(t *testing.T) {
	s := &bucketServer{status: 200, body: okEnvelope}
	s.detail = func(w http.ResponseWriter) {
		if s.deleted.Load() {
			_, _ = w.Write([]byte(notFoundBody))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"name":"my-bucket","count":0,"size":null,"usedCapacity":null}}`))
	}
	if _, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if s.writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", s.writes.Load())
	}
}

func TestEnvelopeCode112MatchesInvalidInput(t *testing.T) {
	var writes atomic.Int32
	c := newTestClient(t, fixtureServer(t, "get_bucket_created.json", "error_envelope_invalid_name.json", &writes))
	_, err := c.CreateBucket(context.Background(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "112" || !strings.Contains(apiErr.Message, "lowercase") || apiErr.StatusCode != 200 {
		t.Fatalf("err = %v, want code 112 and the server message kept", err)
	}
}

func TestEnvelopeCodeMinusOneHasNoSentinel(t *testing.T) {
	var writes atomic.Int32
	c := newTestClient(t, fixtureServer(t, "error_envelope_unknown.json", "delete_bucket.json", &writes))
	_, err := c.GetBucket(context.Background(), &GetBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	if err == nil || errors.Is(err, vngcloud.ErrInvalidInput) || vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want a plain *APIError", err)
	}
}

// A non-dial network failure on the POST leaves the create unknown; a dial
// failure proves the server never saw it.
func TestCreateBucketNetworkFailureNote(t *testing.T) {
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	})
	_, err := newTestClient(t, h).CreateBucket(context.Background(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 0 || apiErr.Retryable {
		t.Fatalf("err = %v, want a status 0 *APIError that is not retryable", err)
	}
	if !strings.Contains(apiErr.Message, "the bucket may exist, check with GetBucket") {
		t.Fatalf("message %q lacks the may-exist note", apiErr.Message)
	}
}

func TestCreateBucketDialFailureHasNoNote(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("x"), vngcloud.WithRetry(0, 0),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: url}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	c.regionIDs = map[string]string{"HCM04": "<region-id-2>"}
	_, err = c.CreateBucket(context.Background(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 0 || !apiErr.Retryable {
		t.Fatalf("err = %v, want a status 0 retryable *APIError", err)
	}
	if strings.Contains(apiErr.Message, "may exist") {
		t.Fatalf("message %q has the may-exist note for a failure that proves no create", apiErr.Message)
	}
}
