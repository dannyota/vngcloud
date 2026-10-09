package storage

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func attach(c *Client) error {
	_, err := c.AttachS3Key(context.Background(), &AttachS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1", ServiceAccountID: "sa-id-1"})
	return err
}

func detach(c *Client) error {
	_, err := c.DetachS3Key(context.Background(), &DetachS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1"})
	return err
}

func TestAttachS3KeyRequestAndFixture(t *testing.T) {
	for _, status := range []int{200, 204} {
		s := &keyServer{status: status, body: fixture(t, "attach_s3_key.json")}
		if status == 204 {
			s.body = ""
		}
		out, err := newTestClient(t, s.handler(t)).AttachS3Key(context.Background(),
			&AttachS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1", ServiceAccountID: "sa-id-1"})
		if err != nil || out == nil {
			t.Fatalf("status %d: out = %+v, err = %v", status, out, err)
		}
		got := s.seen()
		if got.method != http.MethodPut || got.path != keysPath+"/key-1/attach" || got.query != "" ||
			got.body != `{"projectId":"proj-1","serviceAccountId":"sa-id-1"}` || !strings.HasPrefix(got.ctype, "application/json") {
			t.Fatalf("status %d: request = %+v", status, got)
		}
	}
}

func TestDetachS3KeyRequestAndFixture(t *testing.T) {
	for _, status := range []int{200, 204} {
		s := &keyServer{status: status, body: fixture(t, "detach_s3_key.json")}
		if status == 204 {
			s.body = ""
		}
		out, err := newTestClient(t, s.handler(t)).DetachS3Key(context.Background(),
			&DetachS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1"})
		if err != nil || out == nil {
			t.Fatalf("status %d: out = %+v, err = %v", status, out, err)
		}
		got := s.seen()
		if got.method != http.MethodPut || got.path != keysPath+"/key-1/detach" || got.query != "" ||
			got.body != `{"projectId":"proj-1"}` || !strings.HasPrefix(got.ctype, "application/json") {
			t.Fatalf("status %d: request = %+v", status, got)
		}
	}
}

func TestAttachS3KeyRegion(t *testing.T) {
	var header string
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("region")
		testutil.WriteFixture(t, w, fixtures+"attach_s3_key.json")
	})
	_, err := newTestClient(t, h).AttachS3Key(context.Background(),
		&AttachS3KeyInput{Region: "han02", ProjectID: "proj-1", UserKeyID: "key-1", ServiceAccountID: "sa-id-1"})
	if err != nil || header != "<region-id-1>" {
		t.Fatalf("region header = %q, err = %v", header, err)
	}
}

// TestAttachS3KeyRefusalMessages proves each documented code 114 refusal
// reaches the caller as an *APIError with the server's message, and none
// matches a sentinel or says the change may have happened.
func TestAttachS3KeyRefusalMessages(t *testing.T) {
	tests := []struct {
		file string
		op   func(*Client) error
		want string
	}{
		{"error_attach_same_account.json", attach, "This S3 key is already attached with this service account"},
		{"error_attach_other_account.json", attach, "This S3 key is already attached with another service account"},
		{"error_attach_unknown_account.json", attach, "StatusCode=404"},
		{"error_attach_unknown_key.json", attach, "S3 key not found"},
		{"error_detach_unattached.json", detach, "This S3 key is not attached to any service account."},
		{"error_detach_unknown_key.json", detach, "S3 key not found"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			s := &keyServer{status: 200, body: fixture(t, tt.file)}
			err := tt.op(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "114" || apiErr.Message != tt.want || apiErr.StatusCode != 200 {
				t.Fatalf("err = %v, want code 114 with message %q", err, tt.want)
			}
			if vngcloud.IsNotFound(err) || errors.Is(err, vngcloud.ErrInvalidInput) || apiErr.Retryable {
				t.Fatalf("err = %v matched a sentinel or is retryable", err)
			}
		})
	}
}

func TestAttachDetachPathRejection(t *testing.T) {
	bad := []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb"}
	for _, v := range bad {
		var sent atomic.Int32
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
		ctx := context.Background()
		_, e1 := c.AttachS3Key(ctx, &AttachS3KeyInput{ProjectID: v, UserKeyID: "key-1", ServiceAccountID: "sa-id-1"})
		_, e2 := c.AttachS3Key(ctx, &AttachS3KeyInput{ProjectID: "proj-1", UserKeyID: v, ServiceAccountID: "sa-id-1"})
		_, e3 := c.AttachS3Key(ctx, &AttachS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1", ServiceAccountID: v})
		_, e4 := c.DetachS3Key(ctx, &DetachS3KeyInput{ProjectID: v, UserKeyID: "key-1"})
		_, e5 := c.DetachS3Key(ctx, &DetachS3KeyInput{ProjectID: "proj-1", UserKeyID: v})
		for i, err := range []error{e1, e2, e3, e4, e5} {
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("value %q call %d: err = %v, want ErrInvalidInput", v, i, err)
			}
		}
		if sent.Load() != 0 {
			t.Fatalf("value %q: %d request(s) sent, want 0", v, sent.Load())
		}
	}
}

// TestAttachDetachAreSentOnce proves a PUT that the transport would treat as
// idempotent is sent once, and that the error says the change may have
// happened only where the server may have acted.
func TestAttachDetachAreSentOnce(t *testing.T) {
	tests := []struct {
		status   int
		wantHint bool
	}{
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusInternalServerError, true},
		{http.StatusTooManyRequests, false},
		{http.StatusBadRequest, false},
		{http.StatusUnauthorized, false},
	}
	for name, op := range map[string]func(*Client) error{"attach": attach, "detach": detach} {
		for _, tt := range tests {
			t.Run(name+"/"+http.StatusText(tt.status), func(t *testing.T) {
				s := &keyServer{status: tt.status, body: `{"message":"x"}`}
				err := op(New(testutil.NewRetryConfig(t, s.handler(t))))
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
					t.Fatalf("err = %v, want status %d", err, tt.status)
				}
				if got := s.calls.Load(); got != 1 {
					t.Fatalf("%d requests sent, want 1", got)
				}
				if has := strings.Contains(apiErr.Message, "may have happened"); has != tt.wantHint {
					t.Fatalf("message %q: hint = %v, want %v", apiErr.Message, has, tt.wantHint)
				}
				if tt.wantHint {
					if n := strings.Count(err.Error(), "list-s3-keys"); n != 1 {
						t.Fatalf("rendered error %q names list-s3-keys %d times, want 1", err, n)
					}
				}
				if want := tt.status == http.StatusTooManyRequests; apiErr.Retryable != want {
					t.Fatalf("Retryable = %v, want %v", apiErr.Retryable, want)
				}
			})
		}
	}
}

