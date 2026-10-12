package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"danny.vn/vngcloud"
)

func TestDeleteObjectMetadataSafety(t *testing.T) {
	for _, value := range []string{"TRUE", "False", "1", " true", "false ", "\xff", "t\nrue", "true\x7f", strings.Repeat("v", 1025)} {
		c, tr, body := deleteTestClient(t, 204, http.Header{"X-Amz-Delete-Marker": {value}, "X-Amz-Version-Id": {value}}, nil)
		out, err := deleteTestCall(t, c)
		if err != nil || out == nil || out.DeleteMarker != nil || tr.calls != 1 || body.reads != 0 || body.closes != 1 {
			t.Fatalf("out=%v err=%v", out, err)
		}
		if (len(value) > 1024 || !utf8.ValidString(value) || objectHasControls(value)) && out.VersionID != "" {
			t.Fatal("invalid version retained")
		}
	}
	for _, name := range []string{"X-Amz-Delete-Marker", "X-Amz-Version-Id"} {
		for _, h := range []http.Header{{name: {"true", "true"}}, {name: {"true"}, strings.ToLower(name): {"false"}}, {name: {"true"}, strings.ToLower(name): nil}, {name: nil}} {
			c, _, _ := deleteTestClient(t, 204, h, nil)
			out, err := deleteTestCall(t, c)
			if err != nil || out == nil || out.DeleteMarker != nil || out.VersionID != "" {
				t.Fatalf("duplicate out=%v err=%v", out, err)
			}
		}
	}
	for _, n := range []int{1024, 1025} {
		c, _, _ := deleteTestClient(t, 204, http.Header{"X-Amz-Version-Id": {strings.Repeat("z", n)}}, nil)
		out, err := deleteTestCall(t, c)
		if err != nil || out == nil || (out.VersionID != "") != (n == 1024) {
			t.Fatal("version byte limit changed")
		}
	}
	for _, credential := range []string{"true", "false", "t", "x", "short", `a"b\c`, "synthetic-secret-key"} {
		cfg := objectTestConfig()
		cfg.Credentials.AccessKeyID = vngcloud.Secret(credential)
		cfg.Credentials.SecretAccessKey = vngcloud.Secret(credential)
		c, err := NewObjectClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(credential)
		if err != nil {
			t.Fatal(err)
		}
		for _, version := range []string{credential, "prefix " + credential + " suffix", string(encoded[1 : len(encoded)-1])} {
			for _, marker := range []string{"true", "false"} {
				body := new(deleteTestBody)
				tr := &deleteTestTransport{response: &http.Response{StatusCode: 204, Header: http.Header{"X-Amz-Delete-Marker": {marker}, "X-Amz-Version-Id": {version}}, Body: body}}
				c.httpClient.Transport = tr
				out, err := deleteTestCall(t, c)
				if err != nil || out == nil || out.VersionID != "" || tr.calls != 1 || body.reads != 0 || body.closes != 1 {
					t.Fatalf("reflected metadata out=%v err=%v", out, err)
				}
				if strings.Contains(marker, credential) && out.DeleteMarker != nil {
					t.Fatal("credential parsed as public boolean")
				}
				assertObjectSafe(t, out, credential)
				// These fields are the DELETE metadata envelope consumed by renderers.
				projection := struct {
					DeleteMarker *bool
					VersionID    string
				}{out.DeleteMarker, out.VersionID}
				assertObjectSafe(t, projection, credential)
				for _, value := range []any{out, projection} {
					needle := credential
					if len(credential) == 1 {
						// A pointer's 0x prefix is not a reflected scalar.
						needle = `"` + credential + `"`
					}
					if strings.Contains(fmt.Sprintf("%v", value), needle) {
						t.Fatal("credential escaped ordinary formatting")
					}
				}
				if strings.Contains(out.VersionID, credential) {
					t.Fatal("credential retained in direct version field")
				}
				if out.DeleteMarker != nil {
					for _, format := range []string{"%v", "%+v", "%#v"} {
						if strings.Contains(fmt.Sprintf(format, *out.DeleteMarker), credential) {
							t.Fatal("credential retained in boolean representation")
						}
					}
				}
			}
		}
		c.httpClient.CloseIdleConnections()
	}
}

func TestDeleteObjectNoTransportReplay(t *testing.T) {
	for _, reply := range []string{"", "HTTP/1.1 204 No Content\r\n"} {
		var requests atomic.Int32
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Method != "DELETE" {
				t.Error("auxiliary request")
			}
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, err = buffer.WriteString(reply)
			if err != nil {
				t.Error(err)
				return
			}
			if err := buffer.Flush(); err != nil {
				t.Error(err)
			}
		})
		out, err := deleteTestCall(t, c)
		if out != nil || !errors.Is(err, ErrObjectDeleteUnconfirmed) || requests.Load() != 1 || connections.Load() != 1 {
			t.Fatalf("out=%v err=%v requests=%d connections=%d", out, err, requests.Load(), connections.Load())
		}
	}
	var requests atomic.Int32
	c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if r.Method != "DELETE" {
			t.Error("auxiliary request")
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		if n == 1 {
			_, err = buffer.WriteString("HTTP/1.1 204 No Content\r\nConnection: keep-alive\r\n\r\n")
			if err != nil {
				t.Error(err)
				return
			}
			if err := buffer.Flush(); err != nil {
				t.Error(err)
			}
		}
	})
	base := c.httpClient.Transport.(objectLocalTransport).base.(*http.Transport)
	if !base.DisableKeepAlives || base.ForceAttemptHTTP2 || base.Protocols == nil || !base.Protocols.HTTP1() || base.Protocols.HTTP2() || base.Protocols.UnencryptedHTTP2() {
		t.Fatal("production transport permits connection reuse or HTTP2")
	}
	out, err := deleteTestCall(t, c)
	if err != nil || out == nil {
		t.Fatalf("preceding request: out=%v err=%v", out, err)
	}
	out, err = deleteTestCall(t, c)
	if out != nil || !errors.Is(err, ErrObjectDeleteUnconfirmed) || requests.Load() != 2 || connections.Load() != 2 {
		t.Fatalf("stale keep-alive out=%v err=%v requests=%d connections=%d", out, err, requests.Load(), connections.Load())
	}
}

