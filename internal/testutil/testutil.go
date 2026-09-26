package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func NewCoreClient(t testing.TB, handler http.Handler) *core.Client {
	t.Helper()

	return core.ClientOf(NewConfig(t, handler))
}

// NewConfig builds a Config wired to an httptest server that closes with t,
// for tests exercising the shared-Config surface directly. Its transport
// never retries, so a test sees exactly one call per request.
func NewConfig(t testing.TB, handler http.Handler) core.Config {
	t.Helper()

	return newConfig(t, handler, transport.Config{})
}

// NewRetryConfig is NewConfig with a transport that retries up to 3 times
// with a 1ms interval, so a test can prove what is and is not retried
// without slowing the suite down.
func NewRetryConfig(t testing.TB, handler http.Handler) core.Config {
	t.Helper()

	return newConfig(t, handler, transport.Config{RetryCount: 3, RetryInterval: time.Millisecond})
}

func newConfig(t testing.TB, handler http.Handler, tcfg transport.Config) core.Config {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tcfg.HTTPClient = server.Client()
	return core.NewTestConfig("hcm-3", "project-1", endpoints.Set{
		Region:   "hcm-3",
		VServer:  server.URL + "/",
		VLB:      server.URL + "/",
		VNetwork: server.URL + "/",
		GLB:      server.URL + "/",
		DNS:      server.URL + "/",
		VCR:      server.URL + "/",
		Portal:   server.URL + "/",
		Billing:  server.URL + "/",
		Monitor:  server.URL + "/",
	}, transport.New(tcfg))
}

func FixtureBody(t testing.TB, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func WriteFixture(t testing.TB, w http.ResponseWriter, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("%s: invalid JSON", path)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(json.RawMessage(data)); err != nil {
		t.Error(err)
	}
}
