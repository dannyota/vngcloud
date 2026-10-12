package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestObjectErrorsBounded(t *testing.T) {
	c := objectTestClient(t)
	for _, tc := range []struct {
		status int
		want   error
	}{{301, nil}, {400, nil}, {401, vngcloud.ErrAuth}, {403, vngcloud.ErrPermission}, {404, vngcloud.ErrNotFound}, {409, nil}, {412, nil}, {429, vngcloud.ErrRateLimited}, {500, nil}, {503, nil}} {
		resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(`<Error><Code>AccessDenied</Code><Message>synthetic-secret-key</Message><Resource>/private</Resource></Error>`))}
		err := c.objectResponseError("storage.GetObject", resp, false)
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.StatusCode != tc.status || api.Code != "AccessDenied" {
			t.Fatalf("error=%v", err)
		}
		for _, sentinel := range []error{vngcloud.ErrAuth, vngcloud.ErrPermission, vngcloud.ErrNotFound, vngcloud.ErrRateLimited} {
			if errors.Is(err, sentinel) != errors.Is(sentinel, tc.want) {
				t.Fatalf("wrong status identity: %v", err)
			}
		}
		assertObjectSafe(t, api, "synthetic-secret-key", "/private")
	}
	for _, code := range []string{"AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "RequestTimeTooSkewed", "NoSuchBucket", "NoSuchKey", "PreconditionFailed", "SlowDown", "InternalError", "ServiceUnavailable"} {
		err := c.objectResponseError("storage.GetObject", &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader("<Error><Code>" + code + "</Code></Error>"))}, false)
		if vngcloud.ErrorCode(err) != code {
			t.Fatalf("code=%s", vngcloud.ErrorCode(err))
		}
	}
	for _, body := range []string{"", "<Error>", "<Other><Code>AccessDenied</Code></Other>",
		"<Error><Code>evil synthetic-access-id</Code></Error>", "<Error/><Error><Code>AccessDenied</Code></Error>",
		"<Error><Code>AccessDenied</Code></Error>junk", "<Error><Code>AccessDenied</Code><Code>NoSuchKey</Code></Error>",
		"<Error><Unknown><Code>AccessDenied</Code></Unknown></Error>",
		"<Error><Code>AccessDenied</Code><Unknown>" + strings.Repeat("x", 64*1024) + "</Unknown></Error>",
		`<!DOCTYPE Error [<!ENTITY secret SYSTEM "file:///private">]><Error><Code>&secret;</Code></Error>`,
	} {
		err := c.objectResponseError("storage.GetObject", &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(body))}, false)
		if vngcloud.ErrorCode(err) != "S3Error" || !errors.Is(err, vngcloud.ErrPermission) {
			t.Fatalf("code=%s error=%v", vngcloud.ErrorCode(err), err)
		}
		assertObjectSafe(t, err, "synthetic-access-id", "/private", "junk")
	}
	for _, createOnly := range []bool{false, true} {
		err := c.objectResponseError("storage.PutObject", &http.Response{StatusCode: 412, Body: http.NoBody}, createOnly)
		if errors.Is(err, ErrObjectExists) != createOnly {
			t.Fatal("incorrect exists identity")
		}
		if createOnly && vngcloud.ErrorCode(err) != "ObjectExists" {
			t.Fatal("incorrect exists code")
		}
	}
	probe := &objectCountReader{remaining: 100_000}
	_ = c.objectResponseError("storage.GetObject", &http.Response{StatusCode: 500, Body: io.NopCloser(probe)}, false)
	if probe.read != 64*1024+1 {
		t.Fatalf("read=%d", probe.read)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF, errors.New("synthetic-secret-key /private")} {
		err := objectAPIError("storage.GetObject", 200, "RequestFailed", cause)
		want := errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, io.ErrUnexpectedEOF)
		if errors.Is(err, cause) != want {
			t.Fatal("unsafe cause retention or missing sentinel")
		}
		assertObjectSafe(t, err, "synthetic-secret-key", "/private")
	}
	if errors.Is(ErrObjectExists, ErrObjectWriteUnconfirmed) {
		t.Fatal("sentinels overlap")
	}
}

