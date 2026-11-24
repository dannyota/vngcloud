package backup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/backup"
	"danny.vn/vngcloud/internal/transport"
)

func TestGetPolicy(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RequestURI() != "/vbackup-gateway/v1/backup-policies/synthetic" || r.Header.Get("projectId") != "" {
			t.Error("unexpected request")
		}
		_, _ = fmt.Fprint(w, fixture(t, "get-policy"))
	})
	out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	p := out.Policy
	if p.ID != "synthetic" || p.BackendID != "synthetic" || p.ProjectID != "synthetic" || p.Product != "synthetic" || p.Name != "synthetic" || p.IsDefault || p.BackupInstanceCount != 0 || p.CreatedAt != "synthetic" || p.UpdatedAt != "synthetic" || p.Config.TimeZone != "synthetic" || p.Config.DailyConfig == nil || *p.Config.DailyConfig.Retention != 0 || *p.Config.DailyConfig.BackupType != "synthetic" || *p.Config.DailyConfig.IncrementalQuantity != 0 {
		t.Fatalf("unexpected policy: %+v", p)
	}
}

func TestGetPolicyInvalid(t *testing.T) {
	for _, body := range []string{``, `null`, `[]`, `{}`, `{"Id":"policy-1","config":{}}`, `{"id":"policy-1","Config":{}}`, `{"id":"policy-1","config":null}`, `{"id":"other","config":{}}`, `{"id":"policy-1","id":"policy-1","config":{}}`, `{"id":"policy-1","ID":"policy-1","config":{}}`, `{"id":"policy-1","config":[],"secret":"SECRET-MARKER"}`, `{"id":"policy-1","config":{"hour":1,"Hour":2}}`, `{"id":"policy-1","config":{"dailyConfig":{"retention":1,"Retention":2}}}`, `{"id":"policy-1","config":{},"unknown":{"a":1,"a":2}}`} {
		c := client(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) })
		out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
		if err == nil || out != nil || strings.Contains(fmt.Sprintf("%+v", err), marker) {
			t.Fatalf("invalid body accepted or leaked: %q", body)
		}
	}
	p := &provider{}
	c := client(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }, vngcloud.WithCredentialsProvider(p))
	for _, in := range []*backup.GetPolicyInput{nil, {}, {PolicyID: "../secret"}} {
		if _, err := c.GetPolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("invalid input: %v", err)
		}
	}
	if p.calls != 0 {
		t.Fatal("unexpected authentication")
	}
	c = client(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }, vngcloud.WithRegion("han-1"), vngcloud.WithCredentialsProvider(p))
	if _, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"}); !errors.Is(err, vngcloud.ErrInvalidConfig) || p.calls != 0 {
		t.Fatalf("region validation: %v", err)
	}
}

func TestGetPolicyUnsafeIDWithheld(t *testing.T) {
	p := &provider{}
	requests := 0
	c := client(t, func(http.ResponseWriter, *http.Request) { requests++ }, vngcloud.WithCredentialsProvider(p))
	out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "../SECRET-MARKER"})
	if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || strings.Contains(fmt.Sprintf("%+v", err), marker) || p.calls != 0 || requests != 0 {
		t.Fatalf("unsafe ID not withheld: out=%v err=%v auth=%d requests=%d", out, err, p.calls, requests)
	}
}

func TestGetPolicyDailyAndNumbers(t *testing.T) {
	for _, tc := range []struct {
		daily string
		mode  int
	}{{"", 0}, {`,"dailyConfig":null`, 0}, {`,"dailyConfig":{}`, 1}, {`,"dailyConfig":{"retention":0,"backupType":"","incrementalQuantity":0}`, 2}} {
		c := client(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"id":"policy-1","config":{%s}}`, strings.TrimPrefix(tc.daily, ","))
		})
		out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
		if err != nil {
			t.Fatal(err)
		}
		d := out.Policy.Config.DailyConfig
		if tc.mode == 0 && d != nil || tc.mode == 1 && (d == nil || d.Retention != nil || d.BackupType != nil || d.IncrementalQuantity != nil) || tc.mode == 2 && (d == nil || d.Retention == nil || *d.Retention != 0 || d.BackupType == nil || *d.BackupType != "" || d.IncrementalQuantity == nil || *d.IncrementalQuantity != 0) {
			t.Fatalf("daily mode %d: %+v", tc.mode, d)
		}
	}
	for _, n := range []string{`1.5`, `"7"`, `9223372036854775808`} {
		c := client(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"id":"policy-1","config":{"hour":%s}}`, n)
		})
		if out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"}); err == nil || out != nil {
			t.Fatal("invalid number accepted")
		}
	}
}

