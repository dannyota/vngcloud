package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

// roundTripFunc is an http.RoundTripper for tests that need a transport
// error no httptest server can produce.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// roundTripConfig builds a retrying Config whose HTTP client is rt.
func roundTripConfig(t *testing.T, rt http.RoundTripper) core.Config {
	t.Helper()
	tc := transport.New(transport.Config{
		HTTPClient: &http.Client{Transport: rt},
		RetryCount: 3, RetryInterval: time.Millisecond,
	})
	return core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Storage: "http://storage.test/"}, tc)
}

func fixtureResponse(t *testing.T, path string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(testutil.FixtureBody(t, path))),
	}
}

const keysPath = "/internal/v1/users/s3_keys"

// keyRequest is what the fake server saw of one key call.
type keyRequest struct {
	method string
	path   string
	query  string
	body   string
	ctype  string
}

// keyServer answers every key call with status and body, and records them.
type keyServer struct {
	status int
	body   string
	calls  atomic.Int32
	last   atomic.Value
}

func (s *keyServer) handler(t *testing.T) http.Handler {
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		b, _ := io.ReadAll(r.Body)
		s.calls.Add(1)
		s.last.Store(keyRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Content-Type")})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	})
}

func (s *keyServer) seen() keyRequest {
	v, _ := s.last.Load().(keyRequest)
	return v
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	return testutil.FixtureBody(t, fixtures+name)
}

func TestListS3KeysRequestAndFixture(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "list_s3_keys.json")}
	out, err := newTestClient(t, s.handler(t)).ListS3Keys(context.Background(), &ListS3KeysInput{ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	got := s.seen()
	if got.method != http.MethodGet || got.path != keysPath || got.query != "projectId=proj-1" || got.body != "" {
		t.Fatalf("request = %+v", got)
	}
	want := S3Key{
		UserKeyID: "<user-key-id-1>", AccessKey: "<access-key-1>", ProjectID: "<project-id>",
		RegionID: "<region-id-2>", UserID: "<account>", CreatedDate: "01/01/2026 00:00", Status: 1,
	}
	if len(out.Items) != 2 || out.Items[0] != want || out.Items[1].UserKeyID != "<user-key-id-2>" {
		t.Fatalf("items = %+v", out.Items)
	}
}

func TestListS3KeysEmpty(t *testing.T) {
	for _, body := range []string{fixture(t, "list_s3_keys_empty.json"), `{"code":200,"success":true,"datas":null}`} {
		s := &keyServer{status: 200, body: body}
		out, err := newTestClient(t, s.handler(t)).ListS3Keys(context.Background(), &ListS3KeysInput{ProjectID: "proj-1"})
		if err != nil || len(out.Items) != 0 {
			t.Fatalf("body %s: out = %+v, err = %v", body, out, err)
		}
	}
}

func TestListS3KeysRegion(t *testing.T) {
	var header string
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("region")
		testutil.WriteFixture(t, w, fixtures+"list_s3_keys_empty.json")
	})
	if _, err := newTestClient(t, h).ListS3Keys(context.Background(), &ListS3KeysInput{Region: "han02", ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if header != "<region-id-1>" {
		t.Fatalf("region header = %q, want the HAN02 UUID", header)
	}
}

func TestCreateS3KeyRequestAndFixture(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "create_s3_key.json")}
	out, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	got := s.seen()
	if got.method != http.MethodPost || got.path != keysPath || got.query != "" ||
		got.body != `{"projectId":"proj-1"}` || !strings.HasPrefix(got.ctype, "application/json") {
		t.Fatalf("request = %+v", got)
	}
	if out.UserKeyID != "<user-key-id-1>" || out.AccessKey != "<access-key-1>" || out.ProjectID != "proj-1" ||
		out.SecretKey.Reveal() != "<secret>" || out.Status != 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestCreateS3KeyAcceptsCreated(t *testing.T) {
	s := &keyServer{status: 201, body: fixture(t, "create_s3_key.json")}
	if _, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteS3KeyRequestAndFixture(t *testing.T) {
	for _, status := range []int{200, 204} {
		s := &keyServer{status: status, body: fixture(t, "delete_s3_key.json")}
		if status == 204 {
			s.body = ""
		}
		_, err := newTestClient(t, s.handler(t)).DeleteS3Key(context.Background(), &DeleteS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1"})
		if err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		got := s.seen()
		if got.method != http.MethodDelete || got.path != keysPath+"/key-1" || got.query != "" ||
			got.body != `{"projectId":"proj-1"}` || !strings.HasPrefix(got.ctype, "application/json") {
			t.Fatalf("status %d: request = %+v", status, got)
		}
	}
}

// keyOps runs each key call against a client, with a fixed valid input.
var keyOps = map[string]func(c *Client) error{
	"ListS3Keys": func(c *Client) error {
		_, err := c.ListS3Keys(context.Background(), &ListS3KeysInput{ProjectID: "proj-1"})
		return err
	},
	"CreateS3Key": func(c *Client) error {
		_, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		return err
	},
	"DeleteS3Key": func(c *Client) error {
		_, err := c.DeleteS3Key(context.Background(), &DeleteS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1"})
		return err
	},
	"AttachS3Key": attach,
	"DetachS3Key": detach,
}

func TestS3KeyErrorStatuses(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusBadRequest, `{"message":"x"}`, nil},
		{http.StatusForbidden, fixture(t, "error_permission.json"), vngcloud.ErrPermission},
		{http.StatusNotFound, `{"message":"x"}`, vngcloud.ErrNotFound},
		{http.StatusConflict, `{"message":"x"}`, nil},
		{http.StatusInternalServerError, `{"message":"x"}`, nil},
	}
	for name, op := range keyOps {
		for _, tt := range tests {
			t.Run(name+"/"+http.StatusText(tt.status), func(t *testing.T) {
				s := &keyServer{status: tt.status, body: tt.body}
				err := op(newTestClient(t, s.handler(t)))
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
					t.Fatalf("err = %v, want *APIError with status %d", err, tt.status)
				}
				if tt.want != nil && !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
				if tt.status == http.StatusForbidden && apiErr.Code != "IAM_PERMISSION_DENIED" {
					t.Fatalf("code = %q", apiErr.Code)
				}
				if tt.status == http.StatusConflict && vngcloud.IsNotFound(err) {
					t.Fatal("409 matched NotFound")
				}
			})
		}
	}
}

