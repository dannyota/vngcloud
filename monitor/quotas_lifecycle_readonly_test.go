package monitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func lifecycleProfileConfig(t *testing.T, server *httptest.Server, value string) vngcloud.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("[default]\nregion = hcm-3\nread_only = "+value+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(credentialsPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := vngcloud.LoadConfig(context.Background(),
		vngcloud.WithProfile("default"),
		vngcloud.WithConfigFile(path),
		vngcloud.WithSharedCredentialsFile(credentialsPath),
		vngcloud.WithStaticToken("synthetic-token"),
		vngcloud.WithHTTPClient(server.Client()),
		vngcloud.WithRetry(3, time.Millisecond),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func assertLifecycleProfileError(t *testing.T, err error, invalid bool, op string) {
	t.Helper()
	if invalid {
		if !errors.Is(err, vngcloud.ErrInvalidConfig) || !strings.Contains(err.Error(), "read_only") {
			t.Errorf("err=%v, want invalid read_only configuration", err)
		}
		return
	}
	var api *core.APIError
	if !errors.Is(err, core.ErrInvalidInput) || !errors.As(err, &api) || api.Code != "ReadOnly" || api.Operation != op {
		t.Errorf("err=%v, want ReadOnly for %s wrapping ErrInvalidInput", err, op)
	}
}

func TestLifecycleProfileReadOnlyValues(t *testing.T) {
	methods := []struct {
		op, kind string
		delete   bool
	}{
		{"DeleteNotificationQuota", "sms", true},
		{"SetNotificationQuotaAutoRenew", "sms", false},
		{"SetSyntheticQuotaAutoRenew", "synthetic", false},
	}
	values := []struct {
		value       string
		on, invalid bool
	}{
		{value: "tRuE", on: true},
		{value: " 1 ", on: true},
		{value: "TRUE", on: true},
		{value: "yes", invalid: true},
		{value: "on", invalid: true},
		{value: "False"},
		{value: ""},
		{value: "0"},
	}
	for _, method := range methods {
		for _, value := range values {
			t.Run(method.op+"/"+value.value, func(t *testing.T) {
				s := &lifecycleServer{kind: method.kind, delete: method.delete, quota: func(n int32) string {
					changes := map[string]any{}
					if method.delete {
						if n > 2 {
							return ""
						}
						changes["autoRenew"] = 0
					} else if n > 2 {
						changes["autoRenew"] = 0
						changes["autoRenewPeriod"] = 0
					}
					return lifecycleFixture(t, method.kind, changes)
				}, send: func(w http.ResponseWriter, _ *http.Request) {
					switch {
					case method.kind == "synthetic":
						w.WriteHeader(http.StatusBadRequest)
						testutil.WriteFixture(t, w, "../testdata/monitor/SetQuotaAutoRenewSyntheticRefused.json")
					case method.delete:
						w.WriteHeader(http.StatusNoContent)
					default:
						w.WriteHeader(http.StatusOK)
					}
				}}
				var requests atomic.Int32
				handler := s.handler(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					handler(w, r)
				}))
				defer server.Close()
				c := New(lifecycleProfileConfig(t, server, value.value))
				emailFakeClock(c)
				out, err := lifecycleCall(c, method.kind, method.delete)
				if value.on || value.invalid {
					assertLifecycleProfileError(t, err, value.invalid, "monitor."+method.op)
					if requests.Load() != 0 {
						t.Errorf("requests=%d, want zero", requests.Load())
					}
					return
				}
				if method.kind == "synthetic" {
					var api *core.APIError
					if !errors.As(err, &api) || api.Code != "BadRequest" || api.StatusCode != http.StatusBadRequest {
						t.Errorf("err=%v, want server BadRequest refusal", err)
					}
				} else if err != nil || out == nil {
					t.Errorf("out=%v err=%v, want settled success", out, err)
				}
				if s.sends.Load() != 1 {
					t.Errorf("sends=%d, want one", s.sends.Load())
				}
			})
		}
	}
}

func TestLifecycleProfileCheckBeforeUnsupportedSyntheticDelete(t *testing.T) {
	for _, value := range []string{"tRuE", "yes"} {
		t.Run(value, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			defer server.Close()
			c := New(lifecycleProfileConfig(t, server, value))
			_, err := c.DeleteSyntheticQuota(context.Background(), &DeleteSyntheticQuotaInput{Purge: true, ExpectedQuotaID: "../quota"})
			assertLifecycleProfileError(t, err, value == "yes", "monitor.DeleteSyntheticQuota")
			if requests.Load() != 0 {
				t.Errorf("requests=%d, want zero", requests.Load())
			}
		})
	}
}
