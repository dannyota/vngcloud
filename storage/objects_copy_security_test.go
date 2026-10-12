package storage

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func copyTestXML(value string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(value)); err != nil {
		panic(err)
	}
	return b.String()
}

func TestCopyObjectMetadataSafety(t *testing.T) {
	for _, value := range []string{"safe", strings.Repeat("z", 1024), strings.Repeat("z", 1025), "synthetic-secret-key", "bad\nvalue", "\xff"} {
		// Invalid UTF-8 is not valid XML; use it only in injected headers.
		etag := value
		if value == "\xff" {
			etag = "safe"
		}
		c, tr, b := copyTestClient(t, 200, http.Header{"X-Amz-Version-Id": {value}, "X-Amz-Copy-Source-Version-Id": {value}, "X-Amz-Server-Side-Encryption": {value}, "X-Amz-Meta-Private": {"synthetic-secret-key"}}, strings.NewReader(`<CopyObjectResult><ETag>`+copyTestXML(etag)+`</ETag><LastModified>bad</LastModified></CopyObjectResult>`))
		out, err := copyTestCall(t, c)
		if err != nil || out == nil || tr.calls != 1 || b.closes != 1 || out.LastModified != nil {
			t.Fatalf("metadata: %v", err)
		}
		if out.ETag != c.filterObjectMetadata(etag, 1024) || out.VersionID != c.filterObjectMetadata(value, 1024) || out.SourceVersionID != out.VersionID || out.ServerSideEncryption != c.filterObjectMetadata(value, 128) {
			t.Fatal("metadata filter bypass")
		}
	}
	for _, name := range []string{"X-Amz-Version-Id", "X-Amz-Copy-Source-Version-Id", "X-Amz-Server-Side-Encryption"} {
		for _, h := range []http.Header{{name: {"safe", "safe"}}, {name: {"safe"}, strings.ToLower(name): {"safe"}}, {name: {"safe"}, strings.ToLower(name): nil}} {
			c, _, _ := copyTestClient(t, 200, h, strings.NewReader(copyTestResult))
			out, err := copyTestCall(t, c)
			if err != nil || out == nil || out.VersionID != "" || out.SourceVersionID != "" || out.ServerSideEncryption != "" {
				t.Fatal("duplicate header retained")
			}
		}
	}
	for _, n := range []int{128, 129} {
		c, _, _ := copyTestClient(t, 200, http.Header{"X-Amz-Server-Side-Encryption": {strings.Repeat("z", n)}}, strings.NewReader(copyTestResult))
		out, err := copyTestCall(t, c)
		if err != nil || (out.ServerSideEncryption != "") != (n == 128) {
			t.Fatal("encryption header cap changed")
		}
	}
	for _, date := range []string{"", "bad", strings.Repeat("x", 129), "2024-01-02T03:04:05+07:00"} {
		c, _, _ := copyTestClient(t, 200, nil, strings.NewReader(`<CopyObjectResult><ETag>e</ETag><LastModified>`+date+`</LastModified></CopyObjectResult>`))
		out, err := copyTestCall(t, c)
		if err != nil || out == nil || (out.LastModified != nil) != (date == "2024-01-02T03:04:05+07:00") {
			t.Fatal("optional time validity changed")
		}
		if out.LastModified != nil && (out.LastModified.Location() != time.UTC || out.LastModified.Format(time.RFC3339) != "2024-01-01T20:04:05Z") {
			t.Fatal("safe time is not UTC")
		}
	}
}

