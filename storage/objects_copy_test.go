package storage

import (
	"context"
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

const copyTestResult = `<CopyObjectResult><ETag>"opaque-etag"</ETag></CopyObjectResult>`

type copyTestBody struct {
	reader io.Reader
	closes int
}

func (b *copyTestBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *copyTestBody) Close() error               { b.closes++; return nil }

type copyTestTransport struct {
	response *http.Response
	err      error
	before   func(*http.Request)
	calls    int
	request  *http.Request
}

func (tr *copyTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls++
	tr.request = r
	if tr.before != nil {
		tr.before(r)
	}
	return tr.response, tr.err
}
func copyTestClient(t *testing.T, status int, h http.Header, r io.Reader) (*ObjectClient, *copyTestTransport, *copyTestBody) {
	t.Helper()
	c := objectTestClient(t)
	body := &copyTestBody{reader: r}
	tr := &copyTestTransport{response: &http.Response{StatusCode: status, Header: h, Body: body}}
	c.httpClient.Transport = tr
	return c, tr, body
}
func copyTestInput() *CopyObjectInput {
	return &CopyObjectInput{SourceBucket: "source", SourceKey: "key", Bucket: "destination", Key: "key", Replace: true}
}
func copyTestCall(t *testing.T, c *ObjectClient) (*CopyObjectOutput, error) {
	t.Helper()
	return c.CopyObject(t.Context(), copyTestInput())
}

func TestCopyObjectValidation(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		c, tr, _ := copyTestClient(t, 200, nil, strings.NewReader(copyTestResult))
		c.region = region
		inputs := []*CopyObjectInput{nil, {}, {SourceBucket: "source", SourceKey: "key", Bucket: "destination", Key: "key"}, {SourceBucket: "source", SourceKey: "key", Bucket: "source", Key: "key", Replace: true}}
		for _, source := range []bool{false, true} {
			for _, bad := range []struct{ bucket, key string }{{"bad/bucket", "key"}, {"", "key"}, {"bucket", ""}, {"bucket", "\xff"}, {"bucket", strings.Repeat("k", 1025)}} {
				in := copyTestInput()
				if source {
					in.SourceBucket, in.SourceKey = bad.bucket, bad.key
				} else {
					in.Bucket, in.Key = bad.bucket, bad.key
				}
				inputs = append(inputs, in)
			}
		}
		for _, in := range inputs {
			out, err := c.CopyObject(t.Context(), in)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || tr.calls != 0 {
				t.Fatalf("validation out=%v err=%v calls=%d", out, err, tr.calls)
			}
			if in != nil && !in.Replace && !strings.Contains(err.Error(), "native create-only copy is unavailable") {
				t.Fatal("missing default refusal explanation")
			}
		}
		out, err := copyTestCall(t, nil)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatal("nil client accepted")
		}
	}
}

func TestCopyObjectRequest(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		for _, target := range []struct{ key, path string }{{"a/../b//", "a/../b//"}, {"é +%2F%?#\n\x00", "%C3%A9%20%2B%252F%25%3F%23%0A%00"}, {"ending/", "ending/"}, {strings.Repeat("k", 1024), strings.Repeat("k", 1024)}} {
			c, tr, body := copyTestClient(t, 200, nil, strings.NewReader(copyTestResult))
			c.region = region
			c.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
			in := copyTestInput()
			in.SourceKey = target.key
			in.Key = target.key
			out, err := c.CopyObject(t.Context(), in)
			if err != nil || out == nil || tr.calls != 1 || body.closes != 1 {
				t.Fatalf("out=%v err=%v calls=%d", out, err, tr.calls)
			}
			r := tr.request
			if r.Method != "PUT" || r.Body != nil && r.Body != http.NoBody || r.GetBody != nil || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
				t.Fatal("copy is not bodyless")
			}
			if r.URL.Host != strings.ToLower(region)+".vstorage.vngcloud.vn" || r.URL.RequestURI() != "/destination/"+target.path || r.URL.RawQuery != "" || r.Header.Get("X-Amz-Copy-Source") != "/source/"+target.path {
				t.Fatal("literal addresses changed")
			}
			const hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
			if r.Header.Get("X-Amz-Content-Sha256") != hash {
				t.Fatal("wrong payload hash")
			}
			canonical, _, auth := objectReferenceSign(r.Clone(t.Context()), hash, c.now(), region, c.credentials)
			expected := "PUT\n/destination/" + target.path + "\n\nhost:" + r.URL.Host + "\nx-amz-content-sha256:" + hash + "\nx-amz-copy-source:/source/" + target.path + "\nx-amz-date:20231114T221320Z\n\nhost;x-amz-content-sha256;x-amz-copy-source;x-amz-date\n" + hash
			if canonical != expected || r.Header.Get("Authorization") != auth {
				t.Fatal("canonical copy signature differs")
			}
			for name := range r.Header {
				if name != "Authorization" && name != "X-Amz-Copy-Source" && name != "X-Amz-Date" && name != "X-Amz-Content-Sha256" {
					t.Fatalf("unapproved header %s", name)
				}
			}
			if r.Context().Err() != context.Canceled {
				t.Fatal("context not released")
			}
		}
	}
}

