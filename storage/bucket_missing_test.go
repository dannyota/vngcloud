package storage

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// settingCalls is the eight calls that read a bucket after an empty answer.
func settingCalls() map[string]func(*Client) error {
	return map[string]func(*Client) error{
		"GetBucketVersioning": func(c *Client) error { _, err := getVersioning(c); return err },
		"PutBucketVersioning": func(c *Client) error { return putVersioning(c, ptr(true)) },
		"GetBucketCORS":       func(c *Client) error { _, err := getCORS(c); return err },
		"PutBucketCORS":       func(c *Client) error { return putCORS(c, goodRule()) },
		"DeleteBucketCORS":    deleteCORS,
		"GetBucketPolicy":     func(c *Client) error { _, err := getPolicy(c); return err },
		"PutBucketPolicy":     func(c *Client) error { return putPolicy(c, validPolicy) },
		"DeleteBucketPolicy":  deletePolicy,
	}
}

// missingServer answers every setting route with an empty 200 and counts the
// bucket reads, which it answers with detail.
type missingServer struct {
	detail string
	status int
	reads  atomic.Int32
	writes atomic.Int32
}

func (m *missingServer) handler(t *testing.T) http.Handler {
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == detailsPath {
			m.reads.Add(1)
			if m.status != 0 {
				w.WriteHeader(m.status)
			}
			_, _ = w.Write([]byte(m.detail))
			return
		}
		m.writes.Add(1)
		w.WriteHeader(http.StatusOK)
	})
}

func TestSettingCallsOnAMissingBucketAreNotFound(t *testing.T) {
	for name, op := range settingCalls() {
		t.Run(name, func(t *testing.T) {
			m := &missingServer{detail: fixture(t, "error_envelope_not_found.json")}
			err := op(newTestClient(t, m.handler(t)))
			if !errors.Is(err, vngcloud.ErrNotFound) || !vngcloud.IsNotFound(err) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Operation != "storage."+name {
				t.Fatalf("err = %v, want an *APIError whose Operation is storage.%s", err, name)
			}
			if apiErr.Code == "" || apiErr.Message == "" || !errors.Is(apiErr.Err, vngcloud.ErrNotFound) {
				t.Fatalf("err = %+v, want the read's Code, Message, and ErrNotFound kept", apiErr)
			}
			if m.reads.Load() != 1 || m.writes.Load() != 1 {
				t.Fatalf("settings calls %d, bucket reads %d, want 1 and 1", m.writes.Load(), m.reads.Load())
			}
		})
	}
}

func TestSettingCallsOnAnEmptyAnswerForAnExistingBucketAreEmptyResponse(t *testing.T) {
	cases := map[string]*missingServer{
		"bucket exists":      {detail: fixture(t, "get_bucket.json")},
		"bucket read empty":  {detail: ""},
		"bucket read 500":    {detail: `{"message":"x"}`, status: http.StatusInternalServerError},
		"bucket read denied": {detail: `{"code":403,"success":false,"errorMsg":"no"}`},
	}
	for caseName, m := range cases {
		for name, op := range settingCalls() {
			t.Run(caseName+"/"+name, func(t *testing.T) {
				m.reads.Store(0)
				err := op(newTestClient(t, m.handler(t)))
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" || apiErr.Operation != "storage."+name {
					t.Fatalf("err = %v, want the call's own EmptyResponse", err)
				}
				if vngcloud.IsNotFound(err) {
					t.Fatalf("err = %v matches ErrNotFound", err)
				}
				if m.reads.Load() != 1 {
					t.Fatalf("bucket reads = %d, want 1", m.reads.Load())
				}
				wantWrite := strings.HasPrefix(name, "Put") || strings.HasPrefix(name, "Delete")
				if got := strings.Contains(apiErr.Message, "may have happened"); got != wantWrite {
					t.Fatalf("message %q: says a change may have happened = %v, want %v", apiErr.Message, got, wantWrite)
				}
			})
		}
	}
}

func TestSettingCallsDoNotReadTheBucketAfterOtherAnswers(t *testing.T) {
	answers := map[string]func(w http.ResponseWriter){
		"success":       func(w http.ResponseWriter) { _, _ = w.Write([]byte(fixture(t, "get_bucket_versioning_off.json"))) },
		"envelope code": func(w http.ResponseWriter) { _, _ = w.Write([]byte(fixture(t, "error_cors_method.json"))) },
		"HTTP 400": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"x"}`))
		},
		"HTTP 502": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"x"}`))
		},
		"not JSON": func(w http.ResponseWriter) { _, _ = w.Write([]byte(`<html>`)) },
	}
	for answer, write := range answers {
		for name, op := range settingCalls() {
			t.Run(answer+"/"+name, func(t *testing.T) {
				var reads atomic.Int32
				c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == detailsPath {
						reads.Add(1)
					}
					write(w)
				}))
				_ = op(c)
				// A non-JSON 200 is also an EmptyResponse and does read once.
				want := int32(0)
				if answer == "not JSON" {
					want = 1
				}
				if reads.Load() != want {
					t.Fatalf("bucket reads = %d, want %d", reads.Load(), want)
				}
			})
		}
	}
}

func TestMissingBucketReadUsesTheCallersRegionAndContext(t *testing.T) {
	var region string
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == detailsPath {
			region = r.Header.Get("region")
			testutil.WriteFixture(t, w, fixtures+"error_envelope_not_found.json")
		}
	}))
	_, err := c.GetBucketCORS(context.Background(), &GetBucketCORSInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket"})
	if !vngcloud.IsNotFound(err) || region != "<region-id-1>" {
		t.Fatalf("err = %v, region header %q", err, region)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetBucketCORS(ctx, &GetBucketCORSInput{ProjectID: "proj-1", Bucket: "my-bucket"}); err == nil {
		t.Fatal("a canceled context returned no error")
	}
}
