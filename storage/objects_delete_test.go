package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"danny.vn/vngcloud"
)

type deleteTestBody struct {
	reader        io.Reader
	reads, closes int
}

func (b *deleteTestBody) Read(p []byte) (int, error) {
	b.reads++
	if b.reader == nil {
		return 0, errors.New("synthetic body must not be read")
	}
	return b.reader.Read(p)
}
func (b *deleteTestBody) Close() error { b.closes++; return nil }

type deleteTestTransport struct {
	response *http.Response
	err      error
	before   func(*http.Request)
	calls    int
	request  *http.Request
}

func (tr *deleteTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls++
	tr.request = r
	if tr.before != nil {
		tr.before(r)
	}
	return tr.response, tr.err
}

func deleteTestClient(t *testing.T, status int, h http.Header, body io.Reader) (*ObjectClient, *deleteTestTransport, *deleteTestBody) {
	t.Helper()
	c := objectTestClient(t)
	b := &deleteTestBody{reader: body}
	tr := &deleteTestTransport{response: &http.Response{StatusCode: status, Header: h, ContentLength: 999, Body: b}}
	c.httpClient.Transport = tr
	return c, tr, b
}

func deleteTestCall(t *testing.T, c *ObjectClient) (*DeleteObjectOutput, error) {
	t.Helper()
	return c.DeleteObject(t.Context(), &DeleteObjectInput{Bucket: "bucket", Key: "key"})
}

func TestDeleteObjectValidation(t *testing.T) {
	c, tr, _ := deleteTestClient(t, 204, nil, nil)
	for _, in := range []*DeleteObjectInput{nil, {}, {Bucket: "bucket"}, {Bucket: "bad/bucket", Key: "key"}, {Bucket: "bucket", Key: "\xff"}, {Bucket: "bucket", Key: strings.Repeat("k", 1025)}} {
		out, err := c.DeleteObject(t.Context(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || tr.calls != 0 {
			t.Fatalf("out=%v err=%v calls=%d", out, err, tr.calls)
		}
	}
	out, err := deleteTestCall(t, nil)
	if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil client: out=%v err=%v", out, err)
	}
	for _, expired := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		want := context.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		out, err = c.DeleteObject(ctx, &DeleteObjectInput{Bucket: "bucket", Key: "key"})
		cancel()
		if out != nil || !errors.Is(err, want) || errors.Is(err, ErrObjectDeleteUnconfirmed) || tr.calls != 0 {
			t.Fatalf("pre-dispatch out=%v err=%v calls=%d", out, err, tr.calls)
		}
	}
}

func TestDeleteObjectRequest(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		for _, target := range []struct{ key, uri string }{
			{"a/../b//", "/bucket/a/../b//"},
			{"é +%2F?#\n\x00", "/bucket/%C3%A9%20%2B%252F%3F%23%0A%00"},
			{"ending/", "/bucket/ending/"},
			{strings.Repeat("k", 1024), "/bucket/" + strings.Repeat("k", 1024)},
		} {
			c, tr, body := deleteTestClient(t, 204, nil, nil)
			c.region = region
			c.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
			out, err := c.DeleteObject(t.Context(), &DeleteObjectInput{Bucket: "bucket", Key: target.key})
			if out == nil || err != nil || tr.calls != 1 || body.reads != 0 || body.closes != 1 {
				t.Fatalf("out=%v err=%v calls=%d", out, err, tr.calls)
			}
			r := tr.request
			if r.Method != "DELETE" || r.Body != nil && r.Body != http.NoBody || r.ContentLength != 0 || r.GetBody != nil || len(r.TransferEncoding) != 0 {
				t.Fatal("DELETE must be bodyless and unreplayable")
			}
			if r.URL.Host != strings.ToLower(region)+".vstorage.vngcloud.vn" || r.URL.RequestURI() != target.uri || r.URL.RawQuery != "" {
				t.Fatal("exact object address changed")
			}
			if r.Header.Get("X-Amz-Content-Sha256") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" || !strings.Contains(r.Header.Get("Authorization"), "/"+region+"/s3/aws4_request") || !strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
				t.Fatal("DELETE signature scope or empty payload changed")
			}
			_, _, auth := objectReferenceSign(r, r.Header.Get("X-Amz-Content-Sha256"), c.now(), region, c.credentials)
			if r.Header.Get("Authorization") != auth {
				t.Fatal("DELETE signature differs from reference")
			}
			for name := range r.Header {
				if name != "Authorization" && name != "X-Amz-Date" && name != "X-Amz-Content-Sha256" {
					t.Fatalf("unexpected header %s", name)
				}
			}
			if r.Context().Err() != context.Canceled {
				t.Fatal("operation context not released")
			}
		}
	}
}