func TestCopyObjectFixtures(t *testing.T) {
	for _, name := range []string{"copy_result.xml", "copy_error.xml"} {
		raw, err := os.ReadFile("../testdata/storage/objects/" + name)
		if err != nil {
			t.Fatal(err)
		}
		c, tr, body := copyTestClient(t, 200, nil, strings.NewReader(string(raw)))
		out, err := copyTestCall(t, c)
		if tr.calls != 1 || body.closes != 1 {
			t.Fatal("fixture dispatch or closure")
		}
		if name == "copy_error.xml" {
			if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || vngcloud.ErrorCode(err) != "InternalError" {
				t.Fatalf("error fixture: %v", err)
			}
			assertObjectSafe(t, err, "synthetic-secret-key", "synthetic-private-resource")
		} else if err != nil || out == nil || out.ETag != `"synthetic-etag"` || out.LastModified == nil || out.LastModified.Location() != time.UTC {
			t.Fatalf("result fixture: out=%v err=%v", out, err)
		}
	}
}

func TestCopyObjectResultBounds(t *testing.T) {
	prefix, suffix := `<CopyObjectResult><ETag>e</ETag><Unknown>`, `</Unknown></CopyObjectResult>`
	depth := func(n int) string {
		return `<CopyObjectResult><ETag>e</ETag>` + strings.Repeat("<U>", n-1) + strings.Repeat("</U>", n-1) + `</CopyObjectResult>`
	}
	for _, tc := range []struct {
		name, xml string
		valid     bool
	}{
		{"plain", copyTestResult, true}, {"namespace", `<CopyObjectResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ETag>e</ETag><LastModified>bad</LastModified></CopyObjectResult>`, true},
		{"prefixed", `<s:CopyObjectResult xmlns:s="http://s3.amazonaws.com/doc/2006-03-01/"><s:ETag>e</s:ETag></s:CopyObjectResult>`, true},
		{"declaration", `<?xml version="1.0"?> <!--before-->` + copyTestResult + ` <!--after--> `, true},
		{"exact", prefix + strings.Repeat("x", 64*1024-len(prefix)-len(suffix)) + suffix, true},
		{"overflow", prefix + strings.Repeat("x", 64*1024-len(prefix)-len(suffix)+1) + suffix, false},
		{"depth16", depth(16), true}, {"depth17", depth(17), false},
		{"empty", "", false}, {"short", "<", false}, {"truncated", `<CopyObjectResult><ETag>e</ETag>`, false},
		{"missing", `<CopyObjectResult/>`, false}, {"emptyetag", `<CopyObjectResult><ETag/></CopyObjectResult>`, false},
		{"duplicate", `<CopyObjectResult><ETag>e</ETag><ETag>e</ETag></CopyObjectResult>`, false},
		{"duplicatedate", `<CopyObjectResult><ETag>e</ETag><LastModified/><LastModified/></CopyObjectResult>`, false},
		{"nested", `<CopyObjectResult><ETag><U>e</U></ETag></CopyObjectResult>`, false},
		{"nesteddate", `<CopyObjectResult><ETag>e</ETag><LastModified><U/></LastModified></CopyObjectResult>`, false},
		{"wrongroot", `<Other><ETag>e</ETag></Other>`, false},
		{"wrongns", `<CopyObjectResult xmlns="urn:wrong"><ETag>e</ETag></CopyObjectResult>`, false},
		{"qualifiedetag", `<CopyObjectResult><x:ETag xmlns:x="urn:wrong">e</x:ETag></CopyObjectResult>`, false},
		{"qualifieddate", `<CopyObjectResult><ETag>e</ETag><x:LastModified xmlns:x="urn:wrong"/></CopyObjectResult>`, false},
		{"unknown", `<CopyObjectResult><Unknown><ETag>ignored</ETag></Unknown><ETag>e</ETag></CopyObjectResult>`, true},
		{"invalidutf8", `<CopyObjectResult><ETag>` + "\xff" + `</ETag></CopyObjectResult>`, false},
		{"entity", `<CopyObjectResult><ETag>&unknown;</ETag></CopyObjectResult>`, false},
		{"directive", `<!DOCTYPE CopyObjectResult>` + copyTestResult, false},
		{"pi", copyTestResult + `<?other value?>`, false}, {"secondxml", copyTestResult + `<?xml version="1.0"?>`, false},
		{"multiple", copyTestResult + copyTestResult, false}, {"trailing", copyTestResult + "junk", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, tr, b := copyTestClient(t, 200, nil, strings.NewReader(tc.xml))
			out, err := copyTestCall(t, c)
			if (err == nil) != tc.valid || (out != nil) != tc.valid || tr.calls != 1 || b.closes != 1 {
				t.Fatalf("valid=%t out=%v err=%v", tc.valid, out, err)
			}
			if !tc.valid && (!errors.Is(err, ErrObjectCopyUnconfirmed) || vngcloud.ErrorCode(err) != "InvalidResponse") {
				t.Fatalf("wrong malformed classification: %v", err)
			}
		})
	}
	for _, cause := range []error{errors.New("synthetic-secret-key"), context.Canceled, context.DeadlineExceeded} {
		c, _, b := copyTestClient(t, 200, nil, objectFailReader{cause})
		out, err := copyTestCall(t, c)
		if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || vngcloud.ErrorCode(err) != "RequestFailed" || b.closes != 1 {
			t.Fatalf("read loss: %v", err)
		}
		if (errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded)) && !errors.Is(err, cause) {
			t.Fatal("context identity lost")
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
}

