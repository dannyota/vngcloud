package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/volume"
)

// TestGoldenVolumeListVolumesByServer checks list-volumes-by-server's exact
// output shape, reusing exampleVolume from svc_volume_test.go.
func TestGoldenVolumeListVolumesByServer(t *testing.T) {
	v := &volume.ListVolumesByServerOutput{Items: []volume.Volume{exampleVolume()}}
	checkGolden(t, "volume-list-volumes-by-server.json.golden", "json", "", v)
	checkGolden(t, "volume-list-volumes-by-server.table.golden", "table", "", v)
}

// TestGoldenVolumeGetDefaultVolumeType checks get-default-volume-type's
// exact output shape, {"VolumeType": {...}}, the shape every Get in this
// design wraps its resource in.
func TestGoldenVolumeGetDefaultVolumeType(t *testing.T) {
	v := &volume.GetDefaultVolumeTypeOutput{VolumeType: volume.VolumeType{
		ID: "voltype-1", VolumeTypeID: "voltype-1", ZoneID: "zone-1", VolumeTypeZoneID: "zone-1",
	}}
	checkGolden(t, "volume-get-default-volume-type.json.golden", "json", "", v)
	checkGolden(t, "volume-get-default-volume-type.table.golden", "table", "", v)
}

// TestGoldenVolumeQuoteCreateVolume checks quote-create-volume's exact
// output shape, the same pricing.GetQuoteOutput shape pricing get-quote and
// compute quote-create-server both use.
func TestGoldenVolumeQuoteCreateVolume(t *testing.T) {
	v := &pricing.GetQuoteOutput{
		OptimumPrice:  32000,
		OriginalPrice: 32000,
		Properties:    []pricing.PriceProperty{{Name: "Volume", OptimumPrice: 32000, MonthlyPrice: 32000}},
	}
	checkGolden(t, "volume-quote-create-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-quote-create-volume.table.golden", "table", "", v)
}

// TestVolumeListVolumesByServerMissingServerIDStopsBeforeAnyRequest checks
// the required-flag guard: list-volumes-by-server's --server-id is
// vngcloud:"required", so a missing flag must refuse the command with exit
// code 2 before any request.
func TestVolumeListVolumesByServerMissingServerIDStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/servers/": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "list-volumes-by-server"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --server-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestVolumeListVolumesByServerEndToEnd runs the real list-volumes-by-server
// command against a fixture, checking the request path and that the
// decoded Volume survives the round trip.
func TestVolumeListVolumesByServerEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/servers/server-1": jsonHandler(http.StatusOK,
			`{"data":[{"uuid":"volume-1","name":"boot","status":"IN-USE","size":20,"serverId":"server-1"}]}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "list-volumes-by-server", "--server-id", "server-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-volumes-by-server: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v2/proj-1/volumes/servers/server-1"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	var out struct{ Items []volume.Volume }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "volume-1" {
		t.Fatalf("list-volumes-by-server Items = %+v", out.Items)
	}
}

// TestVolumeGetDefaultVolumeTypeSendsZoneIDQuery checks that --zone-id
// reaches the request as the zoneId query parameter, and that the command
// runs with no flag at all: ZoneID is optional, unlike every other new P1
// flag in this file.
func TestVolumeGetDefaultVolumeTypeSendsZoneIDQuery(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/volume_default_id": jsonHandler(http.StatusOK, `{"volumeTypeId":"voltype-1","volumeTypeZoneId":"zone-1"}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "get-default-volume-type", "--zone-id", "zone-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-default-volume-type: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/v1/proj-1/volume_default_id")
	if !ok || q != "zoneId=zone-1" {
		t.Fatalf("query string = %q, ok=%v, want zoneId=zone-1", q, ok)
	}
	if !strings.Contains(stdout.String(), `"ID": "voltype-1"`) {
		t.Fatalf("stdout = %s, want the default volume type printed", stdout.String())
	}
}

// TestVolumeGetDefaultVolumeTypeRunsWithoutZoneID checks that
// get-default-volume-type still runs with no --zone-id given at all: it is
// optional, not required.
func TestVolumeGetDefaultVolumeTypeRunsWithoutZoneID(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/volume_default_id": jsonHandler(http.StatusOK, `{"volumeTypeId":"voltype-1","volumeTypeZoneId":"zone-1"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "get-default-volume-type"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-default-volume-type: %v (stderr=%s)", err, stderr.String())
	}
	if q, ok := fixture.queryFor("/v1/proj-1/volume_default_id"); ok && q != "" {
		t.Fatalf("query string = %q, want empty (no zoneId sent)", q)
	}
}

// pricedVolumeQuoteInfo is the resourceInfo a quote-create-volume request
// sends: the priced keys and nothing else.
var pricedVolumeQuoteInfo = map[string]any{
	"zoneId": "zone-1", "size": float64(10), "volumeTypeId": "voltype-1",
	"period": float64(1), "isPoc": false,
}

// TestVolumeQuoteCreateVolumeSendsRequestBody drives quote-create-volume
// with --name set and checks that the request body holds only the priced
// keys: --name is accepted but never sent.
func TestVolumeQuoteCreateVolumeSendsRequestBody(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"optimumPrice":32000,"originalPrice":32000,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "quote-create-volume",
		"--name", "vol-1", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-create-volume: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/price"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["resourceType"] != "volume" || decoded["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", decoded)
	}
	info, ok := decoded["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", decoded)
	}
	if !reflect.DeepEqual(info, pricedVolumeQuoteInfo) {
		t.Fatalf("resourceInfo = %v, want only the priced keys %v", info, pricedVolumeQuoteInfo)
	}

	var out pricing.GetQuoteOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.OptimumPrice != 32000 {
		t.Fatalf("OptimumPrice = %v, want 32000", out.OptimumPrice)
	}
}

// TestVolumeQuoteCreateVolumeHasNoMaxPriceOrNoWaitFlag checks that MaxPrice
// and NoWait, which only govern a future create-volume, register no flag on
// quote-create-volume.
func TestVolumeQuoteCreateVolumeHasNoMaxPriceOrNoWaitFlag(t *testing.T) {
	cmd := newVolumeCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"quote-create-volume"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if f := sub.Flags().Lookup("max-price"); f != nil {
		t.Fatalf("quote-create-volume registered its own --max-price flag: %+v", f)
	}
	if f := sub.Flags().Lookup("no-wait"); f != nil {
		t.Fatalf("quote-create-volume registered its own --no-wait flag: %+v", f)
	}
}

// TestVolumeQuoteCreateVolumeMissingRequiredFieldExitsWithZeroRequests
// checks that quote-create-volume without --volume-type-id fails the
// required-field check before any request.
func TestVolumeQuoteCreateVolumeMissingRequiredFieldExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "quote-create-volume",
		"--name", "vol-1", "--zone-id", "zone-1", "--size", "10",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --volume-type-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}
