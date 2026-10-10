package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/pricing"
)

// TestGoldenComputeListFlavorZones checks list-flavor-zones' exact output
// shape through the generic renderer.
func TestGoldenComputeListFlavorZones(t *testing.T) {
	v := &compute.ListFlavorZonesOutput{Items: []compute.FlavorZone{
		{ID: "flavor-zone-1", Name: "General Purpose", Description: "General purpose flavors", ZoneID: "zone-1"},
	}}
	checkGolden(t, "compute-list-flavor-zones.json.golden", "json", "", v)
	checkGolden(t, "compute-list-flavor-zones.table.golden", "table", "", v)
}

// TestGoldenComputeListFlavors checks list-flavors' exact output shape,
// including the RemainingVMs and IsSoldOut fields the paid writes design
// added to compute.Flavor.
func TestGoldenComputeListFlavors(t *testing.T) {
	v := &compute.ListFlavorsOutput{Items: []compute.Flavor{
		{FlavorID: "flavor-1", Name: "s2-general-1x2", CPU: 1, Memory: 2048, RemainingVMs: 10, IsSoldOut: false, FlavorZoneID: "flavor-zone-1"},
	}}
	checkGolden(t, "compute-list-flavors.json.golden", "json", "", v)
	checkGolden(t, "compute-list-flavors.table.golden", "table", "", v)
}

// TestGoldenComputeQuoteCreateServer checks quote-create-server's exact
// output shape: the same pricing.GetQuoteOutput shape pricing get-quote and
// volume quote-create-volume both use, unwrapped rather than nested under a
// resource field.
func TestGoldenComputeQuoteCreateServer(t *testing.T) {
	v := &pricing.GetQuoteOutput{
		OptimumPrice:  347800,
		OriginalPrice: 347800,
		Properties: []pricing.PriceProperty{
			{Name: "INSTANCE TYPE", OptimumPrice: 283800, MonthlyPrice: 283800},
			{Name: "ROOT DISK", OptimumPrice: 64000, MonthlyPrice: 64000},
		},
	}
	checkGolden(t, "compute-quote-create-server.json.golden", "json", "", v)
	checkGolden(t, "compute-quote-create-server.table.golden", "table", "", v)
}

// TestComputeListFlavorZonesFiltersByZoneIDEndToEnd runs the real
// list-flavor-zones command against a fixture that returns every zone's
// flavor zones, checking that --zone-id narrows the printed result: the API
// itself ignores the filter (compute.ListFlavorZones applies it client
// side), so this also confirms the CLI reaches that SDK behavior rather than
// sending zoneId on the wire.
func TestComputeListFlavorZonesFiltersByZoneIDEndToEnd(t *testing.T) {
	body := `{"flavorZones":[` +
		`{"id":"flavor-zone-1","name":"General Purpose","description":"","zoneId":"zone-1"},` +
		`{"id":"flavor-zone-2","name":"High CPU","description":"","zoneId":"zone-2"}]}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/flavor_zones/product": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "list-flavor-zones", "--zone-id", "zone-2",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-flavor-zones: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/proj-1/flavor_zones/product"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	var out struct{ Items []compute.FlavorZone }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(out.Items) != 1 || out.Items[0].ID != "flavor-zone-2" {
		t.Fatalf("list-flavor-zones Items = %+v, want only flavor-zone-2", out.Items)
	}
}

// validQuoteCreateServerArgs is the flag set quote-create-server needs to
// pass its required-field check, one repeatable --security-group-id given
// twice so the request-body test below can confirm both values reach the
// quote.
var validQuoteCreateServerArgs = []string{
	"compute", "quote-create-server",
	"--name", "web-1", "--zone-id", "zone-1", "--flavor-id", "flavor-1", "--image-id", "image-1",
	"--vpc-id", "vpc-1", "--subnet-id", "subnet-1",
	"--security-group-id", "sg-1", "--security-group-id", "sg-2",
	"--ssh-key-id", "key-1", "--root-disk-size", "20", "--root-disk-type-id", "voltype-1",
}

// TestComputeQuoteCreateServerSendsRequestBody drives quote-create-server
// with every required flag, including --security-group-id given twice,
// checking the exact request body the CLI builds from that merge and that
// both repeated values reach it as an array: the SDK's own test
// (compute.TestQuoteCreateServerSendsCreateBody) checks buildCreateServerBody
// itself, not that the flags reach it field for field.
func TestComputeQuoteCreateServerSendsRequestBody(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"optimumPrice":347800,"originalPrice":347800,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validQuoteCreateServerArgs...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-create-server: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/price"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["resourceType"] != "server" || decoded["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", decoded)
	}
	info, ok := decoded["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", decoded)
	}
	sgs, ok := info["securityGroup"].([]any)
	if !ok || len(sgs) != 2 || sgs[0] != "sg-1" || sgs[1] != "sg-2" {
		t.Fatalf("resourceInfo[securityGroup] = %v, want [sg-1 sg-2]", info["securityGroup"])
	}
	if info["name"] != "web-1" || info["flavorId"] != "flavor-1" || info["rootDiskSize"] != float64(20) {
		t.Fatalf("unexpected resourceInfo: %+v", info)
	}

	var out pricing.GetQuoteOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.OptimumPrice != 347800 {
		t.Fatalf("OptimumPrice = %v, want 347800", out.OptimumPrice)
	}
}

// TestComputeQuoteCreateServerHasNoUserDataMaxPriceOrNoWaitFlag checks that
// UserData, MaxPrice, and NoWait, which only govern a future create-server,
// register no flag on quote-create-server: all three stay settable only
// through --cli-input-json, and this read ignores them regardless.
func TestComputeQuoteCreateServerHasNoUserDataMaxPriceOrNoWaitFlag(t *testing.T) {
	cmd := newComputeCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"quote-create-server"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, name := range []string{"user-data", "max-price", "no-wait"} {
		if f := sub.Flags().Lookup(name); f != nil {
			t.Fatalf("quote-create-server registered its own --%s flag: %+v", name, f)
		}
	}
}

// TestComputeQuoteCreateServerMissingRequiredFieldExitsWithZeroRequests
// checks that quote-create-server without --security-group-id (or any other
// required CreateServerInput field) fails the required-field check before
// any request.
func TestComputeQuoteCreateServerMissingRequiredFieldExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "quote-create-server",
		"--name", "web-1", "--zone-id", "zone-1", "--flavor-id", "flavor-1", "--image-id", "image-1",
		"--vpc-id", "vpc-1", "--subnet-id", "subnet-1",
		"--ssh-key-id", "key-1", "--root-disk-size", "20", "--root-disk-type-id", "voltype-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --security-group-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeQuoteCreateServerIsAReadUnderReadOnly checks the CLI design's
// rule that quotes are reads: a read-only profile must not refuse
// quote-create-server the way it would a real write, and the request still
// reaches the fixture.
func TestComputeQuoteCreateServerIsAReadUnderReadOnly(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(http.StatusOK, `{"optimumPrice":347800,"originalPrice":347800,"discountPrice":0,"propertiesPrice":[]}`),
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs(append([]string{"--profile", "agent", "compute", "quote-create-server"},
		validQuoteCreateServerArgs[2:]...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-create-server: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (quote-create-server must run as a read under read-only)", n)
	}
}