func TestS3KeyEnvelopeFailures(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
		want     error
	}{
		{"limit", fixture(t, "error_s3_key_limit.json"), "114", nil},
		{"repeat delete", fixture(t, "error_s3_key_repeat_delete.json"), "114", nil},
		{"invalid input", `{"code":112,"success":false,"errorMsg":"<message>"}`, "112", vngcloud.ErrInvalidInput},
		{"not found", fixture(t, "error_envelope_not_found.json"), "404", vngcloud.ErrNotFound},
	}
	for name, op := range keyOps {
		for _, tt := range tests {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				s := &keyServer{status: 200, body: tt.body}
				err := op(newTestClient(t, s.handler(t)))
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != 200 || apiErr.Code != tt.wantCode {
					t.Fatalf("err = %v, want envelope *APIError with code %s", err, tt.wantCode)
				}
				if tt.want != nil && !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
				if strings.Contains(apiErr.Message, "may exist") {
					t.Fatalf("message %q says a key may exist after a refusal", apiErr.Message)
				}
			})
		}
	}
}

func TestS3KeyEmptyResponse(t *testing.T) {
	for name, op := range keyOps {
		for _, body := range []string{"", "<html>", "{}"} {
			s := &keyServer{status: 200, body: body}
			err := op(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" {
				t.Fatalf("%s body %q: err = %v, want EmptyResponse", name, body, err)
			}
			if name == "CreateS3Key" && !strings.Contains(apiErr.Message, "delete any UserKeyID") {
				t.Fatalf("%s body %q: message %q lacks the list-and-delete advice", name, body, apiErr.Message)
			}
		}
	}
}

func TestCreateS3KeyIncompleteResponse(t *testing.T) {
	bodies := []string{
		`{"code":200,"success":true}`,
		`{"code":200,"success":true,"data":null}`,
		`{"code":200,"success":true,"data":{"accessKey":"<access-key-1>","secretKey":"<secret>"}}`,
		`{"code":200,"success":true,"data":{"userKeyId":"<user-key-id-1>","secretKey":"<secret>"}}`,
		`{"code":200,"success":true,"data":{"userKeyId":123,"accessKey":"a","secretKey":"<secret>"}}`,
		`{"code":200,"success":true,"data":"<secret>"}`,
	}
	for _, body := range bodies {
		s := &keyServer{status: 200, body: body}
		out, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		if out != nil || err == nil || !strings.Contains(err.Error(), "a key may exist") {
			t.Fatalf("body %s: out = %+v, err = %v", body, out, err)
		}
		if strings.Contains(err.Error(), "<secret>") || errors.Is(err, ErrNoSecret) {
			t.Fatalf("body %s: err = %v", body, err)
		}
	}
}

func TestCreateS3KeyWithoutSecretReturnsKeyAndErrNoSecret(t *testing.T) {
	for _, secret := range []string{``, `,"secretKey":""`, `,"secretKey":null`} {
		body := `{"code":200,"success":true,"data":{"userKeyId":"<user-key-id-1>","accessKey":"<access-key-1>"` + secret + `}}`
		s := &keyServer{status: 200, body: body}
		out, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		if !errors.Is(err, ErrNoSecret) {
			t.Fatalf("secret %q: err = %v, want ErrNoSecret", secret, err)
		}
		if out == nil || out.UserKeyID != "<user-key-id-1>" || out.AccessKey != "<access-key-1>" || out.SecretKey.Reveal() != "" {
			t.Fatalf("secret %q: out = %+v, want the key without a secret", secret, out)
		}
	}
}

