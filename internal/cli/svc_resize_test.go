package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/volume"
)

// resizeQuoteJSON renders one price-quote response priced at optimumPrice,
// shared by resize-server's and resize-volume's own quote step.
func resizeQuoteJSON(optimumPrice float64) string {
	return fmt.Sprintf(`{"optimumPrice":%v,"originalPrice":%v,"discountPrice":0,"propertiesPrice":[]}`,
		optimumPrice, optimumPrice)
}

// computeServerFlavorJSON renders one GetServer response for server-1 with
// the given status and flavor id, the shape resize-server's own pre-resize
// read and post-resize wait both decode.
func computeServerFlavorJSON(status, flavorID string) string {
	return fmt.Sprintf(`{"data":{"uuid":"server-1","name":"web-1","status":%q,"flavor":{"flavorId":%q}}}`, status, flavorID)
}

// --- compute resize-server ---

// TestGoldenComputeResizeServer checks resize-server's exact output shape.
func TestGoldenComputeResizeServer(t *testing.T) {
	v := &compute.ResizeServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "ACTIVE"}, MonthlyPrice: 631600}
	checkGolden(t, "compute-resize-server.json.golden", "json", "", v)
	checkGolden(t, "compute-resize-server.table.golden", "table", "", v)
}

// TestComputeResizeServerWithoutYesExitsWithZeroRequests checks the design's
// --yes rule: resize-server is a paid write, but still Destructive.
func TestComputeResizeServerWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "resize-server",
		"--server-id", "server-1", "--flavor-id", "flavor-2",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeResizeServerSameFlavorIsInvalidUsageWithZeroRequests checks
// that --flavor-id matching the server's current flavor is refused with
// InvalidUsage before any quote or resize request.
func TestComputeResizeServerSameFlavorIsInvalidUsageWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, computeServerFlavorJSON("ACTIVE", "flavor-1")),
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "compute", "resize-server",
		"--server-id", "server-1", "--flavor-id", "flavor-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an InvalidUsage refusal for the same flavor")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read only)", n)
	}
}

// TestComputeResizeServerUnexpectedStatusWithZeroRequests checks that a
// server that is neither ACTIVE nor STOPPED refuses with error code
// UnexpectedStatus before any quote or resize request.
func TestComputeResizeServerUnexpectedStatusWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, computeServerFlavorJSON("CREATING", "flavor-1")),
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "compute", "resize-server",
		"--server-id", "server-1", "--flavor-id", "flavor-2",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an UnexpectedStatus refusal")
	}
	if got := classify(err).Code; got != "UnexpectedStatus" {
		t.Fatalf("Code = %q, want UnexpectedStatus (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read only)", n)
	}
}

// TestComputeResizeServerDefaultMaxPriceRefusesAboveZero checks the design's
// price guard: --max-price left at its default of 0 refuses a real quote
// with error code PriceAboveMax and sends no resize.
func TestComputeResizeServerDefaultMaxPriceRefusesAboveZero(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, computeServerFlavorJSON("ACTIVE", "flavor-1")),
		"/v1/price":                   jsonHandler(http.StatusOK, resizeQuoteJSON(631600)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "compute", "resize-server",
		"--server-id", "server-1", "--flavor-id", "flavor-2",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a PriceAboveMax refusal")
	}
	if got := classify(err).Code; got != "PriceAboveMax" {
		t.Fatalf("Code = %q, want PriceAboveMax (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (read and quote only, no resize)", n)
	}
}

// TestComputeResizeServerWithYesAndMaxPriceSendsResize drives resize-server
// end to end: the resize PUT flips the fixture's own flavor, so the
// post-resize wait settles on its first poll, with no real sleep.
func TestComputeResizeServerWithYesAndMaxPriceSendsResize(t *testing.T) {
	flavorID := "flavor-1"
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(computeServerFlavorJSON("ACTIVE", flavorID)))
		},
		"/v2/proj-1/servers/server-1/resize": func(w http.ResponseWriter, _ *http.Request) {
			flavorID = "flavor-2"
			w.WriteHeader(http.StatusAccepted)
		},
		"/v1/price": jsonHandler(http.StatusOK, resizeQuoteJSON(631600)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "compute", "resize-server",
		"--server-id", "server-1", "--flavor-id", "flavor-2", "--max-price", "631600",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resize-server: %v (stderr=%s)", err, stderr.String())
	}
	var out struct {
		Server       compute.Server
		MonthlyPrice float64
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.Server.Flavor.FlavorID != "flavor-2" || out.MonthlyPrice != 631600 {
		t.Fatalf("out = %+v, want the new flavor and quoted MonthlyPrice", out)
	}
}

// TestComputeResizeServerReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule for resize-server.
func TestComputeResizeServerReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": refuse,
		"/v1/price":                   refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "--yes", "compute", "resize-server",
		"--server-id", "server-1", "--flavor-id", "flavor-2",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", classify(err).Code, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- volume resize-volume ---

// TestGoldenVolumeResizeVolume checks resize-volume's exact output shape.
func TestGoldenVolumeResizeVolume(t *testing.T) {
	v := &volume.ResizeVolumeOutput{Volume: exampleVolume(), MonthlyPrice: 64000}
	checkGolden(t, "volume-resize-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-resize-volume.table.golden", "table", "", v)
}

// TestVolumeResizeVolumeWithoutYesExitsWithZeroRequests checks the design's
// --yes rule: resize-volume is a paid write, but still Destructive.
func TestVolumeResizeVolumeWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "resize-volume",
		"--volume-id", "volume-1", "--size", "20",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestVolumeResizeVolumeShrinkIsInvalidUsageWithZeroRequests checks that
// --size at or below the volume's current size is refused with InvalidUsage
// before any quote or resize request.
func TestVolumeResizeVolumeShrinkIsInvalidUsageWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, `{"data":{"uuid":"volume-1","name":"data","status":"AVAILABLE","size":20,"volumeTypeId":"type-1"}}`),
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "resize-volume",
		"--volume-id", "volume-1", "--size", "10",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an InvalidUsage refusal for a shrink")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read only)", n)
	}
}

// TestVolumeResizeVolumeDefaultMaxPriceRefusesAboveZero checks the design's
// price guard for resize-volume.
func TestVolumeResizeVolumeDefaultMaxPriceRefusesAboveZero(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, `{"data":{"uuid":"volume-1","name":"data","status":"AVAILABLE","size":10,"volumeTypeId":"type-1"}}`),
		"/v1/price":                   jsonHandler(http.StatusOK, resizeQuoteJSON(64000)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "resize-volume",
		"--volume-id", "volume-1", "--size", "20",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a PriceAboveMax refusal")
	}
	if got := classify(err).Code; got != "PriceAboveMax" {
		t.Fatalf("Code = %q, want PriceAboveMax (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (read and quote only, no resize)", n)
	}
}

// TestVolumeResizeVolumeWithYesAndMaxPriceSendsResize drives resize-volume
// end to end: the resize PUT flips the fixture's own size, so the
// post-resize wait settles on its first poll, with no real sleep.
func TestVolumeResizeVolumeWithYesAndMaxPriceSendsResize(t *testing.T) {
	size := 10
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":"volume-1","name":"data","status":"AVAILABLE","size":%d,"volumeTypeId":"type-1"}}`, size)
		},
		"/v2/proj-1/volumes/volume-1/resize": func(w http.ResponseWriter, _ *http.Request) {
			size = 20
			w.WriteHeader(http.StatusAccepted)
		},
		"/v1/price": jsonHandler(http.StatusOK, resizeQuoteJSON(64000)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "resize-volume",
		"--volume-id", "volume-1", "--size", "20", "--max-price", "64000",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resize-volume: %v (stderr=%s)", err, stderr.String())
	}
	var out struct {
		Volume       volume.Volume
		MonthlyPrice float64
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.Volume.Size != 20 || out.MonthlyPrice != 64000 {
		t.Fatalf("out = %+v, want the new size and quoted MonthlyPrice", out)
	}
}

// TestVolumeResizeVolumeReadOnlyRefusedWithZeroRequests checks the design's
// read-only rule for resize-volume.
func TestVolumeResizeVolumeReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": refuse,
		"/v1/price":                   refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "--yes", "volume", "resize-volume",
		"--volume-id", "volume-1", "--size", "20",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", classify(err).Code, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}
