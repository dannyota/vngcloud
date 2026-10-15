package iam_test

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

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/internal/cli"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type observedSDKTransport struct {
	base   http.RoundTripper
	cancel bool
}
type observedSDKBody struct {
	io.ReadCloser
	cancel bool
}

func (b observedSDKBody) Read([]byte) (int, error) {
	if b.cancel {
		return 0, fmt.Errorf("synthetic-body-canary: %w", context.Canceled)
	}
	return 0, fmt.Errorf("synthetic-body-canary: %w", io.ErrUnexpectedEOF)
}
func (s observedSDKTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(r)
	if err == nil {
		resp.Body = observedSDKBody{ReadCloser: resp.Body, cancel: s.cancel}
	}
	return resp, err
}

func TestObservedStatusSDK(t *testing.T) {
	for _, tc := range []struct {
		status   int
		sentinel error
	}{{404, vngcloud.ErrNotFound}, {401, vngcloud.ErrAuth}, {403, vngcloud.ErrPermission}, {429, vngcloud.ErrRateLimited}} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/canceled=%t", tc.status, canceled), func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Path != "/accounts-api/v1/auth/userinfo" {
						t.Error("unexpected read path")
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, "synthetic-body-canary")
				}))
				defer server.Close()
				cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-bearer-canary"), vngcloud.WithHTTPClient(&http.Client{Transport: observedSDKTransport{base: server.Client().Transport, cancel: canceled}}), vngcloud.WithRetry(3, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{IAM: server.URL + "/", Dashboard: server.URL + "/"}))
				if err != nil {
					t.Fatal(err)
				}
				// Exercise the opted-in shared SDK path without changing a shipped read.
				_, err = core.ClientOf(cfg).DoJSONStatus(context.Background(), transport.Request{Operation: "iam.GetCallerIdentity", URL: server.URL + "/accounts-api/v1/auth/userinfo", OK: []int{200}, KeepObservedStatus: true}, nil)
				cause := io.ErrUnexpectedEOF
				if canceled {
					cause = context.Canceled
				}
				var api *vngcloud.APIError
				if !errors.Is(err, tc.sentinel) || !errors.Is(err, cause) || !errors.As(err, &api) {
					t.Fatalf("error identities lost: %v", err)
				}
				if api.StatusCode != tc.status || api.Retryable || calls != 1 {
					t.Errorf("status=%d retryable=%t calls=%d", api.StatusCode, api.Retryable, calls)
				}
				if strings.Contains(err.Error(), "canary") {
					t.Error("error leaked body or credential")
				}
				_, err = iam.New(cfg).GetCallerIdentity(context.Background(), &iam.GetCallerIdentityInput{})
				if !errors.As(err, &api) || api.StatusCode != 0 || errors.Is(err, tc.sentinel) || !errors.Is(err, cause) || calls != 2 {
					t.Fatalf("unset read changed: error=%v calls=%d", err, calls)
				}
			})
		}
	}
}

