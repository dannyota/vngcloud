package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func objectTestConfig() ObjectConfig {
	return ObjectConfig{Region: "HCM04", Credentials: ObjectCredentials{
		AccessKeyID: vngcloud.Secret("synthetic-access-id"), SecretAccessKey: vngcloud.Secret("synthetic-secret-key"),
	}}
}

func objectTestClient(t *testing.T) *ObjectClient {
	t.Helper()
	c, err := NewObjectClient(objectTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.httpClient.CloseIdleConnections)
	return c
}

func TestNewObjectClientValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ObjectConfig)
		want   error
	}{
		{"absent", func(c *ObjectConfig) { c.Credentials = ObjectCredentials{} }, vngcloud.ErrNoCredentials},
		{"missing access", func(c *ObjectConfig) { c.Credentials.AccessKeyID = "" }, vngcloud.ErrNoCredentials},
		{"missing secret", func(c *ObjectConfig) { c.Credentials.SecretAccessKey = "" }, vngcloud.ErrNoCredentials},
		{"control access", func(c *ObjectConfig) { c.Credentials.AccessKeyID = "bad\nkey" }, vngcloud.ErrInvalidConfig},
		{"control secret", func(c *ObjectConfig) { c.Credentials.SecretAccessKey = "bad\x7fkey" }, vngcloud.ErrInvalidConfig},
		{"empty region", func(c *ObjectConfig) { c.Region = "" }, vngcloud.ErrInvalidConfig},
		{"folded region", func(c *ObjectConfig) { c.Region = "hcm04" }, vngcloud.ErrInvalidConfig},
		{"unknown region", func(c *ObjectConfig) { c.Region = "us-east-1" }, vngcloud.ErrInvalidConfig},
		{"negative timeout", func(c *ObjectConfig) { c.Timeout = -1 }, vngcloud.ErrInvalidConfig},
		{"excess timeout", func(c *ObjectConfig) { c.Timeout = time.Hour + 1 }, vngcloud.ErrInvalidConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := objectTestConfig()
			tc.change(&cfg)
			c, err := NewObjectClient(cfg)
			var api *vngcloud.APIError
			if c != nil || !errors.Is(err, tc.want) || errors.As(err, &api) {
				t.Fatalf("client=%v error=%v", c, err)
			}
			assertObjectSafe(t, err, "bad\nkey", "bad\x7fkey", "us-east-1")
		})
	}
	for _, region := range []string{"HCM04", "HAN02"} {
		for _, timeout := range []time.Duration{0, time.Nanosecond, time.Hour} {
			cfg := objectTestConfig()
			cfg.Region = region
			cfg.Timeout = timeout
			c, err := NewObjectClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			want := timeout
			if want == 0 {
				want = 5 * time.Minute
			}
			if c.timeout != want {
				t.Fatalf("timeout=%v", c.timeout)
			}
			if c.now == nil {
				t.Fatal("missing clock")
			}
			c.httpClient.CloseIdleConnections()
		}
	}
	if MaxPutObjectSize != 5_000_000_000 || DefaultMaxDownloadBytes != 5_000_000_000 {
		t.Fatal("incorrect byte caps")
	}
}

