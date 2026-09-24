package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func NewCoreClient(t testing.TB, handler http.Handler) *core.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return core.NewTestClient("hcm-3", "project-1", endpoints.Set{
		Region:   "hcm-3",
		VServer:  server.URL + "/",
		VLB:      server.URL + "/",
		VNetwork: server.URL + "/",
		GLB:      server.URL + "/",
		DNS:      server.URL + "/",
		VCR:      server.URL + "/",
		Portal:   server.URL + "/",
	}, transport.New(transport.Config{HTTPClient: server.Client()}))
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
