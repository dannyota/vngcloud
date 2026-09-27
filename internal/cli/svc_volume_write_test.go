package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/volume"
)

// volumeQuoteJSON renders one price-quote response priced at optimumPrice,
// the shape create-volume's own quote step reads before it orders.
func volumeQuoteJSON(optimumPrice float64) string {
	return fmt.Sprintf(`{"optimumPrice":%v,"originalPrice":%v,"discountPrice":0,"propertiesPrice":[]}`,
		optimumPrice, optimumPrice)
}

// volumeEmptyListJSON is one empty ListVolumes page: no volume matches,
// the shape create-volume's own pre-order duplicate-name check gets for a
// name nothing already uses.
const volumeEmptyListJSON = `{"listData":[],"page":0,"pageSize":10000,"totalPage":0,"totalItem":0}`

// TestGoldenVolumeCreateVolume checks create-volume's exact output shape:
// the created volume plus the quoted MonthlyPrice.
func TestGoldenVolumeCreateVolume(t *testing.T) {
	v := &volume.CreateVolumeOutput{Volume: exampleVolume(), MonthlyPrice: 32000}
	checkGolden(t, "volume-create-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-create-volume.table.golden", "table", "", v)
}

// TestGoldenVolumeDeleteVolume checks delete-volume's exact output shape: an
// empty object, since DeleteVolumeOutput carries no field.
func TestGoldenVolumeDeleteVolume(t *testing.T) {
	v := &volume.DeleteVolumeOutput{}
	checkGolden(t, "volume-delete-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-delete-volume.table.golden", "table", "", v)
}

// TestVolumeCreateVolumeSendsOrderRequestBody drives create-volume with
// --max-price above the quoted price, checking that the exact flag-to-body
// mapping reaches the order POST, and that the printed Output carries the
// quoted MonthlyPrice, mirroring monitor's own
// TestMonitorCreateLogProjectSendsOrderRequestBody for this design's own
// volume create.
func TestVolumeCreateVolumeSendsOrderRequestBody(t *testing.T) {
	var orderBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(volumeEmptyListJSON))
			case http.MethodPost:
				defer func() { _ = r.Body.Close() }()
				orderBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, volumeQuoteJSON(32000)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "create-volume",
		"--name", "data", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1",
		"--max-price", "32000", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-volume: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(orderBody, &decoded); err != nil {
		t.Fatalf("order body is not valid JSON: %v (%s)", err, orderBody)
	}
	want := map[string]any{"name": "data", "size": float64(10), "volumeTypeId": "voltype-1", "zoneId": "zone-1", "isEnableAutoRenew": false}
	for k, v := range want {
		if decoded[k] != v {
			t.Fatalf("order body[%q] = %v, want %v (body=%s)", k, decoded[k], v, orderBody)
		}
	}

	out := stdout.String()
	if !strings.Contains(out, `"UUID": "volume-1"`) || !strings.Contains(out, `"MonthlyPrice": 32000`) {
		t.Fatalf("stdout = %s, want the new UUID and quoted MonthlyPrice printed", out)
	}
}

// TestVolumeCreateVolumeDefaultMaxPriceRefusesAboveZero checks the design's
// price guard: --max-price left at its default of 0 refuses a real,
// non-free quote with error code PriceAboveMax and sends no order.
func TestVolumeCreateVolumeDefaultMaxPriceRefusesAboveZero(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(volumeEmptyListJSON))
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, volumeQuoteJSON(32000)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "create-volume",
		"--name", "data", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a PriceAboveMax refusal")
	}
	if got := classify(err).Code; got != "PriceAboveMax" {
		t.Fatalf("Code = %q, want PriceAboveMax (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (name check and quote only, no order)", n)
	}
}

// TestVolumeCreateVolumeAmbiguous502KeepsListAdviceInMessage checks that a
// 502 on the order POST reaches the CLI's error envelope with
// wrapAmbiguousVolumeCreateErr's own advice folded into Message, not just
// the bare APIError text a plain 502 would otherwise carry: an agent that
// prints only Message must still see not to repeat an order that may have
// already reached the server, while Code and Status still come from the
// APIError underneath.
func TestVolumeCreateVolumeAmbiguous502KeepsListAdviceInMessage(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(volumeEmptyListJSON))
			case http.MethodPost:
				w.WriteHeader(http.StatusBadGateway)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, volumeQuoteJSON(40000)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "create-volume",
		"--name", "data", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1",
		"--max-price", "40000", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a 502 error")
	}

	env := classify(err)
	if env.Status != http.StatusBadGateway {
		t.Fatalf("Status = %d, want %d (stderr=%s)", env.Status, http.StatusBadGateway, stderr.String())
	}
	if env.Code != "ServerError" {
		t.Fatalf("Code = %q, want ServerError", env.Code)
	}
	if !strings.Contains(env.Message, "list volumes and match the name exactly before creating it again") {
		t.Fatalf("Message = %q, want the create-may-have-landed advice", env.Message)
	}
	if !strings.Contains(env.Message, "Bad Gateway") {
		t.Fatalf("Message = %q, want the server's own Bad Gateway text kept too", env.Message)
	}
}

// TestVolumeCreateVolumeMaxPriceNaNExitsWithZeroRequests checks
// core.CheckMaxPrice's guard reaches the CLI: a NaN --max-price is refused
// with InvalidUsage before any request, including the duplicate-name list.
func TestVolumeCreateVolumeMaxPriceNaNExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "create-volume",
		"--name", "data", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1",
		"--max-price", "NaN",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an invalid-input refusal for a NaN --max-price")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestVolumeCreateVolumeReadOnlyRefusedWithZeroRequests checks the design's
// read-only rule: create-volume is a Write, so a read-only profile refuses
// it before any request, including the duplicate-name check and the quote.
func TestVolumeCreateVolumeReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes": refuse,
		"/v1/price":          refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "volume", "create-volume",
		"--name", "data", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1",
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

// TestVolumeDeleteVolumeWithoutYesExitsWithZeroRequests checks the design's
// --yes rule: delete-volume is Write and Destructive.
func TestVolumeDeleteVolumeWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "delete-volume", "--volume-id", "volume-1"})
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

// TestVolumeDeleteVolumeWithYesSendsDelete checks that --yes together with
// --no-wait lets delete-volume read the volume once (AVAILABLE, so the
// in-use guard passes) and send exactly one DELETE.
func TestVolumeDeleteVolumeWithYesSendsDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","name":"data","status":"AVAILABLE"}}`))
			case http.MethodDelete:
				w.WriteHeader(http.StatusAccepted)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"volume", "delete-volume", "--volume-id", "volume-1", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-volume: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (one read, one delete)", n)
	}
}

// TestVolumeDeleteVolumeRefusesInUseWithNoDeleteRequest checks the design's
// pre-delete guard: a volume the pre-delete read shows IN-USE is refused
// with error code VolumeInUse and no DELETE is ever sent.
func TestVolumeDeleteVolumeRefusesInUseWithNoDeleteRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want GET only (no delete)", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","name":"data","status":"IN-USE","serverId":"server-1"}}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"volume", "delete-volume", "--volume-id", "volume-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a VolumeInUse refusal")
	}
	if got := classify(err).Code; got != "VolumeInUse" {
		t.Fatalf("Code = %q, want VolumeInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-delete read only)", n)
	}
}

// TestVolumeDeleteVolumeReadOnlyRefusedWithZeroRequests checks the design's
// read-only rule for delete-volume, matching create-volume's own read-only
// test above.
func TestVolumeDeleteVolumeReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{"--profile", "agent", "--yes", "volume", "delete-volume", "--volume-id", "volume-1"})
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