func TestGetPolicyOversized(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"policy-1","config":{},"secret":"SECRET-MARKER`+strings.Repeat("x", 8<<20)+`"}`)
	})
	out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
	var api *vngcloud.APIError
	if out != nil || !errors.Is(err, transport.ErrBodyTooLarge) || !errors.As(err, &api) || api.StatusCode != 200 || api.Operation != "backup.GetPolicy" {
		t.Fatalf("oversized body error: %v", err)
	}
	assertSafe(t, err)
}

type rotatingProvider struct{ calls int }

func (p *rotatingProvider) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	token := "first-secret"
	if p.calls > 1 {
		token = "second-secret"
	}
	return vngcloud.Token{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*rotatingProvider) Invalidate(string) {}

func TestGetPolicyReflectedCredentials(t *testing.T) {
	for _, field := range []string{`"backendId":"prefix-first-secret-suffix"`, `"projectId":"first-secret"`, `"product":"first-secret"`, `"name":"first-secret"`, `"createdAt":"first-secret"`, `"updatedAt":"first-secret"`, `"config":{"timeZone":"first-secret"}`, `"config":{"dailyConfig":{"backupType":"first-secret"}}`, `"name":"second-secret"`} {
		t.Run(field, func(t *testing.T) {
			p := &rotatingProvider{}
			calls := 0
			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(401)
					return
				}
				config := `,"config":{}`
				if strings.HasPrefix(field, `"config"`) {
					config = ""
				}
				encoded := strings.ReplaceAll(field, "first-secret", "first-"+string(rune(92))+"u0073ecret")
				_, _ = fmt.Fprintf(w, `{"id":"policy-1",%s%s}`, encoded, config)
			}, vngcloud.WithCredentialsProvider(p))
			out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" || api.Err != nil || calls != 2 || strings.Contains(fmt.Sprintf("%+v", err), "secret") {
				t.Fatalf("reflected credential accepted: out=%v err=%v calls=%d", out, err, calls)
			}
		})
	}
	c := client(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"synthetic-token","config":{}}`)
	})
	out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "synthetic-token"})
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
		t.Fatalf("reflected ID accepted: %v", err)
	}
	c = client(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"policy-1","config":{"hourlyConfig":{"x":"synthetic-token"}},"userId":"synthetic-token"}`)
	})
	if _, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestGetPolicyRedirectCredentialWithheld(t *testing.T) {
	destinationCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls++ }))
	defer destination.Close()
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}, vngcloud.WithStaticToken("127.0.0.1"))
	out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
	if out != nil || err == nil || destinationCalls != 0 {
		t.Fatalf("redirect accepted: %v", err)
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(fmt.Sprintf("%v %+v %#v", e, e, e), "127.0.0.1") {
			t.Fatal("redirect error exposed credential")
		}
	}
}

func TestGetPolicySecrecyAndStatuses(t *testing.T) {
	for _, status := range []int{200, 401, 403, 404, 429, 500} {
		var logs bytes.Buffer
		captures := 0
		c := client(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			if status == 200 {
				_, _ = fmt.Fprint(w, `{"id":"policy-1","name":"safe","config":{"hour":7,"minute":45,"dailyEnabled":true,"dailyConfig":{"retention":5},"hourlyConfig":{"secret":"SECRET-MARKER"},"statusSendEmail":["SECRET-MARKER"]},"userId":"SECRET-MARKER"}`)
			} else {
				_, _ = fmt.Fprint(w, `{"code":"SECRET-MARKER","message":"SECRET-MARKER"}`)
			}
		}, vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
		out, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"})
		if status == 200 {
			if err != nil || out.Policy.Config.Hour != 7 || out.Policy.Config.Minute != 45 || !out.Policy.Config.DailyEnabled || *out.Policy.Config.DailyConfig.Retention != 5 {
				t.Fatalf("result: %+v %v", out, err)
			}
			assertSafe(t, out)
			b, _ := json.Marshal(out)
			if strings.Contains(string(b), marker) {
				t.Fatal("secret escaped")
			}
		} else {
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != status {
				t.Fatalf("status error: %v", err)
			}
			sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
			if sentinel != nil && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			assertSafe(t, err)
		}
		if captures != 0 || strings.Contains(logs.String(), marker) {
			t.Fatal("secret captured or logged")
		}
	}
	for _, status := range []int{401, 429, 503} {
		calls := 0
		c := client(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(status)
				return
			}
			_, _ = fmt.Fprint(w, `{"id":"policy-1","config":{}}`)
		}, vngcloud.WithRetry(1, time.Nanosecond))
		if _, err := c.GetPolicy(context.Background(), &backup.GetPolicyInput{PolicyID: "policy-1"}); err != nil || calls != 2 {
			t.Fatalf("retry calls=%d err=%v", calls, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := client(t, func(http.ResponseWriter, *http.Request) { t.Error("canceled request sent") })
	if _, err := c.GetPolicy(ctx, &backup.GetPolicyInput{PolicyID: "policy-1"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