func TestCopyObjectCredentialReflections(t *testing.T) {
	for _, credential := range []string{"synthetic-secret-key", "x", `a"b\c`, "secret<&quote", "NotImplemented"} {
		cfg := objectTestConfig()
		cfg.Credentials.AccessKeyID = vngcloud.Secret(credential)
		cfg.Credentials.SecretAccessKey = vngcloud.Secret(credential)
		encoded, err := json.Marshal(credential)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{credential, "prefix " + credential + " suffix", string(encoded[1 : len(encoded)-1])} {
			c, err := NewObjectClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(c.httpClient.CloseIdleConnections)
			c.httpClient.Transport = &copyTestTransport{response: &http.Response{StatusCode: 200, Header: http.Header{"X-Amz-Version-Id": {value}, "X-Amz-Copy-Source-Version-Id": {value}, "X-Amz-Server-Side-Encryption": {value}}, Body: io.NopCloser(strings.NewReader(`<CopyObjectResult><ETag>` + copyTestXML(value) + `</ETag></CopyObjectResult>`))}}
			out, err := copyTestCall(t, c)
			if err != nil || out == nil || out.ETag != "" || out.VersionID != "" || out.SourceVersionID != "" || out.ServerSideEncryption != "" {
				t.Fatal("credential metadata escaped")
			}
			assertObjectSafe(t, out, credential)
			// Direct fields have no formatting wrapper that could conceal a leak.
			for _, field := range []string{out.ETag, out.VersionID, out.SourceVersionID, out.ServerSideEncryption} {
				if field != "" {
					t.Fatal("direct metadata retained")
				}
			}
			for _, status := range []int{200, 403, 501} {
				c.httpClient.Transport = &copyTestTransport{response: &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`<Error><Code>` + copyTestXML(value) + `</Code><Message>` + copyTestXML(value) + `</Message></Error>`))}}
				out, err := copyTestCall(t, c)
				if out != nil || err == nil || strings.Contains(vngcloud.ErrorCode(err), credential) {
					t.Fatal("reflected error code retained")
				}
				assertObjectSafe(t, err, value, credential)
			}
		}
	}
}