func TestObservedStatusCLI(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"config", "credentials"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VNGCLOUD_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("VNGCLOUD_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	for _, name := range []string{"VNGCLOUD_PROFILE", "VNGCLOUD_CLIENT_ID", "VNGCLOUD_CLIENT_SECRET", "VNGCLOUD_ROOT_EMAIL", "VNGCLOUD_USERNAME", "VNGCLOUD_PASSWORD", "VNGCLOUD_TOTP_SECRET"} {
		t.Setenv(name, "")
	}
	t.Setenv("VNGCLOUD_ACCESS_TOKEN", "synthetic-bearer-canary")
	for _, status := range []int{401, 403, 404, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "synthetic-body-canary")
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			original := http.DefaultTransport
			http.DefaultTransport = observedSDKTransport{base: guardLoopbackTransport{base: server.Client().Transport, endpoint: endpoint}, cancel: true}
			defer func() { http.DefaultTransport = original }()
			var stdout, stderr bytes.Buffer
			code := cli.Main(context.Background(), []string{"--region", "hcm-3", "iam", "get-caller-identity"}, strings.NewReader(""), &stdout, &stderr)
			var envelope struct {
				Error struct {
					Status int `json:"status"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			// Cancellation keeps exit 1 ahead of the auth and not-found exit codes.
			if code != 1 || stdout.Len() != 0 || envelope.Error.Status != 0 || strings.Contains(stderr.String(), "canary") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestObservedEmptyGuardReads(t *testing.T) {
	deleteSA := func(c *iam.Client) error {
		_, err := c.DeleteServiceAccount(context.Background(), &iam.DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
		return err
	}
	attachUser := func(c *iam.Client) error {
		_, err := c.AttachUserPolicy(context.Background(), &iam.AttachUserPolicyInput{UserID: "user-2", PolicyID: "policy-1"})
		return err
	}
	deletePolicy := func(c *iam.Client) error {
		_, err := c.DeletePolicy(context.Background(), &iam.DeletePolicyInput{PolicyID: "policy-1"})
		return err
	}
	deleteGroup := func(c *iam.Client) error {
		_, err := c.DeleteGroup(context.Background(), &iam.DeleteGroupInput{GroupID: "group-1"})
		return err
	}
	for _, tc := range []struct {
		path string
		call func(*iam.Client) error
	}{
		{"/accounts-api/v1/auth/userinfo", deleteSA},
		{"/policies-api/v1/actions", deleteSA},
		{"/policies-api/v1/policies/policy-1", deleteSA},
		{"/policies-api/v1/user-attachments/service-accounts/sa-1/policies", deleteSA},
		{"/policies-api/v1/user-attachments/iam-users/user-2/policies", attachUser},
		{"/policies-api/v1/user-attachments/iam-users/user-2/groups", attachUser},
		{"/policies-api/v1/groups/group-1", deleteGroup},
		{"/policies-api/v1/policies/policy-1/groups", deletePolicy},
		{"/policies-api/v1/policies/policy-1/iam-users", deletePolicy},
		{"/policies-api/v1/policies/policy-1/service-accounts", deletePolicy},
	} {
		t.Run(tc.path, func(t *testing.T) {
			bodies := map[string]string{
				"/accounts-api/v1/auth/userinfo":                                   `{"userId":"caller-1","userType":"iam-user"}`,
				"/policies-api/v1/actions":                                         `[{"action":"CreatePolicy","label":"Write"}]`,
				"/policies-api/v1/policies/policy-1":                               `{"id":"policy-1","manager":"user","statements":[{"effect":"allow","actions":["vserver:List*"]}]}`,
				"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": `{"data":[{"id":"policy-1"}],"totalItems":1}`,
				"/policies-api/v1/user-attachments/iam-users/user-2/policies":      `{"data":[],"totalItems":0}`,
				"/policies-api/v1/user-attachments/iam-users/user-2/groups":        `[]`,
				"/policies-api/v1/groups/group-1":                                  `{"id":"group-1","mode":"iam","iamUsers":[],"policies":[]}`,
				"/policies-api/v1/policies/policy-1/groups":                        `[]`,
				"/policies-api/v1/policies/policy-1/iam-users":                     `[]`,
				"/policies-api/v1/policies/policy-1/service-accounts":              `[]`,
			}
			writes, emptyReads := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(204)
					return
				}
				body, ok := bodies[r.URL.Path]
				if !ok {
					t.Error("unexpected guard path")
					w.WriteHeader(404)
					return
				}
				if r.URL.Path == tc.path {
					emptyReads++
					body = ""
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-bearer-canary"), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{IAM: server.URL + "/", Dashboard: server.URL + "/"}))
			if err != nil {
				t.Fatal(err)
			}
			err = tc.call(iam.New(cfg))
			var api *vngcloud.APIError
			if !errors.As(err, &api) || writes != 0 || emptyReads != 1 {
				t.Fatalf("error=%v writes=%d empty reads=%d", err, writes, emptyReads)
			}
		})
	}
}
