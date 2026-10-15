package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/cli"
	"danny.vn/vngcloud/internal/transport"
)

const strictCanary = "synthetic-strict-private-canary"

func assertStrictErrorSafe(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected decode failure")
	}
	var api *transport.APIError
	if !errors.As(err, &api) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if api.Operation != "strict-test" || api.StatusCode != 0 || api.Code != "" || api.Retryable {
		t.Fatalf("changed decode error fields: %+v", api)
	}
	var visit func(error)
	visit = func(node error) {
		if node == nil {
			return
		}
		if strings.Contains(fmt.Sprintf("%+v %#v", node, node), strictCanary) {
			t.Fatal("error node leaked body")
		}
		switch e := node.(type) { //nolint:errorlint // Inspect each node directly, including causes hidden by wrapping.
		case interface{ Unwrap() []error }:
			for _, child := range e.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(e.Unwrap())
		}
	}
	visit(err)
	if strings.Contains(api.Message, strictCanary) || strings.Contains(api.Code, strictCanary) {
		t.Fatal("APIError fields leaked body")
	}
}

func TestStrictJSONSanitizedFailures(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		t.Run(strconv.FormatBool(sensitive), func(t *testing.T) {
			var reference string
			for _, body := range []string{
				`{"name":"` + strictCanary + `"}`,
				`{"name":1,"name":2,"` + strictCanary + `":true}`,
				`{"name":1,"Name":2,"value":"` + strictCanary + `"}`,
				`{"nested":{"` + strictCanary + `":1,"` + strictCanary + `":2}}`,
				`{"name":"` + strictCanary + `"`,
				`{"name":1} "` + strictCanary + `"`,
				"",
			} {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
				c := transport.New(transport.Config{HTTPClient: server.Client()})
				var out struct {
					Name int `json:"name"`
				}
				status, err := c.DoJSONStatus(context.Background(), transport.Request{Operation: "strict-test", URL: server.URL, SkipAuth: true, StrictJSON: true, Sensitive: sensitive}, &out)
				server.Close()
				assertStrictErrorSafe(t, err)
				if status != 200 {
					t.Fatalf("status = %d", status)
				}
				if reference == "" {
					reference = err.Error()
				}
				if err.Error() != reference {
					t.Fatalf("strict error %q differs from decode error %q", err, reference)
				}
				if sensitive {
					var api *transport.APIError
					if !errors.As(err, &api) || api.Err != nil || api.Message != "response failed to decode; body withheld" {
						t.Fatalf("sensitive error = %+v", api)
					}
				}
			}
		})
	}
}

func TestStrictJSONUnset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"name":1,"Name":2}`) }))
	defer server.Close()
	var out struct {
		Name int `json:"name"`
	}
	c := transport.New(transport.Config{HTTPClient: server.Client()})
	if err := c.DoJSON(context.Background(), transport.Request{URL: server.URL, SkipAuth: true}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != 2 {
		t.Fatalf("Name = %d, want legacy last value", out.Name)
	}
}

func TestStrictJSONSyntaxIdentity(t *testing.T) {
	body := `{"name":`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
	defer server.Close()
	c := transport.New(transport.Config{HTTPClient: server.Client()})
	var out any
	err := c.DoJSON(context.Background(), transport.Request{Operation: "strict-test", URL: server.URL, SkipAuth: true, StrictJSON: true}, &out)
	assertStrictErrorSafe(t, err)
	var original, actual *json.SyntaxError
	if !errors.As(json.Unmarshal([]byte(body), &out), &original) || !errors.As(err, &actual) || original.Offset != actual.Offset {
		t.Fatal("syntax error identity or offset lost")
	}
}

// strictCLITransport routes the CLI's default endpoint exclusively to the
// loopback server. No request can dial its original hostname.
type strictCLITransport struct {
	base     http.RoundTripper
	endpoint *url.URL
}

func (s strictCLITransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = s.endpoint.Scheme
	clone.URL.Host = s.endpoint.Host
	clone.Host = s.endpoint.Host
	return s.base.RoundTrip(clone)
}

func TestStrictJSONCLIStderr(t *testing.T) {
	// Empty profile paths and a synthetic token prevent ambient credential reads.
	dir := t.TempDir()
	for _, name := range []string{"config", "credentials"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VNGCLOUD_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("VNGCLOUD_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("VNGCLOUD_ACCESS_TOKEN", "synthetic-strict-token")
	t.Setenv("VNGCLOUD_PROFILE", "")
	t.Setenv("VNGCLOUD_CLIENT_ID", "")
	t.Setenv("VNGCLOUD_CLIENT_SECRET", "")
	t.Setenv("VNGCLOUD_ROOT_EMAIL", "")
	t.Setenv("VNGCLOUD_USERNAME", "")
	t.Setenv("VNGCLOUD_PASSWORD", "")
	t.Setenv("VNGCLOUD_TOTP_SECRET", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts-api/v1/auth/userinfo" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, `{"userId":"`+strictCanary+`","userId":"caller","userType":"iam-user"}`)
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = strictCLITransport{base: server.Client().Transport, endpoint: endpoint}
	t.Cleanup(func() { http.DefaultTransport = original })
	var stdout, stderr bytes.Buffer
	code := cli.Main(context.Background(), []string{"--region", "hcm-3", "iam", "get-caller-identity"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), strictCanary) {
		t.Fatal("CLI stderr leaked response body")
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Message != "iam.GetCallerIdentity: request failed: response failed to decode" {
		t.Fatalf("CLI message = %q", envelope.Error.Message)
	}
}
