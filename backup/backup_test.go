package backup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/backup"
	"danny.vn/vngcloud/internal/core"
)

const marker = "SECRET-MARKER"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../testdata/backup/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func client(t *testing.T, handler http.HandlerFunc, opts ...vngcloud.LoadOption) *backup.Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	guarded := &http.Client{Transport: backupTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != s.Listener.Addr().String() {
			t.Errorf("unexpected test destination: %s", r.URL.Host)
			return nil, errors.New("refusing non-test destination")
		}
		return s.Client().Transport.RoundTrip(r)
	})}
	base := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithProjectID("ignored-project"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{BackupCenter: s.URL + "/vbackup-gateway/"}), vngcloud.WithHTTPClient(guarded), vngcloud.WithRetry(0, 0)}
	cfg, err := vngcloud.NewConfig(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return backup.New(cfg)
}

func TestLists(t *testing.T) {
	var paths []string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("projectId") != "" {
			t.Error("unexpected method or headers")
		}
		paths = append(paths, r.URL.RequestURI())
		if strings.HasSuffix(r.URL.Path, "backends") {
			_, _ = fmt.Fprint(w, fixture(t, "backends"))
		} else {
			_, _ = fmt.Fprint(w, fixture(t, "policies"))
		}
	})
	b, err := c.ListBackends(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantB := &backup.ListBackendsOutput{Items: []backup.Backend{{ID: "backend-1", Name: "backend"}}, TotalPage: 1, TotalItem: 1}
	if !reflect.DeepEqual(b, wantB) {
		t.Fatalf("backends = %+v", b)
	}
	p, err := c.ListPolicies(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 7
	quantity := 2
	typ := "incremental"
	want := backup.Policy{ID: "policy-1", BackendID: "backend-1", ProjectID: "project-1", Product: "SERVER", Name: "policy", IsDefault: true, BackupInstanceCount: 3, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Config: backup.PolicyConfig{Hour: 1, Minute: 30, TimeZone: "UTC", HourlyEnabled: true, DailyEnabled: true, WeeklyEnabled: true, MonthlyEnabled: true, IsProtectedServer: true, DailyConfig: &backup.DailyConfig{Retention: &n, BackupType: &typ, IncrementalQuantity: &quantity}}}
	if !reflect.DeepEqual(p.Items, []backup.Policy{want}) || p.Page != 1 || p.PageSize != 200 || p.TotalPage != 2 || p.TotalItem != 3 {
		t.Fatalf("policies = %+v", p)
	}
	if _, err = c.ListPolicies(context.Background(), &backup.ListPoliciesInput{Page: 2, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListBackends(context.Background(), &backup.ListBackendsInput{}); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"/vbackup-gateway/v1/backends", "/vbackup-gateway/v1/backup-policies?page=1&size=200", "/vbackup-gateway/v1/backup-policies?page=2&size=10", "/vbackup-gateway/v1/backends"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatal(paths)
	}
	assertSafe(t, b, p)
}

func assertSafe(t *testing.T, values ...any) {
	t.Helper()
	for _, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{string(b), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v)} {
			if strings.Contains(s, marker) {
				t.Fatal("secret escaped")
			}
		}
	}
}

type provider struct{ calls int }

func (p *provider) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	return vngcloud.Token{AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*provider) Invalidate(string) {}

func TestLocalValidation(t *testing.T) {
	for _, region := range []string{"han-1", "unknown", "hcm-3"} {
		t.Run(region, func(t *testing.T) {
			p := &provider{}
			requests := 0
			c := client(t, func(http.ResponseWriter, *http.Request) { requests++ }, vngcloud.WithRegion(region), vngcloud.WithCredentialsProvider(p))
			if region == "unknown" {
				for _, call := range []func() error{func() error { _, e := c.ListBackends(context.Background(), nil); return e }, func() error { _, e := c.ListPolicies(context.Background(), nil); return e }} {
					if !errors.Is(call(), vngcloud.ErrInvalidConfig) {
						t.Fatal("region accepted")
					}
				}
			} else {
				for _, in := range []*backup.ListPoliciesInput{{Page: -1}, {Size: -1}} {
					_, e := c.ListPolicies(context.Background(), in)
					if !errors.Is(e, vngcloud.ErrInvalidInput) {
						t.Fatal(e)
					}
				}
			}
			if requests != 0 || p.calls != 0 {
				t.Fatal("validation used auth or network")
			}
		})
	}
	for _, call := range []func() error{func() error { _, e := backup.New(vngcloud.Config{}).ListBackends(context.Background(), nil); return e }, func() error { _, e := backup.New(vngcloud.Config{}).ListPolicies(context.Background(), nil); return e }} {
		if !errors.Is(call(), vngcloud.ErrInvalidConfig) {
			t.Fatal("zero config accepted")
		}
	}
}