func TestDeleteObjectSuccessFixture(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/storage/objects/delete_204.http")
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := json.Unmarshal(fixture, &raw); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(raw, "\r\n\r\n") {
		t.Fatal("fixture must include the HTTP header terminator")
	}
	c := objectTestClient(t)
	reader := bufio.NewReader(strings.NewReader(raw))
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent || resp.Body != http.NoBody || resp.ContentLength != 0 {
		t.Fatal("fixture must be a bodyless HTTP 204 response")
	}
	if _, err := reader.Peek(1); !errors.Is(err, io.EOF) {
		t.Fatal("fixture must end after the HTTP headers")
	}
	tr := &deleteTestTransport{response: resp}
	c.httpClient.Transport = tr
	out, err := c.DeleteObject(t.Context(), &DeleteObjectInput{Bucket: "bucket", Key: "missing-name"})
	if err != nil || out == nil || out.DeleteMarker == nil || !*out.DeleteMarker || out.VersionID != "synthetic-version" || tr.calls != 1 {
		t.Fatalf("fixture out=%v err=%v", out, err)
	}
	for _, marker := range []string{"", "true", "false", "invalid"} {
		c, tr, body := deleteTestClient(t, 204, http.Header{"X-Amz-Delete-Marker": {marker}}, nil)
		out, err := deleteTestCall(t, c)
		valid := marker == "true" || marker == "false"
		if err != nil || out == nil || (out.DeleteMarker != nil) != valid || out.VersionID != "" || tr.calls != 1 || body.reads != 0 || body.closes != 1 {
			t.Fatalf("marker=%q out=%v err=%v", marker, out, err)
		}
		if valid && *out.DeleteMarker != (marker == "true") {
			t.Fatal("marker value changed")
		}
	}
}

func TestDeleteObjectBodylessSuccess(t *testing.T) {
	for _, content := range []string{"", "arbitrary body", strings.Repeat("x", 100_000)} {
		c, tr, body := deleteTestClient(t, 204, http.Header{"X-Amz-Delete-Marker": {"bad"}, "X-Amz-Version-Id": {"bad\nvalue"}}, strings.NewReader(content))
		out, err := deleteTestCall(t, c)
		if err != nil || out == nil || out.DeleteMarker != nil || out.VersionID != "" || body.reads != 0 || body.closes != 1 || tr.calls != 1 {
			t.Fatalf("out=%v err=%v reads=%d closes=%d", out, err, body.reads, body.closes)
		}
	}
	// Hold the raw response open without sending its declared length. HTTP 204
	// must finish before the peer supplies any body bytes or closes its socket.
	release := make(chan struct{})
	defer close(release)
	c, connections := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := buffer.WriteString("HTTP/1.1 204 No Content\r\nContent-Length: 999999\r\n\r\n"); err != nil {
			t.Error(err)
			return
		}
		if err := buffer.Flush(); err != nil {
			t.Error(err)
			return
		}
		<-release
	})
	c.timeout = time.Second
	out, err := deleteTestCall(t, c)
	if err != nil || out == nil || connections.Load() != 1 {
		t.Fatalf("loopback out=%v err=%v", out, err)
	}
}