func TestCopyObjectNormalizedTimestampSafety(t *testing.T) {
	const source = "2024-01-02T03:04:05.123456789+07:00"
	parsed, err := time.Parse(time.RFC3339, source)
	if err != nil {
		t.Fatal(err)
	}
	parsed = parsed.UTC()
	text, err := parsed.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	data, err := parsed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{parsed.Format(time.RFC3339Nano), string(text), string(data), parsed.String(), parsed.GoString(), "20:04:05", "time.January", "UTC"} {
		if strings.Contains(source, credential) {
			t.Fatal("test must isolate normalization")
		}
		c, _, _ := copyTestClient(t, 200, nil, strings.NewReader(`<CopyObjectResult><ETag>e</ETag><LastModified>`+source+`</LastModified></CopyObjectResult>`))
		c.credentials.AccessKeyID = vngcloud.Secret(credential)
		c.credentials.SecretAccessKey = vngcloud.Secret(credential)
		out, err := copyTestCall(t, c)
		if err != nil || out == nil || out.LastModified != nil {
			t.Fatal("normalized credential retained or confirmed copy revoked")
		}
		assertObjectSafe(t, out, credential)
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, out), credential) {
				t.Fatal("time leaks through formatting")
			}
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"LastModified":null`) {
			t.Fatal("unsafe time not null")
		}
	}
}

func TestCopyObjectEmbeddedErrorSafety(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`<Error><Code>NotImplemented</Code><Message>synthetic-secret-key</Message></Error>`, "NotImplemented"},
		{`<Error><Code>InternalError</Code></Error>`, "InternalError"},
		{`<Error><Code>synthetic-secret-key</Code></Error>`, "S3Error"},
		{`<Error><Code>InternalError</Code>`, "InvalidResponse"},
		{`<Error><Code>InternalError</Code><Code>InternalError</Code></Error>`, "InvalidResponse"},
		{`<Error><Code><Unknown>InternalError</Unknown></Code></Error>`, "InvalidResponse"},
		{`<Error><x:Code xmlns:x="urn:evil">InternalError</x:Code></Error>`, "InvalidResponse"},
		{`<Error xmlns="urn:evil"><Code>InternalError</Code></Error>`, "InvalidResponse"},
		{`<Error><Code>InternalError</Code></Error>junk`, "InvalidResponse"},
		{`<Error><Code>InternalError</Code></Error><?other x?>`, "InvalidResponse"},
		{`<Error><Code>InternalError</Code><Unknown>` + strings.Repeat("x", 64*1024) + `</Unknown></Error>`, "InvalidResponse"},
	} {
		c, _, b := copyTestClient(t, 200, nil, strings.NewReader(tc.body))
		out, err := copyTestCall(t, c)
		var api *vngcloud.APIError
		if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || !errors.As(err, &api) || api.Code != tc.code || api.Operation != "storage.CopyObject" || api.StatusCode != 200 || api.Retryable || b.closes != 1 {
			t.Fatalf("embedded error: %v", err)
		}
		assertObjectSafe(t, err, "synthetic-secret-key", "urn:evil")
	}
}

func TestCopyObjectNoReplay(t *testing.T) {
	for _, reply := range []string{"", "HTTP/1.1 200 OK\r\n", "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n<CopyObjectResult>"} {
		var requests atomic.Int32
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Method != "PUT" {
				t.Error("secondary operation")
			}
			conn, b, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			if _, err := b.WriteString(reply); err != nil {
				t.Error(err)
				return
			}
			if err := b.Flush(); err != nil {
				t.Error(err)
			}
		})
		out, err := copyTestCall(t, c)
		if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || requests.Load() != 1 || connections.Load() != 1 {
			t.Fatalf("disconnect: %v requests=%d connections=%d", err, requests.Load(), connections.Load())
		}
	}
	var requests atomic.Int32
	c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if r.Method != "PUT" {
			t.Error("secondary operation")
		}
		conn, b, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		if n == 1 {
			if _, err := fmt.Fprintf(b, "HTTP/1.1 200 OK\r\nConnection: keep-alive\r\nContent-Length: %d\r\n\r\n%s", len(copyTestResult), copyTestResult); err != nil {
				t.Error(err)
				return
			}
			if err := b.Flush(); err != nil {
				t.Error(err)
			}
		}
	})
	base := c.httpClient.Transport.(objectLocalTransport).base.(*http.Transport)
	if !base.DisableKeepAlives || base.Proxy != nil || base.ForceAttemptHTTP2 || base.Protocols == nil || !base.Protocols.HTTP1() || base.Protocols.HTTP2() || base.Protocols.UnencryptedHTTP2() {
		t.Fatal("unsafe mutation transport")
	}
	out, err := copyTestCall(t, c)
	if err != nil || out == nil {
		t.Fatalf("first request: %v", err)
	}
	out, err = copyTestCall(t, c)
	if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || requests.Load() != 2 || connections.Load() != 2 {
		t.Fatalf("stale keepalive: %v requests=%d connections=%d", err, requests.Load(), connections.Load())
	}
	c = objectTestClient(t)
	var dials atomic.Int32
	c.httpClient.Transport.(*http.Transport).DialContext = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("synthetic-secret-key")
	}
	out, err = copyTestCall(t, c)
	if out != nil || errors.Is(err, ErrObjectCopyUnconfirmed) || vngcloud.ErrorCode(err) != "RequestFailed" || dials.Load() != 1 {
		t.Fatalf("dial failure: %v", err)
	}
	assertObjectSafe(t, err, "synthetic-secret-key")
	for _, status := range []int{429, 501, 503, 307} {
		var calls atomic.Int32
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Method != "PUT" {
				t.Error("secondary operation")
			}
			w.Header().Set("Location", "https://private.invalid")
			w.WriteHeader(status)
		})
		out, err := copyTestCall(t, c)
		if out != nil || err == nil || calls.Load() != 1 || connections.Load() != 1 {
			t.Fatalf("status=%d calls=%d err=%v", status, calls.Load(), err)
		}
	}
}

func TestCopyObjectCurrentNameRaces(t *testing.T) {
	var calls atomic.Int32
	source, destination := "old-source", "old-destination"
	done := make(chan struct{})
	c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "PUT" || r.RequestURI != "/destination/key" || r.Header.Get("X-Amz-Copy-Source") != "/source/key" || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" {
			t.Error("conditional or auxiliary request")
		}
		source = "new-source"
		destination = "concurrent-destination"
		destination = source
		close(done)
		_, _ = io.WriteString(w, copyTestResult)
	})
	out, err := copyTestCall(t, c)
	if err != nil || out == nil || calls.Load() != 1 || connections.Load() != 1 {
		t.Fatalf("race model: %v", err)
	}
	<-done
	// Server callback writes precede the result; names remain outside SDK control.
	if source != "new-source" || destination != "new-source" {
		t.Fatal("client cleanup changed current names")
	}
}

func TestCopyObjectRawDeadline(t *testing.T) {
	for _, caller := range []bool{false, true} {
		var requests atomic.Int32
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, copyTestResult)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		})
		c.timeout = 50 * time.Millisecond
		ctx := t.Context()
		cancel := func() {}
		if caller {
			c.timeout = time.Second
			ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
		}
		out, err := c.CopyObject(ctx, copyTestInput())
		cancel()
		if out != nil || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrObjectCopyUnconfirmed) || requests.Load() != 1 || connections.Load() != 1 {
			t.Fatalf("raw deadline: %v", err)
		}
	}
	var sink atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { sink.Add(1) }))
	defer foreign.Close()
	c, connections := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", foreign.URL)
		w.WriteHeader(307)
	})
	out, err := copyTestCall(t, c)
	if out != nil || !errors.Is(err, ErrObjectCopyUnconfirmed) || sink.Load() != 0 || connections.Load() != 1 {
		t.Fatal("redirect followed")
	}
}