func TestBodies(t *testing.T) {
	for _, body := range []string{"", `null`, `[]`, `{}`, `{"data":[]}`, `{"items":null,"page":1,"pageSize":10,"totalPages":0,"totalItems":0}`, `{"items":[],"page":"SECRET-MARKER","pageSize":10,"totalPages":0,"totalItems":0}`, `{"items":[]}`, `<html>SECRET-MARKER</html>`, `{"items":[{"id":true}]}`} {
		t.Run(body, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) })
			_, b := c.ListBackends(context.Background(), nil)
			_, p := c.ListPolicies(context.Background(), nil)
			if b == nil || p == nil {
				t.Fatal("unrecognized body accepted")
			}
			for _, err := range []error{b, p} {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != 200 {
					t.Fatal("decode error lost HTTP status")
				}
			}
			assertSafe(t, b, p)
		})
	}
	for _, name := range []string{"empty", "policies"} {
		c := client(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, fixture(t, name)) })
		if _, e := c.ListPolicies(context.Background(), &backup.ListPoliciesInput{}); e != nil {
			t.Fatal(e)
		}
	}
	for _, daily := range []string{"", `,"dailyConfig":null`, `,"dailyConfig":{}`, `,"dailyConfig":{"retention":0,"backupType":"","incrementalQuantity":0}`} {
		c := client(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"items":[{"config":{%s}}],"page":2,"pageSize":10,"totalPages":2,"totalItems":1}`, strings.TrimPrefix(daily, ","))
		})
		out, e := c.ListPolicies(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		d := out.Items[0].Config.DailyConfig
		switch {
		case daily == "" || strings.Contains(daily, "null"):
			if d != nil {
				t.Fatal("expected nil daily config")
			}

		case daily == `,"dailyConfig":{}`:
			if d == nil || d.Retention != nil || d.BackupType != nil || d.IncrementalQuantity != nil {
				t.Fatal("expected absent members")
			}

		case d == nil || d.Retention == nil || *d.Retention != 0 || d.BackupType == nil || *d.BackupType != "" || d.IncrementalQuantity == nil || *d.IncrementalQuantity != 0:
			t.Fatal("zero values lost")
		}
		if out.Page != 2 {
			t.Fatal("page lost")
		}
	}
}

func TestErrorsAndSecrecy(t *testing.T) {
	for _, status := range []int{200, 401, 403, 404, 429, 500, 503} {
		for _, policies := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, policies), func(t *testing.T) {
				var logs bytes.Buffer
				captures := 0
				c := client(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					if status == 200 {
						_, _ = fmt.Fprint(w, fixture(t, "policies"))
					} else {
						_, _ = fmt.Fprint(w, `{"code":"SECRET-MARKER","message":"SECRET-MARKER"}`)
					}
				}, vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
				var out any
				var err error
				if policies {
					out, err = c.ListPolicies(context.Background(), nil)
				} else {
					out, err = c.ListBackends(context.Background(), nil)
				}
				if status == 200 {
					if err != nil {
						t.Fatal(err)
					}
					assertSafe(t, out)
				} else {
					var a *vngcloud.APIError
					if !errors.As(err, &a) || a.StatusCode != status || a.Code != core.ResolvedCode(status, "") || a.Operation == "" || a.Retryable != (status == 429 || status == 502 || status == 503 || status == 504) {
						t.Fatalf("unexpected error: %+v", err)
					}
					sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
					if sentinel != nil && !errors.Is(err, sentinel) {
						t.Fatal(err)
					}
					assertSafe(t, err)
				}
				if captures != 0 || strings.Contains(logs.String(), marker) {
					t.Fatal("capture or logs exposed body")
				}
			})
		}
	}
}

func TestRetryAndCancellation(t *testing.T) {
	for _, status := range []int{401, 429, 503} {
		calls := 0
		c := client(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"code":"SECRET-MARKER"}`)
			} else {
				_, _ = fmt.Fprint(w, fixture(t, "empty"))
			}
		}, vngcloud.WithRetry(1, time.Nanosecond))
		if _, e := c.ListPolicies(context.Background(), nil); e != nil {
			t.Fatal(e)
		}
		if calls != 2 {
			t.Fatalf("calls=%d", calls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := client(t, func(http.ResponseWriter, *http.Request) { t.Error("canceled request sent") })
	if _, e := c.ListBackends(ctx, nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestPolicyWholeNumbers(t *testing.T) {
	for _, number := range []string{"7.0", "7e0", "7.5", "9223372036854775808", "1e1000000", `"7"`} {
		t.Run(number, func(t *testing.T) {
			c := client(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"items":[{"config":{"hour":%s,"minute":%s,"dailyConfig":{"retention":%s,"incrementalQuantity":%s}}}],"page":1,"pageSize":200,"totalPages":1,"totalItems":1}`, number, number, number, number)
			})
			out, err := c.ListPolicies(context.Background(), nil)
			if number == "7.0" || number == "7e0" {
				if err != nil {
					t.Fatal(err)
				}
				config := out.Items[0].Config
				if config.Hour != 7 || config.Minute != 7 || config.DailyConfig == nil || *config.DailyConfig.Retention != 7 || *config.DailyConfig.IncrementalQuantity != 7 {
					t.Fatal("integer decoding failed")
				}
			} else if err == nil {
				t.Fatal("non-integer accepted")
			}
		})
	}
}

func TestRegionalOverride(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		t.Run(region, func(t *testing.T) {
			requests := 0
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/vbackup-gateway/v1/backends" {
					t.Error(r.URL.Path)
				}
				_, _ = fmt.Fprint(w, fixture(t, "backends"))
			}, vngcloud.WithRegion(region))
			if _, err := c.ListBackends(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatal(requests)
			}
		})
	}
}

func TestUnmappedWithoutOverride(t *testing.T) {
	p := &provider{}
	requests := 0
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("unknown"), vngcloud.WithCredentialsProvider(p), vngcloud.WithHTTPClient(&http.Client{Transport: backupTransport(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected request") })}))
	if err != nil {
		t.Fatal(err)
	}
	c := backup.New(cfg)
	for _, call := range []func() error{func() error { _, e := c.ListBackends(context.Background(), nil); return e }, func() error { _, e := c.ListPolicies(context.Background(), nil); return e }, func() error {
		_, e := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
		return e
	}} {
		if err := call(); !errors.Is(err, vngcloud.ErrInvalidConfig) {
			t.Fatal(err)
		}
	}
	if p.calls != 0 || requests != 0 {
		t.Fatal("unexpected access")
	}
}

type backupTransport func(*http.Request) (*http.Response, error)

func (f backupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHANPolicyDestinations(t *testing.T) {
	for _, tc := range []struct {
		name, path, query, fixture string
		call                       func(*backup.Client) error
	}{
		{"list", "/vbackup-gateway/v1/backup-policies", "page=1&size=200", "policies", func(c *backup.Client) error {
			_, err := c.ListPolicies(context.Background(), nil)
			return err
		}},
		{"get", "/vbackup-gateway/v1/backup-policies/synthetic", "", "get-policy", func(c *backup.Client) error {
			_, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "synthetic"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &provider{}
			requests := 0
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithCredentialsProvider(p), vngcloud.WithHTTPClient(&http.Client{Transport: backupTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "han-1.api.vngcloud.vn" || r.URL.Path != tc.path || r.URL.RawQuery != tc.query {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fixture(t, tc.fixture)))}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.call(backup.New(cfg)); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || p.calls != 1 {
				t.Fatalf("requests=%d credentials=%d", requests, p.calls)
			}
		})
	}
}

func TestRegionalDefaultDestination(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		t.Run(region, func(t *testing.T) {
			p := &provider{}
			requests := 0
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion(region), vngcloud.WithCredentialsProvider(p), vngcloud.WithHTTPClient(&http.Client{Transport: backupTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Scheme != "https" || r.URL.Host != region+".api.vngcloud.vn" || r.URL.Path != "/vbackup-gateway/v1/backends" {
					t.Errorf("unexpected URL %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fixture(t, "backends")))}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backup.New(cfg).ListBackends(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || p.calls != 1 {
				t.Fatalf("requests=%d credentials=%d", requests, p.calls)
			}
		})
	}
}
