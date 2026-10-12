package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"danny.vn/vngcloud"
)

type headTestBody struct {
	reads, closes int
	onClose       func()
}

func (b *headTestBody) Read([]byte) (int, error) {
	b.reads++
	return 0, errors.New("HEAD body must not be read")
}
func (b *headTestBody) Close() error {
	b.closes++
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

type headTestTransport struct {
	calls   int
	request *http.Request
	status  int
	header  http.Header
	body    *headTestBody
	err     error
	before  func(*http.Request)
}

func (tr *headTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls++
	tr.request = r
	if tr.before != nil {
		tr.before(r)
	}
	if tr.err != nil {
		return nil, tr.err
	}
	return &http.Response{StatusCode: tr.status, Header: tr.header, ContentLength: 999, Body: tr.body}, nil
}
func headTestClient(t *testing.T, status int, h http.Header) (*ObjectClient, *headTestTransport) {
	t.Helper()
	c := objectTestClient(t)
	tr := &headTestTransport{status: status, header: h, body: new(headTestBody)}
	c.httpClient.Transport = tr
	return c, tr
}
func headTestCall(t *testing.T, c *ObjectClient) (*HeadObjectOutput, error) {
	t.Helper()
	return c.HeadObject(t.Context(), &HeadObjectInput{Bucket: "bucket", Key: "key"})
}
func assertHeadClosed(t *testing.T, tr *headTestTransport) {
	t.Helper()
	if tr.calls != 1 || tr.body.reads != 0 || tr.body.closes != 1 {
		t.Fatalf("calls=%d reads=%d closes=%d", tr.calls, tr.body.reads, tr.body.closes)
	}
	if tr.request.Context().Err() != context.Canceled {
		t.Fatal("operation context remains active")
	}
}
func TestHeadObjectRequest(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		for _, key := range []string{"a/../b//", "é +%2F?#\n", strings.Repeat("k", 1024)} {
			c, tr := headTestClient(t, 200, nil)
			c.region = region
			out, err := c.HeadObject(t.Context(), &HeadObjectInput{Bucket: "bucket", Key: key})
			if err != nil || out == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			r := tr.request
			sum := sha256.Sum256(nil)
			if r.Method != http.MethodHead || r.Body != nil || r.ContentLength != 0 || r.GetBody != nil || len(r.TransferEncoding) != 0 {
				t.Fatal("request is not bodyless HEAD")
			}
			if r.URL.Host != strings.ToLower(region)+".vstorage.vngcloud.vn" || r.URL.RequestURI() != objectEncodePath("/bucket/"+key) || r.URL.RawQuery != "" {
				t.Fatal("exact object addressing changed")
			}
			if r.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) || !strings.Contains(r.Header.Get("Authorization"), "/"+region+"/s3/aws4_request") || !strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
				t.Fatal("HEAD signing differs")
			}
			for name := range r.Header {
				if name != "Authorization" && name != "X-Amz-Date" && name != "X-Amz-Content-Sha256" {
					t.Fatalf("unexpected header %s", name)
				}
			}
			assertHeadClosed(t, tr)
		}
	}
}
func TestHeadObjectValidation(t *testing.T) {
	for _, in := range []*HeadObjectInput{nil, {}, {Bucket: "bucket"}, {Bucket: "bad/bucket", Key: "key"}, {Bucket: "bucket", Key: "\xff"}, {Bucket: "bucket", Key: strings.Repeat("k", 1025)}} {
		c, tr := headTestClient(t, 200, nil)
		out, err := c.HeadObject(t.Context(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || tr.calls != 0 {
			t.Fatalf("out=%v err=%v calls=%d", out, err, tr.calls)
		}
	}
	var c *ObjectClient
	if out, err := c.HeadObject(t.Context(), &HeadObjectInput{Bucket: "bucket", Key: "key"}); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatal("nil client accepted")
	}
}
func TestHeadObjectLength(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		want int64
		bad  bool
	}{
		{"absent", nil, -1, false}, {"zero", http.Header{"Content-Length": {"0"}}, 0, false},
		{"max", http.Header{"Content-Length": {strconv.FormatInt(math.MaxInt64, 10)}}, math.MaxInt64, false},
		{"large", http.Header{"content-length": {"5000000001"}}, 5000000001, false},
		{"leading zero", http.Header{"CONTENT-LENGTH": {"0001"}}, 1, false},
		{"duplicate", http.Header{"Content-Length": {"1", "1"}}, 0, true},
		{"variants", http.Header{"Content-Length": {"1"}, "content-length": {"1"}}, 0, true},
		{"empty variant", http.Header{"Content-Length": {"1"}, "content-length": nil}, 0, true},
		{"no values", http.Header{"Content-Length": nil}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, tr := headTestClient(t, 200, tc.h)
			out, err := headTestCall(t, c)
			if tc.bad {
				assertHeadInvalid(t, out, err)
			} else if err != nil || out == nil || out.ContentLength != tc.want {
				t.Fatalf("out=%v err=%v", out, err)
			}
			assertHeadClosed(t, tr)
		})
	}
	for _, value := range []string{"", "-1", "+1", " 1", "1 ", "1.0", "1e2", "9223372036854775808", "1\n", "\xff", "synthetic-secret-key"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			c, tr := headTestClient(t, 200, http.Header{"Content-Length": {value}})
			out, err := headTestCall(t, c)
			assertHeadInvalid(t, out, err)
			assertHeadClosed(t, tr)
			assertObjectSafe(t, err, "synthetic-secret-key", "synthetic-access-id")
		})
	}
	c, tr := headTestClient(t, 200, http.Header{"Content-Length": {"123"}})
	c.credentials.AccessKeyID = "23"
	out, err := headTestCall(t, c)
	assertHeadInvalid(t, out, err)
	assertHeadClosed(t, tr)
}
func assertHeadInvalid(t *testing.T, out *HeadObjectOutput, err error) {
	t.Helper()
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || api.Operation != "storage.HeadObject" || api.StatusCode != 200 || api.Code != "InvalidResponse" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}
func TestHeadObjectLengthWire(t *testing.T) {
	// Synthetic raw HTTP/1 responses exercise transport framing, not provider behavior.
	for _, tc := range []struct {
		name, headers string
		bad           bool
	}{
		{"identical", "Content-Length: 9223372036854775807\r\nContent-Length: 9223372036854775807\r\n", false},
		{"conflicting", "Content-Length: 1\r\nContent-Length: 2\r\n", true},
		{"negative", "Content-Length: -1\r\n", true},
		{"bad length", "Content-Length: nope\r\n", true},
		{"bad transfer encoding", "Transfer-Encoding: unknown\r\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, count := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "HEAD" {
					t.Error("non-HEAD request")
				}
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = conn.Close() }()
				_, _ = rw.WriteString("HTTP/1.1 200 OK\r\n" + tc.headers + "Connection: close\r\n\r\n")
				_ = rw.Flush()
			})
			out, err := headTestCall(t, c)
			if tc.bad {
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "RequestFailed" {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else if err != nil || out == nil || out.ContentLength != math.MaxInt64 {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if count.Load() != 1 {
				t.Fatalf("connections=%d", count.Load())
			}
		})
	}
}
func TestHeadObjectMetadata(t *testing.T) {
	h := http.Header{"ETag": {`"safe"`}, "Content-Type": {"application/octet-stream"}, "Last-Modified": {"Tue, 02 Jan 2024 03:04:05 GMT"}, "X-Amz-Version-Id": {"version"}, "X-Amz-Server-Side-Encryption": {"AES256"}, "X-Amz-Meta-LaBel": {"safe"}, "X-Amz-Checksum-Sha256": {"private-checksum"}, "X-Amz-Request-Id": {"private-request"}, "Owner": {"private-owner"}, "Unknown": {"private-unknown"}}
	c, tr := headTestClient(t, 200, h)
	out, err := headTestCall(t, c)
	if err != nil || out == nil || out.ContentLength != -1 || out.ETag != `"safe"` || out.ContentType != "application/octet-stream" || out.VersionID != "version" || out.ServerSideEncryption != "AES256" || out.LastModified == nil || out.LastModified.Location() != time.UTC || out.LastModified.Format(time.RFC3339) != "2024-01-02T03:04:05Z" || !reflect.DeepEqual(out.Metadata, map[string]string{"label": "safe"}) {
		t.Fatalf("out=%v err=%v", out, err)
	}
	assertObjectSafe(t, out, "private-checksum", "private-request", "private-owner", "private-unknown")
	assertHeadClosed(t, tr)
	for _, field := range []struct {
		name  string
		limit int
		get   func(*HeadObjectOutput) string
	}{
		{"ETag", 1024, func(o *HeadObjectOutput) string { return o.ETag }},
		{"Content-Type", 256, func(o *HeadObjectOutput) string { return o.ContentType }},
		{"X-Amz-Version-Id", 1024, func(o *HeadObjectOutput) string { return o.VersionID }},
		{"X-Amz-Server-Side-Encryption", 128, func(o *HeadObjectOutput) string { return o.ServerSideEncryption }},
	} {
		for _, value := range []string{strings.Repeat("v", field.limit), strings.Repeat("v", field.limit+1), "\xff", "a\nb", "synthetic-access-id", "synthetic-secret-key", `client_secret=unknown`} {
			c, tr := headTestClient(t, 200, http.Header{field.name: {value}})
			out, err := headTestCall(t, c)
			want := ""
			if value == strings.Repeat("v", field.limit) {
				want = value
			}
			if err != nil || out == nil || field.get(out) != want {
				t.Fatalf("field=%s out=%v err=%v", field.name, out, err)
			}
			assertHeadClosed(t, tr)
		}
		for _, h := range []http.Header{{field.name: {"same", "same"}}, {field.name: {"safe"}, strings.ToLower(field.name): {"other"}}} {
			c, _ := headTestClient(t, 200, h)
			out, err := headTestCall(t, c)
			if err != nil || out == nil || field.get(out) != "" {
				t.Fatal("duplicate optional header retained")
			}
		}
	}
	for _, value := range []string{"invalid", "2024-01-02T03:04:05Z", strings.Repeat("v", 129), "Tue, 02 Jan 2024 03:04:05 GMT\n"} {
		c, _ := headTestClient(t, 200, http.Header{"Last-Modified": {value}})
		out, err := headTestCall(t, c)
		if err != nil || out == nil || out.LastModified != nil {
			t.Fatal("invalid date retained")
		}
	}
	c, _ = headTestClient(t, 200, http.Header{"Last-Modified": {"Tue, 02 Jan 2024 03:04:05 GMT", "Tue, 02 Jan 2024 03:04:05 GMT"}})
	out, err = headTestCall(t, c)
	if err != nil || out == nil || out.LastModified != nil {
		t.Fatal("duplicate date retained")
	}
	c, _ = headTestClient(t, 200, nil)
	out, err = headTestCall(t, c)
	if err != nil || out == nil || out.Metadata == nil || out.VersionID != "" || out.ServerSideEncryption != "" {
		t.Fatal("absent observations changed")
	}
}
func TestHeadObjectSecurity(t *testing.T) {
	const source = "Tue, 02 Jan 2024 03:04:05 GMT"
	parsed, err := time.Parse(http.TimeFormat, source)
	if err != nil {
		t.Fatal(err)
	}
	utc := parsed.UTC()
	text, _ := utc.MarshalText()
	data, _ := utc.MarshalJSON()
	for _, form := range []string{utc.Format(time.RFC3339Nano), string(text), string(data), fmt.Sprintf("%v", utc), fmt.Sprintf("%+v", utc), fmt.Sprintf("%#v", utc)} {
		c, tr := headTestClient(t, 200, http.Header{"Last-Modified": {source}})
		c.credentials.SecretAccessKey = vngcloud.Secret(form)
		out, err := headTestCall(t, c)
		if err != nil || out == nil || out.LastModified != nil {
			t.Fatal("normalized credential time retained")
		}
		assertObjectSafe(t, out, form)
		assertHeadClosed(t, tr)
		encoded, err := json.Marshal(out)
		if err != nil || !strings.Contains(string(encoded), `"LastModified":null`) {
			t.Fatal("time is not JSON null")
		}
	}
	for _, secret := range []string{"synthetic-secret-key", `a"b\c`} {
		encoded, _ := json.Marshal(secret)
		for _, value := range []string{secret, string(encoded)} {
			c, tr := headTestClient(t, 200, http.Header{"ETag": {value}, "Content-Type": {value}, "X-Amz-Version-Id": {value}, "X-Amz-Server-Side-Encryption": {value}, "X-Amz-Meta-Safe": {value}, "X-Amz-Meta-SYNTHETIC-ACCESS-ID": {"safe"}})
			c.credentials.SecretAccessKey = vngcloud.Secret(secret)
			out, err := headTestCall(t, c)
			if err != nil || out == nil || out.ETag != "" || out.ContentType != "" || out.VersionID != "" || out.ServerSideEncryption != "" || len(out.Metadata) != 0 {
				t.Fatal("reflected metadata retained")
			}
			assertObjectSafe(t, out, secret, "synthetic-access-id")
			assertHeadClosed(t, tr)
		}
	}
	h := http.Header{"X-Amz-Meta-a": {"safe", "safe"}, "X-Amz-Meta-b": {"\xff"}, "X-Amz-Meta-c": {"a\nb"}, "X-Amz-Meta-d": {"synthetic-secret-key"}, "X-Amz-Meta-e.f": {"safe"}, "X-Amz-Meta-Z": {"safe"}, "x-amz-meta-z": {"safe"}}
	for i := 0; i < 65; i++ {
		h[fmt.Sprintf("x-amz-meta-k%02d", i)] = []string{"v"}
	}
	c, _ := headTestClient(t, 200, h)
	out, err := headTestCall(t, c)
	if err != nil || len(out.Metadata) != 64 || out.Metadata["k63"] != "v" {
		t.Fatal("metadata filtering/count budget differs")
	}
	if _, ok := out.Metadata["k64"]; ok {
		t.Fatal("metadata count exceeded")
	}
	h = http.Header{"x-amz-meta-a": {strings.Repeat("v", 2048)}, "x-amz-meta-b": {strings.Repeat("v", 2048)}, "x-amz-meta-c": {strings.Repeat("v", 2048)}, "x-amz-meta-d": {strings.Repeat("v", 2044)}, "x-amz-meta-e": {""}, "x-amz-meta-" + strings.Repeat("n", 129): {"safe"}, "x-amz-meta-f": {strings.Repeat("v", 2049)}}
	c, _ = headTestClient(t, 200, h)
	out, err = headTestCall(t, c)
	if err != nil || len(out.Metadata) != 4 || len(out.Metadata["d"]) != 2044 {
		t.Fatal("metadata byte budget differs")
	}
}
func TestHeadObjectOutcomes(t *testing.T) {
	for _, status := range []int{204, 206, 301, 302, 307, 308, 400, 401, 403, 404, 409, 412, 429, 500, 501, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c, tr := headTestClient(t, status, nil)
			out, err := headTestCall(t, c)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Operation != "storage.HeadObject" || api.StatusCode != status || api.Code != "S3Error" || api.Retryable != (status == 429) {
				t.Fatalf("out=%v err=%v", out, err)
			}
			for _, s := range []struct {
				status int
				err    error
			}{{401, vngcloud.ErrAuth}, {403, vngcloud.ErrPermission}, {404, vngcloud.ErrNotFound}, {429, vngcloud.ErrRateLimited}, {412, ErrObjectExists}} {
				if errors.Is(err, s.err) != (status == s.status && !errors.Is(s.err, ErrObjectExists)) {
					t.Fatal("wrong status sentinel")
				}
			}
			assertHeadClosed(t, tr)
		})
	}
	c, tr := headTestClient(t, 200, nil)
	tr.err = errors.New("synthetic-secret-key https://peer.invalid/private synthetic-object-bytes")
	out, err := headTestCall(t, c)
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "RequestFailed" || api.StatusCode != 0 || api.Err != nil || tr.calls != 1 {
		t.Fatal("unsafe transport error retained")
	}
	assertObjectSafe(t, err, "synthetic-secret-key", "https://peer.invalid/private", "synthetic-object-bytes")
}
func TestHeadObjectContext(t *testing.T) {
	for _, status := range []int{200, 403, 404, 429, 500} {
		for _, mode := range []string{"cancel before response", "cancel on close", "operation timeout", "caller timeout"} {
			t.Run(fmt.Sprintf("%d/%s", status, mode), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					c, tr := headTestClient(t, status, nil)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					want := context.Canceled
					switch mode {
					case "cancel before response":
						tr.before = func(*http.Request) { cancel() }
					case "cancel on close":
						tr.body.onClose = cancel
					case "operation timeout":
						c.timeout = time.Second
						tr.before = func(r *http.Request) { <-r.Context().Done() }
						want = context.DeadlineExceeded
					case "caller timeout":
						c.timeout = time.Hour
						var end context.CancelFunc
						ctx, end = context.WithTimeout(ctx, time.Second)
						defer end()
						tr.before = func(r *http.Request) { <-r.Context().Done() }
						want = context.DeadlineExceeded
					}
					out, err := c.HeadObject(ctx, &HeadObjectInput{Bucket: "bucket", Key: "key"})
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != status || !errors.Is(err, want) {
						t.Fatalf("out=%v err=%v", out, err)
					}
					if errors.Is(err, vngcloud.ErrPermission) != (status == 403) || errors.Is(err, vngcloud.ErrNotFound) != (status == 404) || errors.Is(err, vngcloud.ErrRateLimited) != (status == 429) {
						t.Fatal("context erased status identity")
					}
					if tr.calls != 1 || tr.body.reads != 0 || tr.body.closes != 1 {
						t.Fatal("context path read body or failed to close")
					}
				})
			})
		}
	}
}