func TestAttachDetachNetworkErrorIsSentOnce(t *testing.T) {
	for name, op := range map[string]func(*Client) error{"attach": attach, "detach": detach} {
		t.Run(name, func(t *testing.T) {
			var puts atomic.Int32
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				puts.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			})
			err := op(New(testutil.NewRetryConfig(t, h)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "may have happened") || apiErr.Retryable {
				t.Fatalf("err = %v, want the may-have-happened hint and not retryable", err)
			}
			if n := strings.Count(err.Error(), "list-s3-keys"); n != 1 {
				t.Fatalf("rendered error %q names list-s3-keys %d times, want 1", err, n)
			}
			if puts.Load() != 1 {
				t.Fatalf("%d PUTs, want 1", puts.Load())
			}
		})
	}
}

func TestAttachDetachFailedDialIsRetryableWithoutHint(t *testing.T) {
	for name, op := range map[string]func(*Client) error{"attach": attach, "detach": detach} {
		t.Run(name, func(t *testing.T) {
			var puts atomic.Int32
			rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return fixtureResponse(t, fixtures+"list_regions.json"), nil
				}
				puts.Add(1)
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
			})
			err := op(New(roundTripConfig(t, rt)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || !apiErr.Retryable || strings.Contains(apiErr.Message, "may have happened") {
				t.Fatalf("err = %v, want a retryable error without the hint", err)
			}
			if puts.Load() != 1 {
				t.Fatalf("%d PUTs, want 1", puts.Load())
			}
		})
	}
}

// TestAttachDetachEmptyResponseSaysChangeMayHaveHappened covers a 200 whose
// body holds no envelope: the server may have acted.
func TestAttachDetachEmptyResponseSaysChangeMayHaveHappened(t *testing.T) {
	for name, op := range map[string]func(*Client) error{"attach": attach, "detach": detach} {
		for _, body := range []string{"", "<html>", "{}"} {
			s := &keyServer{status: 200, body: body}
			err := op(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" || !strings.Contains(apiErr.Message, "may have happened") {
				t.Fatalf("%s body %q: err = %v", name, body, err)
			}
			if n := strings.Count(err.Error(), "may have happened"); n != 1 {
				t.Fatalf("%s body %q: rendered error %q repeats the hint %d times", name, body, err, n)
			}
			if !strings.Contains(err.Error(), "list-s3-keys") {
				t.Fatalf("%s body %q: error %q lacks list-s3-keys", name, body, err)
			}
		}
	}
}

func TestListS3KeysDecodesSubUserID(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "list_s3_keys_attached.json")}
	out, err := newTestClient(t, s.handler(t)).ListS3Keys(context.Background(), &ListS3KeysInput{ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].SubUserID != "<account-user>:sa-<name>" {
		t.Fatalf("items = %+v", out.Items)
	}
}

// TestAttachDetachOtherSuccessStatus proves a 2xx outside the ok list gets
// the may-have-happened advice and is not retried.
func TestAttachDetachOtherSuccessStatus(t *testing.T) {
	for name, op := range map[string]func(*Client) error{"attach": attach, "detach": detach} {
		s := &keyServer{status: http.StatusAccepted}
		err := op(New(testutil.NewRetryConfig(t, s.handler(t))))
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusAccepted || !strings.Contains(apiErr.Message, "list-s3-keys") {
			t.Fatalf("%s: err = %v", name, err)
		}
		if got := s.calls.Load(); got != 1 {
			t.Fatalf("%s: %d PUTs, want 1", name, got)
		}
	}
}
