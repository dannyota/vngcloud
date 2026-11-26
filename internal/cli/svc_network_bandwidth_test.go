package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestBandwidthPackagesCLIWithholdsError(t *testing.T) {
	withCleanEnv(t)
	rt := saCLITransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"peer-code-canary","message":"peer-message-canary"}`)), Request: r}, nil
	})
	withTestOptions(t, vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
	var stdout, stderr strings.Builder
	root := newRootCmd(strings.NewReader(""), &stdout, &stderr)
	root.SetArgs([]string{"--region", "han-1", "--project-id", "project-1", "network", "list-bandwidth-packages"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	printError(&stderr, err)
	if strings.Contains(err.Error()+stdout.String()+stderr.String(), "peer-") {
		t.Fatal("peer error leaked")
	}
}

func TestBandwidthPackagesCLI(t *testing.T) {
	for _, query := range []string{"", "length(Items)"} {
		t.Run(query, func(t *testing.T) {
			withCleanEnv(t)
			calls := 0
			rt := saCLITransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "han-1.console.greennode.ai" || r.URL.RawQuery != "" {
					t.Errorf("unexpected URL %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"uuid":"package-1","name":"Example","type":"X","description":"Synthetic","bandwidth":100}]}`)), Request: r}, nil
			})
			withTestOptions(t, vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
			var stdout, stderr strings.Builder
			root := newRootCmd(strings.NewReader(""), &stdout, &stderr)
			root.SetArgs([]string{"--region", "han-1", "--project-id", "project-1", "--read-only", "--output", "json", "--query", query, "network", "list-bandwidth-packages"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("%v: %s", err, stderr.String())
			}
			if calls != 1 {
				t.Fatalf("calls %d output %s", calls, stdout.String())
			}
			if query != "" {
				var count int
				if err := json.Unmarshal([]byte(stdout.String()), &count); err != nil || count != 1 {
					t.Fatalf("count output %s: %v", stdout.String(), err)
				}
			} else if !strings.Contains(stdout.String(), "Example") {
				t.Fatalf("output %s", stdout.String())
			}
		})
	}
}
