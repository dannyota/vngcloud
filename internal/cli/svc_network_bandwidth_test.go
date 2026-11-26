package cli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

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
			expected := "Example"
			if query != "" {
				expected = "1"
			}
			if calls != 1 || !strings.Contains(stdout.String(), expected) {
				t.Fatalf("calls %d output %s", calls, stdout.String())
			}
		})
	}
}