func TestObjectClientTransportBoundary(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { proxyCalls.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	c := objectTestClient(t)
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport")
	}
	if tr.Proxy != nil || !tr.DisableCompression || !tr.DisableKeepAlives || tr.ForceAttemptHTTP2 ||
		tr.MaxResponseHeaderBytes != 32*1024 || tr.TLSHandshakeTimeout != 10*time.Second ||
		tr.ResponseHeaderTimeout != 30*time.Second || tr.DialContext == nil || c.httpClient.Jar != nil {
		t.Fatal("unsafe transport settings")
	}
	if tr.TLSClientConfig != nil && (tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.RootCAs != nil) {
		t.Fatal("custom TLS trust")
	}
	if tr.Protocols == nil || !tr.Protocols.HTTP1() || tr.Protocols.HTTP2() || tr.Protocols.UnencryptedHTTP2() {
		t.Fatal("not HTTP/1 only")
	}
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { targetCalls.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, redirect.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "synthetic-auth")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTemporaryRedirect || targetCalls.Load() != 0 || proxyCalls.Load() != 0 {
		t.Fatal("redirect or proxy contacted")
	}
	tlsServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsServer.StartTLS()
	defer tlsServer.Close()
	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, tlsServer.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.httpClient.Do(req)
	if err == nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		t.Fatal("accepted untrusted certificate")
	}
	safe := objectAPIError("storage.GetObject", 0, "RequestFailed", err)
	assertObjectSafe(t, safe, tlsServer.URL, "synthetic-auth")
	for _, region := range []string{"HCM04", "HAN02"} {
		cfg := objectTestConfig()
		cfg.Region = region
		client, err := NewObjectClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		u, err := client.objectURL("bucket", "key")
		if err != nil {
			t.Fatal(err)
		}
		if u.String() != "https://"+strings.ToLower(region)+".vstorage.vngcloud.vn/bucket/key" {
			t.Fatalf("URL=%s", u)
		}
		client.httpClient.CloseIdleConnections()
	}
}

func TestObjectURLBytes(t *testing.T) {
	c := objectTestClient(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.RequestURI)) }))
	defer server.Close()
	for _, tc := range []struct{ key, path string }{
		{"é/猫", "/bucket/%C3%A9/%E7%8C%AB"}, {"%2F", "/bucket/%252F"}, {"a+b c?#", "/bucket/a%2Bb%20c%3F%23"},
		{"\x00\n\x7f", "/bucket/%00%0A%7F"}, {"/a//.././b/", "/bucket//a//.././b/"},
		{strings.Repeat("a", 1024), "/bucket/" + strings.Repeat("a", 1024)},
	} {
		u, err := c.objectURL("bucket", tc.key)
		if err != nil {
			t.Fatal(err)
		}
		if u.EscapedPath() != tc.path || u.RawQuery != "" || u.Fragment != "" {
			t.Fatalf("escaped path=%s want=%s", u.EscapedPath(), tc.path)
		}
		local := strings.TrimPrefix(server.URL, "http://")
		u.Scheme = "http"
		u.Host = local
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		_, readErr := got.ReadFrom(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil || closeErr != nil || got.String() != tc.path {
			t.Fatalf("RequestURI=%q read=%v close=%v", got.String(), readErr, closeErr)
		}
	}
	for _, tc := range []struct{ bucket, key string }{{"", "key"}, {"../bucket", "key"}, {"b/key", "key"}, {"bucket", ""}, {"bucket", "\xff"}, {"bucket", strings.Repeat("é", 513)}} {
		u, err := c.objectURL(tc.bucket, tc.key)
		if u != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("URL=%v error=%v", u, err)
		}
	}
}

func assertObjectSafe(t *testing.T, value any, rejected ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).InfoContext(t.Context(), "test", "value", value)
	for _, output := range []string{fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), string(encoded), log.String()} {
		for _, secret := range rejected {
			needle := secret
			if len(secret) == 1 {
				needle = `"` + secret + `"`
			}
			if secret != "" && strings.Contains(output, needle) {
				t.Fatal("rejected value escaped")
			}
		}
	}
}

func TestObjectBucketURL(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		c := objectTestClient(t)
		c.region = region
		for _, bucket := range []string{"bucket", "a-b", "a.b", "BAD", strings.Repeat("a", 255)} {
			u, err := c.objectBucketURL(bucket)
			want := "https://" + strings.ToLower(region) + ".vstorage.vngcloud.vn/" + bucket
			if err != nil || u == nil || u.String() != want || u.RawQuery != "" || u.Fragment != "" {
				t.Fatalf("URL=%v error=%v", u, err)
			}
		}
		for _, bucket := range []string{"", "../bucket", "b/key", "bucket?query", "bucket#fragment", "bucket%2F", "猫", strings.Repeat("a", 256)} {
			u, err := c.objectBucketURL(bucket)
			if u != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("URL=%v error=%v", u, err)
			}
			assertObjectSafe(t, err, bucket)
		}
	}
	var c *ObjectClient
	if u, err := c.objectBucketURL("bucket"); u != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("URL=%v error=%v", u, err)
	}
}