func TestS3KeyPathRejection(t *testing.T) {
	bad := []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb"}
	for _, v := range bad {
		var sent atomic.Int32
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
		ctx := context.Background()
		_, e1 := c.ListS3Keys(ctx, &ListS3KeysInput{ProjectID: v})
		_, e2 := c.CreateS3Key(ctx, &CreateS3KeyInput{ProjectID: v})
		_, e3 := c.DeleteS3Key(ctx, &DeleteS3KeyInput{ProjectID: v, UserKeyID: "key-1"})
		_, e4 := c.DeleteS3Key(ctx, &DeleteS3KeyInput{ProjectID: "proj-1", UserKeyID: v})
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

func TestS3KeyUnmappedRegionSendsNothing(t *testing.T) {
	var sent atomic.Int32
	c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
	_, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{Region: "HCM99", ProjectID: "proj-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) || sent.Load() != 0 {
		t.Fatalf("err = %v, sent = %d", err, sent.Load())
	}
}

// TestCreateS3KeyIsSentOnce proves the create makes one POST whatever the
// answer, and says a key may exist only where the server may have acted.
func TestCreateS3KeyIsSentOnce(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantHint bool
	}{
		{"502", http.StatusBadGateway, true},
		{"503", http.StatusServiceUnavailable, true},
		{"500", http.StatusInternalServerError, true},
		{"429", http.StatusTooManyRequests, false},
		{"400", http.StatusBadRequest, false},
		{"401", http.StatusUnauthorized, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &keyServer{status: tt.status, body: `{"message":"x"}`}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			_, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v, want status %d", err, tt.status)
			}
			if got := s.calls.Load(); got != 1 {
				t.Fatalf("%d requests sent, want 1", got)
			}
			if has := strings.Contains(apiErr.Message, "delete any UserKeyID"); has != tt.wantHint {
				t.Fatalf("message %q: advice = %v, want %v", apiErr.Message, has, tt.wantHint)
			}
			if tt.wantHint {
				rendered := err.Error()
				for _, want := range []string{"storage.CreateS3Key:", fmt.Sprintf("(status %d)", tt.status), "delete any UserKeyID"} {
					if n := strings.Count(rendered, want); n != 1 {
						t.Fatalf("rendered error %q has %q %d times, want 1", rendered, want, n)
					}
				}
			}
			if tt.status == http.StatusTooManyRequests && !apiErr.Retryable {
				t.Fatal("429 not marked retryable")
			}
		})
	}
}

// TestCreateS3KeyOtherSuccessStatus proves a 2xx outside the create's ok list
// gets the advice, since the server most likely made a key, and is not retried.
func TestCreateS3KeyOtherSuccessStatus(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusNoContent} {
		s := &keyServer{status: status}
		c := New(testutil.NewRetryConfig(t, s.handler(t)))
		out, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		var apiErr *vngcloud.APIError
		if out != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: out = %+v, err = %v", status, out, err)
		}
		if n := strings.Count(err.Error(), "delete any UserKeyID"); n != 1 {
			t.Fatalf("status %d: rendered error %q has the advice %d times, want 1", status, err, n)
		}
		if errors.Is(err, ErrNoSecret) {
			t.Fatalf("status %d: err = %v, want no ErrNoSecret", status, err)
		}
		if got := s.calls.Load(); got != 1 {
			t.Fatalf("status %d: %d POSTs, want 1", status, got)
		}
	}
}

func TestCreateS3KeyNetworkErrorIsSentOnce(t *testing.T) {
	var posts atomic.Int32
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	})
	c := New(testutil.NewRetryConfig(t, h))
	_, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "delete any UserKeyID") {
		t.Fatalf("err = %v, want the list-and-delete advice", err)
	}
	if n := strings.Count(err.Error(), "delete any UserKeyID"); n != 1 {
		t.Fatalf("rendered error %q has the advice %d times, want 1", err, n)
	}
	if posts.Load() != 1 {
		t.Fatalf("%d POSTs, want 1", posts.Load())
	}
}

func TestCreateS3KeyFailedDialPassesThrough(t *testing.T) {
	var posts atomic.Int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return fixtureResponse(t, fixtures+"list_regions.json"), nil
		}
		posts.Add(1)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	})
	c := New(roundTripConfig(t, rt))
	_, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || !apiErr.Retryable || strings.Contains(apiErr.Message, "UserKeyID") {
		t.Fatalf("err = %v, want a retryable error without the advice", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("%d POSTs, want 1", posts.Load())
	}
}

// TestDeleteS3KeyKeepsRetries proves a delete is idempotent: a 503 is retried.
func TestDeleteS3KeyKeepsRetries(t *testing.T) {
	var deletes atomic.Int32
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if deletes.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		testutil.WriteFixture(t, w, fixtures+"delete_s3_key.json")
	})
	c := New(testutil.NewRetryConfig(t, h))
	if _, err := c.DeleteS3Key(context.Background(), &DeleteS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1"}); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 2 {
		t.Fatalf("%d DELETEs, want 2", deletes.Load())
	}
}