func TestCopyObjectOutcome(t *testing.T) {
	for _, status := range []int{201, 202, 204, 206, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 412, 429, 500, 501, 502, 503, 504} {
		c, tr, b := copyTestClient(t, status, nil, strings.NewReader(`<Error><Code>NotImplemented</Code><Message>synthetic-secret-key</Message></Error>`))
		out, err := copyTestCall(t, c)
		var api *vngcloud.APIError
		ambiguous := status < 400 || status >= 500
		if out != nil || !errors.As(err, &api) || api.Operation != "storage.CopyObject" || api.StatusCode != status || api.Code != "NotImplemented" || errors.Is(err, ErrObjectCopyUnconfirmed) != ambiguous || api.Retryable != (status == 429) || tr.calls != 1 || b.closes != 1 || errors.Is(err, ErrObjectExists) {
			t.Fatalf("status=%d err=%v", status, err)
		}
		for _, mapping := range []struct {
			status   int
			sentinel error
		}{{401, vngcloud.ErrAuth}, {403, vngcloud.ErrPermission}, {404, vngcloud.ErrNotFound}, {429, vngcloud.ErrRateLimited}} {
			if errors.Is(err, mapping.sentinel) != (status == mapping.status) {
				t.Fatal("status identity lost")
			}
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
	for _, connected := range []bool{false, true} {
		for _, cause := range []error{errors.New("synthetic-secret-key"), context.Canceled, context.DeadlineExceeded} {
			c := objectTestClient(t)
			tr := &copyTestTransport{err: cause, before: func(r *http.Request) {
				if connected {
					httptrace.ContextClientTrace(r.Context()).GotConn(httptrace.GotConnInfo{})
				}
			}}
			c.httpClient.Transport = tr
			out, err := copyTestCall(t, c)
			if out != nil || errors.Is(err, ErrObjectCopyUnconfirmed) != connected || vngcloud.ErrorCode(err) != "RequestFailed" || tr.calls != 1 {
				t.Fatalf("connected=%t err=%v", connected, err)
			}
			if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
				if !errors.Is(err, cause) {
					t.Fatal("context identity lost")
				}
			}
			assertObjectSafe(t, err, "synthetic-secret-key")
		}
	}
	for _, expired := range []bool{false, true} {
		c, tr, _ := copyTestClient(t, 200, nil, strings.NewReader(copyTestResult))
		ctx, cancel := context.WithCancel(t.Context())
		want := context.Canceled
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
			want = context.DeadlineExceeded
		} else {
			cancel()
		}
		out, err := c.CopyObject(ctx, copyTestInput())
		cancel()
		if out != nil || !errors.Is(err, want) || errors.Is(err, ErrObjectCopyUnconfirmed) || tr.calls != 0 {
			t.Fatalf("pre-dispatch: %v", err)
		}
	}
}

func TestCopyObjectDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, connected := range []bool{false, true} {
			c := objectTestClient(t)
			c.timeout = time.Second
			tr := &copyTestTransport{err: context.DeadlineExceeded, before: func(r *http.Request) {
				if connected {
					httptrace.ContextClientTrace(r.Context()).GotConn(httptrace.GotConnInfo{})
				}
				<-r.Context().Done()
			}}
			c.httpClient.Transport = tr
			out, err := copyTestCall(t, c)
			if out != nil || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrObjectCopyUnconfirmed) != connected || tr.calls != 1 {
				t.Fatalf("acquisition deadline: %v", err)
			}
		}
		c := objectTestClient(t)
		c.timeout = time.Second
		body := &copyBlockingBody{closed: make(chan struct{})}
		tr := &copyTestTransport{response: &http.Response{StatusCode: 200, Body: body}}
		c.httpClient.Transport = tr
		out, err := copyTestCall(t, c)
		if out != nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrObjectCopyUnconfirmed) || tr.calls != 1 {
			t.Fatalf("deadline: %v", err)
		}
	})
}

type copyBlockingBody struct{ closed chan struct{} }

func (b *copyBlockingBody) Read(_ []byte) (int, error) {
	<-b.closed
	return 0, errors.New("synthetic body closed")
}
func (b *copyBlockingBody) Close() error { close(b.closed); return nil }

func TestCopyObjectRawRequest(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		var requests atomic.Int32
		const key = "a/../é +%2F%?#\n\x00//"
		const path = "a/../%C3%A9%20%2B%252F%25%3F%23%0A%00//"
		stamp := time.Unix(1_700_000_000, 0)
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			data, err := io.ReadAll(r.Body)
			if err != nil || len(data) != 0 || r.ContentLength != 0 || r.Header.Get("Content-Length") != "0" || len(r.TransferEncoding) != 0 || r.Method != "PUT" || r.RequestURI != "/destination/"+path || r.URL.RawQuery != "" || r.Header.Get("X-Amz-Copy-Source") != "/source/"+path {
				t.Error("raw copy request changed")
			}
			reference := r.Clone(t.Context())
			// net/http adds framing and connection headers after signing.
			for _, name := range []string{"User-Agent", "Content-Length", "Connection"} {
				reference.Header.Del(name)
			}
			reference.Host = strings.ToLower(region) + ".vstorage.vngcloud.vn"
			_, _, auth := objectReferenceSign(reference, objectTestHash(""), stamp, region, objectTestConfig().Credentials)
			if auth != r.Header.Get("Authorization") {
				t.Error("wire signature differs")
			}
			for name := range r.Header {
				switch name {
				case "Authorization", "X-Amz-Date", "X-Amz-Content-Sha256", "X-Amz-Copy-Source", "Content-Length", "User-Agent", "Connection":
				default:
					t.Errorf("unexpected wire header: %s", name)
				}
			}
			_, _ = io.WriteString(w, copyTestResult)
		})
		c.region = region
		c.now = func() time.Time { return stamp }
		in := copyTestInput()
		in.Key = key
		in.SourceKey = key
		out, err := c.CopyObject(t.Context(), in)
		if err != nil || out == nil || requests.Load() != 1 || connections.Load() != 1 {
			t.Fatalf("raw request: %v", err)
		}
	}
}

type copyBytesErrorReader struct {
	sent bool
	err  error
}

func (r *copyBytesErrorReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, copyTestResult), r.err
}
func TestCopyObjectReadLossAndCaps(t *testing.T) {
	for _, cause := range []error{errors.New("synthetic-secret-key"), context.Canceled, context.DeadlineExceeded} {
		c, _, b := copyTestClient(t, 200, nil, &copyBytesErrorReader{err: cause})
		out, err := copyTestCall(t, c)
		if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || vngcloud.ErrorCode(err) != "RequestFailed" || b.closes != 1 {
			t.Fatal("complete buffered tokens concealed read failure")
		}
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			if !errors.Is(err, cause) {
				t.Fatal("context identity lost")
			}
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
	for _, status := range []int{200, 500} {
		probe := &objectCountReader{remaining: 100_000}
		c, _, b := copyTestClient(t, status, nil, probe)
		out, err := copyTestCall(t, c)
		if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || probe.read > 64*1024+1 || b.closes != 1 {
			t.Fatalf("status=%d read=%d err=%v", status, probe.read, err)
		}
		if status == 500 && probe.read != 64*1024+1 {
			t.Fatal("error body bound changed")
		}
	}
	for _, status := range []int{401, 404, 429, 500} {
		c, _, b := copyTestClient(t, status, nil, objectFailReader{errors.Join(context.Canceled, errors.New("synthetic-secret-key"))})
		out, err := copyTestCall(t, c)
		if out != nil || !errors.Is(err, context.Canceled) || errors.Is(err, ErrObjectCopyUnconfirmed) != (status >= 500) || b.closes != 1 {
			t.Fatalf("status/read precedence: %v", err)
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
}