// The observer records what net/http exposes, not suppressed wire bytes.
type deleteWireObserver struct {
	base   http.RoundTripper
	noBody bool
}

func (tr *deleteWireObserver) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := tr.base.RoundTrip(r)
	if resp != nil {
		tr.noBody = resp.Body == http.NoBody
	}
	return resp, err
}

func TestDeleteObjectMalformedWire(t *testing.T) {
	for _, reply := range []string{
		"HTTP/1.1 204 No Content\r\nContent-Length: invalid\r\n\r\n",
		"HTTP/1.1 204 No Content\r\nBad Header\r\n\r\n",
		"HTTP/1.1 204 No Content\r\nX-Amz-Version-Id: truncated",
		"HTTP/1.1 204 No Content\r\nContent-Length: 99\r\n\r\nsuppressed wire content",
	} {
		var requests atomic.Int32
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, err = buffer.WriteString(reply)
			if err != nil {
				t.Error(err)
				return
			}
			if err := buffer.Flush(); err != nil {
				t.Error(err)
			}
		})
		observer := &deleteWireObserver{base: c.httpClient.Transport}
		c.httpClient.Transport = observer
		out, err := deleteTestCall(t, c)
		if strings.Contains(reply, "suppressed wire content") {
			if err != nil || out == nil || !observer.noBody {
				t.Fatalf("net/http 204 behavior: out=%v err=%v NoBody=%t", out, err, observer.noBody)
			}
		} else if out != nil || !errors.Is(err, ErrObjectDeleteUnconfirmed) {
			t.Fatalf("malformed wire out=%v err=%v", out, err)
		}
		if requests.Load() != 1 || connections.Load() != 1 {
			t.Fatal("malformed response caused replay")
		}
	}
}

func TestDeleteObjectDialAndTLS(t *testing.T) {
	c := objectTestClient(t)
	base := c.httpClient.Transport.(*http.Transport)
	base.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("synthetic-secret-key https://private.invalid")
	}
	out, err := deleteTestCall(t, c)
	if out != nil || err == nil || errors.Is(err, ErrObjectDeleteUnconfirmed) {
		t.Fatalf("dial out=%v err=%v", out, err)
	}
	assertObjectSafe(t, err, "synthetic-secret-key", "https://private.invalid")
	var requests atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	defer s.Close()
	c = objectTestClient(t)
	base = c.httpClient.Transport.(*http.Transport)
	base.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(s.URL, "https://"))
	}
	out, err = deleteTestCall(t, c)
	if out != nil || err == nil || errors.Is(err, ErrObjectDeleteUnconfirmed) || requests.Load() != 0 {
		t.Fatalf("TLS out=%v err=%v requests=%d", out, err, requests.Load())
	}
	assertObjectSafe(t, err, s.URL, "x509", "certificate")
}

func TestDeleteObjectRedirect(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		c, connections := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
		})
		out, err := deleteTestCall(t, c)
		if out != nil || !errors.Is(err, ErrObjectDeleteUnconfirmed) || forwarded.Load() != 0 || connections.Load() != 1 {
			t.Fatalf("redirect out=%v err=%v", out, err)
		}
	}
}

func TestDeleteObjectResponseContext(t *testing.T) {
	// Confirmed headers and definitive refusals remain authoritative even if
	// cancellation races with the response. Error-body context causes stay safe.
	for _, status := range []int{204, 403, 500} {
		ctx, cancel := context.WithCancel(t.Context())
		c, tr, body := deleteTestClient(t, status, nil, objectFailReader{context.Canceled})
		tr.before = func(r *http.Request) {
			httptrace.ContextClientTrace(r.Context()).GotConn(httptrace.GotConnInfo{})
			cancel()
		}
		out, err := c.DeleteObject(ctx, &DeleteObjectInput{Bucket: "bucket", Key: "key"})
		cancel()
		if status == 204 {
			if out == nil || err != nil || body.reads != 0 {
				t.Fatalf("accepted response revoked: out=%v err=%v", out, err)
			}
		} else if out != nil || !errors.Is(err, context.Canceled) || errors.Is(err, ErrObjectDeleteUnconfirmed) != (status == 500) {
			t.Fatalf("response context out=%v err=%v", out, err)
		}
		if body.closes != 1 || tr.calls != 1 {
			t.Fatal("response resource not released")
		}
	}
}

var _ io.ReadCloser = (*deleteTestBody)(nil)
