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

const versioningPath = bucketsPath + "/versioning"

func getVersioning(c *Client) (*GetBucketVersioningOutput, error) {
	return c.GetBucketVersioning(context.Background(), &GetBucketVersioningInput{ProjectID: "proj-1", Bucket: "my-bucket"})
}

func putVersioning(c *Client, enabled *bool) error {
	_, err := c.PutBucketVersioning(context.Background(), &PutBucketVersioningInput{ProjectID: "proj-1", Bucket: "my-bucket", Enabled: enabled})
	return err
}

func ptr(b bool) *bool { return &b }

func TestGetBucketVersioningFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		enabled bool
		status  string
	}{
		{"get_bucket_versioning_off.json", false, "Off"},
		{"get_bucket_versioning_enabled.json", true, "Enabled"},
		{"get_bucket_versioning_suspended.json", false, "Suspended"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			s := &keyServer{status: 200, body: fixture(t, tt.fixture)}
			out, err := getVersioning(newTestClient(t, s.handler(t)))
			if err != nil {
				t.Fatal(err)
			}
			if out.Enabled != tt.enabled || out.Status != tt.status {
				t.Fatalf("out = %+v, want Enabled %v and Status %q", out, tt.enabled, tt.status)
			}
			got := s.seen()
			if got.method != http.MethodGet || got.path != versioningPath || got.query != "" || got.body != "" {
				t.Fatalf("request = %+v", got)
			}
		})
	}
}

