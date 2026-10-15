package iam_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/internal/cli"
	"danny.vn/vngcloud/internal/transport"
)

type guardErrorCase struct {
	name, token, op string
	group           func(string) any
	page            func(string) any
	typeError       bool
	invalidInput    bool
}

func guardErrorCases() []guardErrorCase {
	group := func(field string, value func(string) any) func(string) any {
		return func(token string) any {
			body := map[string]any{"mode": "iam", "policies": []string{}, "iamUsers": []string{}}
			body[field] = value(token)
			return body
		}
	}
	number := func(token string) any { return json.Number(token) }
	numbers := func(token string) any { return []any{json.Number(token)} }
	return []guardErrorCase{
		{name: "unknown group key", token: "synthetic-bearer-canary", op: "iam.DeleteGroup", group: func(token string) any {
			return map[string]any{token: true, "mode": "iam", "policies": []string{}, "iamUsers": []string{}}
		}},
		{name: "group mode", token: "1234567890123456", op: "iam.DeleteGroup", group: group("mode", number), typeError: true},
		{name: "group policies", token: "1234567890123456", op: "iam.DeleteGroup", group: group("policies", numbers), typeError: true},
		{name: "group users", token: "1234567890123456", op: "iam.DeleteGroup", group: group("iamUsers", numbers), typeError: true},
		{name: "attachment total", token: "1234567890123456", op: "iam.DeleteServiceAccount", page: func(token string) any { return map[string]any{"data": []any{}, "totalItems": json.Number(token)} }},
		{name: "attachment policy ID", token: "synthetic.bearer-canary", op: "iam.GetPolicy", invalidInput: true, page: func(token string) any {
			return map[string]any{"data": []any{map[string]any{"id": token}}, "totalItems": 1}
		}},
	}
}

func guardErrorServer(t *testing.T, tc guardErrorCase) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	writes, reflections := new(atomic.Int32), new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token != tc.token {
			t.Error("request did not carry the synthetic bearer token")
			w.WriteHeader(401)
			return
		}
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(204)
			return
		}
		var body any
		switch r.URL.Path {
		case "/accounts-api/v1/auth/userinfo":
			body = map[string]any{"userId": "caller-1", "userType": "iam-user"}
		case "/policies-api/v1/actions":
			body = []any{map[string]any{"action": "CreatePolicy", "label": "Write"}}
		case "/policies-api/v1/groups/group-1":
			body = tc.group(token)
			reflections.Add(1)
		case "/policies-api/v1/user-attachments/service-accounts/sa-1/policies":
			body = tc.page(token)
			reflections.Add(1)
		default:
			t.Error("unexpected read")
			w.WriteHeader(404)
			return
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error("could not encode synthetic response")
		}
	}))
	t.Cleanup(server.Close)
	return server, writes, reflections
}

func assertGuardErrorSafe(t *testing.T, err error, tc guardErrorCase) {
	t.Helper()
	if err == nil {
		t.Fatal("guard accepted hostile read")
	}
	if !strings.Contains(err.Error(), tc.op) {
		t.Error("operation name was lost")
	}
	var typeError *json.UnmarshalTypeError
	if tc.typeError && !errors.As(err, &typeError) {
		t.Error("UnmarshalTypeError identity was lost")
	}
	if tc.invalidInput && !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Error("ErrInvalidInput identity was lost")
	}
	var visit func(error)
	visit = func(node error) {
		if node == nil {
			return
		}
		if strings.Contains(fmt.Sprintf("%v %+v %#v", node, node, node), tc.token) {
			t.Error("error node leaked the bearer token")
		}
		if api, ok := node.(*vngcloud.APIError); ok { //nolint:errorlint // Inspect each node's own fields instead of a wrapped match.
			if strings.Contains(api.Operation+api.Code+api.Message, tc.token) {
				t.Error("SDK APIError field leaked the bearer token")
			}
		}
		if api, ok := node.(*transport.APIError); ok { //nolint:errorlint // Inspect each node's own fields instead of a wrapped match.
			if strings.Contains(api.Operation+api.Code+api.Message, tc.token) {
				t.Error("transport APIError field leaked the bearer token")
			}
		}
		switch e := node.(type) { //nolint:errorlint // Visit every cause, including causes hidden by a wrapped match.
		case interface{ Unwrap() []error }:
			for _, child := range e.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(e.Unwrap())
		}
	}
	visit(err)
}

func TestGuardStrictPeerErrorSDK(t *testing.T) {
	for _, tc := range guardErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			server, writes, reflections := guardErrorServer(t, tc)
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken(tc.token), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{IAM: server.URL + "/", Dashboard: server.URL + "/"}))
			if err != nil {
				t.Fatal(err)
			}
			c := iam.New(cfg)
			if tc.group != nil {
				_, err = c.DeleteGroup(context.Background(), &iam.DeleteGroupInput{GroupID: "group-1"})
			} else {
				_, err = c.DeleteServiceAccount(context.Background(), &iam.DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
			}
			assertGuardErrorSafe(t, err, tc)
			if writes.Load() != 0 || reflections.Load() != 1 {
				t.Fatalf("writes = %d, reflected responses = %d", writes.Load(), reflections.Load())
			}
		})
	}
}

type guardLoopbackTransport struct {
	base     http.RoundTripper
	endpoint *url.URL
}

func (s guardLoopbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = s.endpoint.Scheme
	clone.URL.Host = s.endpoint.Host
	clone.Host = s.endpoint.Host
	return s.base.RoundTrip(clone)
}

func TestGuardStrictPeerErrorCLI(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"config", "credentials"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VNGCLOUD_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("VNGCLOUD_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	for _, name := range []string{"VNGCLOUD_PROFILE", "VNGCLOUD_CLIENT_ID", "VNGCLOUD_CLIENT_SECRET", "VNGCLOUD_ROOT_EMAIL", "VNGCLOUD_USERNAME", "VNGCLOUD_PASSWORD", "VNGCLOUD_TOTP_SECRET"} {
		t.Setenv(name, "")
	}
	for _, tc := range guardErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VNGCLOUD_ACCESS_TOKEN", tc.token)
			server, writes, reflections := guardErrorServer(t, tc)
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			original := http.DefaultTransport
			http.DefaultTransport = guardLoopbackTransport{base: server.Client().Transport, endpoint: endpoint}
			t.Cleanup(func() { http.DefaultTransport = original })
			args := []string{"--region", "hcm-3", "iam", "delete-group", "--group-id", "group-1", "--yes"}
			if tc.group == nil {
				args = []string{"--region", "hcm-3", "iam", "delete-service-account", "--service-account-id", "sa-1", "--yes"}
			}
			var stderr, stdout bytes.Buffer
			code := cli.Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
			if code == 0 || stdout.Len() != 0 {
				t.Error("CLI accepted hostile read")
			}
			if strings.Contains(stderr.String(), tc.token) {
				t.Error("CLI stderr leaked the bearer token")
			}
			if !strings.Contains(stderr.String(), tc.op) {
				t.Error("CLI operation name was lost")
			}
			if writes.Load() != 0 || reflections.Load() != 1 {
				t.Fatalf("writes = %d, reflected responses = %d", writes.Load(), reflections.Load())
			}
			for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
				if !json.Valid([]byte(line)) {
					t.Error("CLI stderr was not JSON")
				}
			}
		})
	}
}
