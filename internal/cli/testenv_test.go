package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// withCleanEnv points HOME and USERPROFILE at a fresh temp directory and
// clears every VNGCLOUD_* variable from the ambient environment, so a test
// never reads the real developer machine's profile files, credentials, or
// environment overrides. It returns the temp home directory. t.Setenv
// restores every value automatically at the end of the test.
func withCleanEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(name, "VNGCLOUD_") {
			t.Setenv(name, "")
		}
	}
	return home
}

// refusingTransport wraps base but refuses to dial any host that is not
// loopback, so a test whose endpoint wiring is wrong fails loudly instead of
// silently reaching the real API.
type refusingTransport struct{ base http.RoundTripper }

func (t refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return t.base.RoundTrip(req)
	default:
		return nil, errors.New("cli test transport: refusing non-loopback host " + req.URL.Hostname())
	}
}

// newFakeServer starts an httptest server using handler, and returns
// LoadOptions that route every endpoint of a Config through it via a
// transport that refuses any non-loopback host. The server is closed
// automatically at the end of the test.
func newFakeServer(t *testing.T, handler http.Handler) []vngcloud.LoadOption {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	base := server.URL + "/"
	overrides := vngcloud.EndpointOverrides{
		VServer:           base,
		VLB:               base,
		VNetwork:          base,
		GLB:               base,
		DNS:               base,
		ContainerRegistry: base,
		Portal:            base,
		Signin:            base,
		Dashboard:         base,
		Token:             base + "accounts-api/v1/auth/token",
		Billing:           base,
	}
	return []vngcloud.LoadOption{
		vngcloud.WithEndpointOverrides(overrides),
		vngcloud.WithHTTPClient(&http.Client{Transport: refusingTransport{base: server.Client().Transport}}),
	}
}

// withTestOptions sets the package's test-only load-option hook for the
// duration of one test and restores it afterward, so tests never leak
// endpoint overrides into one another.
func withTestOptions(t *testing.T, opts ...vngcloud.LoadOption) {
	t.Helper()
	prev := testOptions
	testOptions = opts
	t.Cleanup(func() { testOptions = prev })
}