func TestGetBucketVersioningPassesUnknownStatusThrough(t *testing.T) {
	s := &keyServer{status: 200, body: `{"code":200,"success":true,"data":{"versioning":false,"versioningStatus":"Paused"}}`}
	out, err := getVersioning(newTestClient(t, s.handler(t)))
	if err != nil || out.Status != "Paused" || out.Enabled {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestGetBucketVersioningWithoutDataIsADecodeError(t *testing.T) {
	for name, body := range map[string]string{
		"no data":      okEnvelope,
		"null data":    `{"code":200,"success":true,"data":null}`,
		"data is true": `{"code":200,"success":true,"data":true}`,
		"data is text": `{"code":200,"success":true,"data":"Enabled"}`,
		"wrong type":   `{"code":200,"success":true,"data":{"versioning":"yes","versioningStatus":"Enabled"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := &keyServer{status: 200, body: body}
			out, err := getVersioning(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if out != nil || !errors.As(err, &apiErr) || apiErr.Operation != "storage.GetBucketVersioning" || apiErr.Code != "InvalidResponse" {
				t.Fatalf("out = %+v, err = %v, want an InvalidResponse error", out, err)
			}
		})
	}
}

func TestPutBucketVersioningBody(t *testing.T) {
	for _, tt := range []struct {
		enabled bool
		body    string
	}{{true, `{"enable":true}`}, {false, `{"enable":false}`}} {
		for _, status := range []int{200, 204} {
			s := &keyServer{status: status, body: fixture(t, "put_bucket_versioning.json")}
			if status == 204 {
				s.body = ""
			}
			c := newTestClient(t, s.handler(t))
			if err := putVersioning(c, ptr(tt.enabled)); err != nil {
				t.Fatalf("enabled %v, status %d: %v", tt.enabled, status, err)
			}
			got := s.seen()
			if got.method != http.MethodPut || got.path != versioningPath || got.query != "" || got.body != tt.body || !strings.HasPrefix(got.ctype, "application/json") {
				t.Fatalf("enabled %v, status %d: request = %+v", tt.enabled, status, got)
			}
		}
	}
}

func TestPutBucketVersioningNilEnabledSendsNothing(t *testing.T) {
	var sent atomic.Int32
	c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
	err := putVersioning(c, nil)
	if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), "Enabled") {
		t.Fatalf("err = %v, want ErrInvalidInput naming Enabled", err)
	}
	if sent.Load() != 0 {
		t.Fatalf("%d request(s) sent, want 0", sent.Load())
	}
}

func TestBucketVersioningRegion(t *testing.T) {
	var headers []string
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("region"), r.Header.Get("region_id"))
		_, _ = w.Write([]byte(fixture(t, "get_bucket_versioning_off.json")))
	}))
	ctx := context.Background()
	if _, err := c.GetBucketVersioning(ctx, &GetBucketVersioningInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutBucketVersioning(ctx, &PutBucketVersioningInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket", Enabled: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	for i, h := range headers {
		if h != "<region-id-1>" {
			t.Fatalf("header %d = %q, want the HAN02 region id", i, h)
		}
	}
	if len(headers) != 4 {
		t.Fatalf("headers = %v", headers)
	}
}

func TestBucketVersioningPathRejection(t *testing.T) {
	for _, v := range []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb"} {
		var sent atomic.Int32
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
		ctx := context.Background()
		_, e1 := c.GetBucketVersioning(ctx, &GetBucketVersioningInput{ProjectID: v, Bucket: "my-bucket"})
		_, e2 := c.GetBucketVersioning(ctx, &GetBucketVersioningInput{ProjectID: "proj-1", Bucket: v})
		_, e3 := c.PutBucketVersioning(ctx, &PutBucketVersioningInput{ProjectID: v, Bucket: "my-bucket", Enabled: ptr(true)})
		_, e4 := c.PutBucketVersioning(ctx, &PutBucketVersioningInput{ProjectID: "proj-1", Bucket: v, Enabled: ptr(true)})
		for i, err := range []error{e1, e2, e3, e4} {
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("value %q call %d: err = %v, want ErrInvalidInput", v, i, err)
			}
		}
		if sent.Load() != 0 {
			t.Fatalf("value %q: %d request(s) sent, want 0", v, sent.Load())
		}
	}
}

func TestBucketVersioningStatuses(t *testing.T) {
	ops := map[string]func(*Client) error{
		"get": func(c *Client) error { _, err := getVersioning(c); return err },
		"put": func(c *Client) error { return putVersioning(c, ptr(true)) },
	}
	for name, op := range ops {
		for _, tt := range statusCases {
			t.Run(name+"/"+http.StatusText(tt.status), func(t *testing.T) {
				checkStatusCase(t, tt, func(body string, status int) error {
					s := &keyServer{status: status, body: body}
					return op(newTestClient(t, s.handler(t)))
				}, fixture(t, "get_bucket_versioning_off.json"))
			})
		}
	}
}

func TestBucketVersioningEnvelopeCodes(t *testing.T) {
	notFound := fixture(t, "error_envelope_not_found.json")
	tests := []struct {
		name     string
		body     string
		op       func(*Client) error
		code     string
		sentinel error
	}{
		{"get code 404", notFound, func(c *Client) error { _, err := getVersioning(c); return err }, "404", vngcloud.ErrNotFound},
		{"put code 404", notFound, func(c *Client) error { return putVersioning(c, ptr(true)) }, "404", vngcloud.ErrNotFound},
		{"put code 112", `{"code":112,"success":false,"errorMsg":"bad input"}`, func(c *Client) error { return putVersioning(c, ptr(true)) }, "112", vngcloud.ErrInvalidInput},
		{"put code 400", fixture(t, "error_cors_malformed.json"), func(c *Client) error { return putVersioning(c, ptr(true)) }, "400", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &keyServer{status: 200, body: tt.body}
			err := tt.op(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tt.code || apiErr.StatusCode != 200 {
				t.Fatalf("err = %v, want code %s", err, tt.code)
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Fatalf("err = %v, want %v", err, tt.sentinel)
			}
		})
	}
}

func TestPutBucketVersioningKeepsTheTransportRetries(t *testing.T) {
	var calls atomic.Int32
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(okEnvelope))
	})
	if err := putVersioning(New(testutil.NewRetryConfig(t, h)), ptr(false)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want a retry after the 502", calls.Load())
	}
}