func TestDeleteObjectStatusOutcomes(t *testing.T) {
	for _, status := range []int{200, 201, 202, 206, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 412, 429, 500, 503} {
		c, tr, body := deleteTestClient(t, status, nil, strings.NewReader(`<Error><Code>PreconditionFailed</Code><Message>synthetic-secret-key</Message><Resource>https://private.invalid</Resource></Error>`))
		out, err := deleteTestCall(t, c)
		var api *vngcloud.APIError
		ambiguous := status < 400 || status >= 500
		if out != nil || !errors.As(err, &api) || api.Operation != "storage.DeleteObject" || api.StatusCode != status || errors.Is(err, ErrObjectDeleteUnconfirmed) != ambiguous || ambiguous && api.Retryable || errors.Is(err, ErrObjectExists) || tr.calls != 1 || body.closes != 1 {
			t.Fatalf("status=%d out=%v err=%v", status, out, err)
		}
		for _, item := range []struct {
			status   int
			sentinel error
		}{{401, vngcloud.ErrAuth}, {403, vngcloud.ErrPermission}, {404, vngcloud.ErrNotFound}, {429, vngcloud.ErrRateLimited}} {
			if errors.Is(err, item.sentinel) != (status == item.status) {
				t.Fatalf("status identity %d: %v", status, err)
			}
		}
		assertObjectSafe(t, err, "synthetic-secret-key", "https://private.invalid")
	}
	c, _, body := deleteTestClient(t, 404, nil, strings.NewReader(""))
	out, err := deleteTestCall(t, c)
	if out != nil || !errors.Is(err, vngcloud.ErrNotFound) || errors.Is(err, ErrObjectDeleteUnconfirmed) || body.closes != 1 {
		t.Fatal("bodyless 404 identity lost")
	}
	probe := &objectCountReader{remaining: 100_000}
	c, _, body = deleteTestClient(t, 500, nil, probe)
	out, err = deleteTestCall(t, c)
	if out != nil || !errors.Is(err, ErrObjectDeleteUnconfirmed) || probe.read != 64*1024+1 || body.closes != 1 {
		t.Fatalf("bounded error read=%d err=%v", probe.read, err)
	}
	var calls atomic.Int32
	c, connections := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(429) })
	out, err = deleteTestCall(t, c)
	if out != nil || !errors.Is(err, vngcloud.ErrRateLimited) || calls.Load() != 1 || connections.Load() != 1 {
		t.Fatal("429 retried")
	}
}

func TestDeleteObjectDispatchBoundary(t *testing.T) {
	for _, connected := range []bool{false, true} {
		for _, cause := range []error{errors.New("synthetic-secret-key https://private.invalid"), context.Canceled, context.DeadlineExceeded} {
			c := objectTestClient(t)
			tr := &deleteTestTransport{err: errors.Join(cause, errors.New("synthetic-access-id"))}
			tr.before = func(r *http.Request) {
				if connected {
					httptrace.ContextClientTrace(r.Context()).GotConn(httptrace.GotConnInfo{})
				}
			}
			c.httpClient.Transport = tr
			out, err := deleteTestCall(t, c)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Operation != "storage.DeleteObject" || api.Retryable || errors.Is(err, ErrObjectDeleteUnconfirmed) != connected || tr.calls != 1 {
				t.Fatalf("connected=%t out=%v err=%v", connected, out, err)
			}
			if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
				if !errors.Is(err, cause) {
					t.Fatal("context identity lost")
				}
			}
			assertObjectSafe(t, err, "synthetic-secret-key", "synthetic-access-id", "https://private.invalid")
		}
	}
	synctest.Test(t, func(t *testing.T) {
		for _, connected := range []bool{false, true} {
			c := objectTestClient(t)
			c.timeout = time.Second
			tr := &deleteTestTransport{before: func(r *http.Request) {
				if connected {
					httptrace.ContextClientTrace(r.Context()).GotConn(httptrace.GotConnInfo{})
				}
				<-r.Context().Done()
			}, err: context.DeadlineExceeded}
			c.httpClient.Transport = tr
			out, err := deleteTestCall(t, c)
			if out != nil || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrObjectDeleteUnconfirmed) != connected {
				t.Fatalf("deadline out=%v err=%v", out, err)
			}
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, connections := objectLocalClient(t, func(_ http.ResponseWriter, r *http.Request) { cancel(); <-r.Context().Done() })
	out, err := c.DeleteObject(ctx, &DeleteObjectInput{Bucket: "bucket", Key: "key"})
	if out != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrObjectDeleteUnconfirmed) || connections.Load() != 1 {
		t.Fatalf("cancel out=%v err=%v", out, err)
	}
}