type objectCountReader struct{ remaining, read int }

func (r *objectCountReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

func TestObjectMetadataFilter(t *testing.T) {
	c := objectTestClient(t)
	for _, value := range []string{"synthetic-access-id", "prefix synthetic-secret-key suffix", `{"client_secret":"unknown"}`, "line\nbreak", "bad\x7f", strings.Repeat("x", 1025)} {
		if got := c.filterObjectMetadata(value, 1024); got != "" {
			t.Fatal("unsafe metadata retained")
		}
	}
	if got := c.filterObjectMetadata(`"opaque-etag"`, 1024); got != `"opaque-etag"` {
		t.Fatalf("ETag=%q", got)
	}
	for _, credentials := range []ObjectCredentials{
		{AccessKeyID: "id<quote\"slash\\", SecretAccessKey: "secret&quote\"slash\\"},
		{AccessKeyID: "x", SecretAccessKey: "y"},
	} {
		cfg := objectTestConfig()
		cfg.Credentials = credentials
		client, err := NewObjectClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal()} {
			encoded, err := json.Marshal(secret)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"prefix " + secret + " suffix", "prefix " + string(encoded[1:len(encoded)-1]) + " suffix"} {
				if client.filterObjectMetadata(value, 1024) != "" {
					t.Fatal("reflected metadata retained")
				}
			}
		}
		encodedConfig, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct{ Credentials map[string]string }
		if err := json.Unmarshal(encodedConfig, &fields); err != nil {
			t.Fatal(err)
		}
		if fields.Credentials["AccessKeyID"] != "[redacted]" || fields.Credentials["SecretAccessKey"] != "[redacted]" {
			t.Fatal("config exposed credential fields")
		}
		if fmt.Sprintf("%+v", credentials) != "{AccessKeyID:[redacted] SecretAccessKey:[redacted]}" {
			t.Fatal("credential formatting exposed values")
		}
		assertObjectSafe(t, cfg, credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal())
		assertObjectSafe(t, client, credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal())
		assertObjectSafe(t, *client, credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal())
		client.httpClient.CloseIdleConnections()
	}
	if c.filterObjectMetadata("safe", 3) != "" || c.filterObjectMetadata("safe", 0) != "" {
		t.Fatal("metadata bound ignored")
	}
}

func TestObjectErrorsBodyReadFailure(t *testing.T) {
	c := objectTestClient(t)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("synthetic-secret-key /private")} {
		resp := &http.Response{StatusCode: 500, Body: io.NopCloser(objectFailReader{cause})}
		err := c.objectResponseError("storage.GetObject", resp, false)
		if vngcloud.ErrorCode(err) != "S3Error" {
			t.Fatal("partial XML accepted")
		}
		want := errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded)
		if errors.Is(err, cause) != want {
			t.Fatal("context identity lost or raw cause retained")
		}
		assertObjectSafe(t, err, "synthetic-secret-key", "/private")
	}
	for _, extra := range []int{0, 1} {
		prefix, suffix := "<Error><Code>NoSuchKey</Code><Unknown>", "</Unknown></Error>"
		body := prefix + strings.Repeat("x", 64*1024-len(prefix)-len(suffix)+extra) + suffix
		err := c.objectResponseError("storage.GetObject", &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(body))}, false)
		want := "NoSuchKey"
		if extra == 1 {
			want = "S3Error"
		}
		if vngcloud.ErrorCode(err) != want {
			t.Fatal("incorrect XML size boundary")
		}
	}
}

type objectFailReader struct{ err error }

func (r objectFailReader) Read(_ []byte) (int, error) { return 0, r.err }
