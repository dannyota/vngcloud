package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
)

// computeServerJSON renders one GetServer response for a server matching
// uuid, name, and status.
func computeServerJSON(uuid, name, status string) string {
	return fmt.Sprintf(`{"data":{"uuid":%q,"name":%q,"status":%q}}`, uuid, name, status)
}

// computeEmptyServerListJSON is one empty ListServers page: no server
// matches, the shape create-server's own pre-order duplicate-name check
// gets for a name nothing already uses.
const computeEmptyServerListJSON = `{"listData":[],"page":0,"pageSize":10000,"totalPage":0,"totalItem":0}`

// computeQuoteJSON renders one price-quote response priced at optimumPrice,
// the shape create-server's own quote step reads before it orders.
func computeQuoteJSON(optimumPrice float64) string {
	return fmt.Sprintf(`{"optimumPrice":%v,"originalPrice":%v,"discountPrice":0,"propertiesPrice":[]}`,
		optimumPrice, optimumPrice)
}

// validCreateServerArgs is the flag set create-server needs to pass its
// required-field check, matching validQuoteCreateServerArgs's own values
// (svc_compute_paid_test.go) but for the create command.
var validCreateServerArgs = []string{
	"compute", "create-server",
	"--name", "web-1", "--zone-id", "zone-1", "--flavor-id", "flavor-1", "--image-id", "image-1",
	"--vpc-id", "vpc-1", "--subnet-id", "subnet-1", "--security-group-id", "sg-1",
	"--ssh-key-id", "key-1", "--root-disk-size", "20", "--root-disk-type-id", "voltype-1",
}

func createServerArgsWithoutSSHKey() []string {
	args := make([]string, 0, len(validCreateServerArgs)-2)
	for i := 0; i < len(validCreateServerArgs); i++ {
		if validCreateServerArgs[i] == "--ssh-key-id" {
			i++
			continue
		}
		args = append(args, validCreateServerArgs[i])
	}
	return args
}

// TestGoldenComputeCreateServer checks create-server's exact output shape:
// the created server plus the quoted MonthlyPrice.
func TestGoldenComputeCreateServer(t *testing.T) {
	v := &compute.CreateServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "ACTIVE"}, MonthlyPrice: 347800}
	checkGolden(t, "compute-create-server.json.golden", "json", "", v)
	checkGolden(t, "compute-create-server.table.golden", "table", "", v)
}

// TestGoldenComputeDeleteServer checks delete-server's exact output shape:
// the volumes kept after a delete that did not remove them.
func TestGoldenComputeDeleteServer(t *testing.T) {
	v := &compute.DeleteServerOutput{KeptVolumeIDs: []string{"volume-1"}}
	checkGolden(t, "compute-delete-server.json.golden", "json", "", v)
	checkGolden(t, "compute-delete-server.table.golden", "table", "", v)
}

// TestGoldenComputeStartServer checks start-server's exact output shape.
func TestGoldenComputeStartServer(t *testing.T) {
	v := &compute.StartServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "ACTIVE"}, Changed: true}
	checkGolden(t, "compute-start-server.json.golden", "json", "", v)
	checkGolden(t, "compute-start-server.table.golden", "table", "", v)
}

// TestGoldenComputeStopServer checks stop-server's exact output shape.
func TestGoldenComputeStopServer(t *testing.T) {
	v := &compute.StopServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "STOPPED"}, Changed: true}
	checkGolden(t, "compute-stop-server.json.golden", "json", "", v)
	checkGolden(t, "compute-stop-server.table.golden", "table", "", v)
}

// TestGoldenComputeRebootServer checks reboot-server's exact output shape.
func TestGoldenComputeRebootServer(t *testing.T) {
	v := &compute.RebootServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "ACTIVE"}}
	checkGolden(t, "compute-reboot-server.json.golden", "json", "", v)
	checkGolden(t, "compute-reboot-server.table.golden", "table", "", v)
}

// TestGoldenComputeRenameServer checks rename-server's exact output shape.
func TestGoldenComputeRenameServer(t *testing.T) {
	v := &compute.RenameServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-2", Status: "ACTIVE"}}
	checkGolden(t, "compute-rename-server.json.golden", "json", "", v)
	checkGolden(t, "compute-rename-server.table.golden", "table", "", v)
}

// --- create-server ---

// TestComputeCreateServerSendsOrderRequestBody drives create-server with
// --max-price above the quoted price, checking that the order POST carries
// the flag-to-body mapping and that the printed Output carries the quoted
// MonthlyPrice.
func TestComputeCreateServerSendsOrderRequestBody(t *testing.T) {
	var orderBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(computeEmptyServerListJSON))
			case http.MethodPost:
				defer func() { _ = r.Body.Close() }()
				orderBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, computeQuoteJSON(347800)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateServerArgs, "--max-price", "347800", "--no-wait")...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-server: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(orderBody, &decoded); err != nil {
		t.Fatalf("order body is not valid JSON: %v (%s)", err, orderBody)
	}
	if decoded["name"] != "web-1" || decoded["flavorId"] != "flavor-1" || decoded["rootDiskSize"] != float64(20) {
		t.Fatalf("unexpected order body: %+v", decoded)
	}
	if _, hasUserData := decoded["userData"]; hasUserData {
		t.Fatalf("order body = %+v, want no userData key (none was given)", decoded)
	}

	out := stdout.String()
	if !strings.Contains(out, `"UUID": "server-1"`) || !strings.Contains(out, `"MonthlyPrice": 347800`) {
		t.Fatalf("stdout = %s, want the new UUID and quoted MonthlyPrice printed", out)
	}
}

// TestComputeCreateServerDefaultMaxPriceRefusesAboveZero checks the design's
// price guard: --max-price left at its default of 0 refuses a real,
// non-free quote with error code PriceAboveMax and sends no order.
func TestComputeCreateServerDefaultMaxPriceRefusesAboveZero(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(computeEmptyServerListJSON))
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, computeQuoteJSON(347800)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateServerArgs, "--no-wait")...))
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

// TestComputeCreateServerAmbiguous502KeepsListAdviceInMessage checks that a
// 502 on the order POST reaches the CLI's error envelope with
// wrapAmbiguousServerCreateErr's own advice folded into Message, not just
// the bare APIError text a plain 502 would otherwise carry: an agent that
// prints only Message must still see not to repeat a create that may have
// already reached the server, while Code and Status still come from the
// APIError underneath.
func TestComputeCreateServerAmbiguous502KeepsListAdviceInMessage(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(computeEmptyServerListJSON))
			case http.MethodPost:
				w.WriteHeader(http.StatusBadGateway)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, computeQuoteJSON(500000)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateServerArgs, "--max-price", "500000", "--no-wait")...))
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
	if !strings.Contains(env.Message, "list servers and match the name exactly before creating it again") {
		t.Fatalf("Message = %q, want the create-may-have-landed advice", env.Message)
	}
	if !strings.Contains(env.Message, "Bad Gateway") {
		t.Fatalf("Message = %q, want the server's own Bad Gateway text kept too", env.Message)
	}
}

// TestComputeCreateServerHasNoUserDataFlag checks that UserData registers no
// plain flag at all: it is settable only through --user-data-file.
func TestComputeCreateServerHasNoUserDataFlag(t *testing.T) {
	cmd := newComputeCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"create-server"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if f := sub.Flags().Lookup("user-data"); f != nil {
		t.Fatalf("create-server registered its own --user-data flag: %+v", f)
	}
	if f := sub.Flags().Lookup(userDataFileFlagName); f == nil {
		t.Fatal("create-server is missing --user-data-file")
	}
}

// TestComputeCreateServerCLIInputJSONRefusesUserData checks that an inline
// --cli-input-json value setting UserData is refused before any request,
// per the design's rule that UserData reaches the SDK only through
// --user-data-file.
func TestComputeCreateServerCLIInputJSONRefusesUserData(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateServerArgs, "--cli-input-json", `{"UserData":"#!/bin/sh\necho hi"}`)...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a refusal for an inline UserData value")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if strings.Contains(stderr.String(), "echo hi") {
		t.Fatalf("stderr = %s, want the refused user data never echoed back", stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeCreateServerUserDataFileNeverPrinted drives create-server with
// --user-data-file, checking that the secret cloud-init content reaches the
// order body but never reaches stdout, stderr, or a --debug log line.
func TestComputeCreateServerUserDataFileNeverPrinted(t *testing.T) {
	const secret = "#!/bin/sh\necho super-secret-token"
	dir := t.TempDir()
	path := filepath.Join(dir, "user-data.sh")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var orderBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(computeEmptyServerListJSON))
			case http.MethodPost:
				defer func() { _ = r.Body.Close() }()
				orderBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v1/price": jsonHandler(http.StatusOK, computeQuoteJSON(347800)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "--debug"},
		append(createServerArgsWithoutSSHKey(), "--max-price", "347800", "--no-wait", "--user-data-file", path)...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-server: %v (stderr=%s)", err, stderr.String())
	}

	if !strings.Contains(string(orderBody), "userData") {
		t.Fatalf("order body = %s, want a userData key", orderBody)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(orderBody, &body); err != nil {
		t.Fatalf("order body: %v", err)
	}
	if _, ok := body["sshKeyId"]; ok {
		t.Fatal("order body contains sshKeyId with userData")
	}
	var encoded string
	if err := json.Unmarshal(body["userData"], &encoded); err != nil {
		t.Fatalf("userData: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != secret {
		t.Fatal("userData does not encode the file content")
	}
	if strings.Contains(string(orderBody), "super-secret-token") {
		t.Fatalf("order body = %s, want the user data base64-encoded, not sent in the clear", orderBody)
	}
	if strings.Contains(stdout.String(), "super-secret-token") {
		t.Fatalf("stdout = %s, want no user data content", stdout.String())
	}
	if strings.Contains(stderr.String(), "super-secret-token") {
		t.Fatalf("stderr = %s, want no user data content, including in --debug output", stderr.String())
	}
	if strings.Contains(stdout.String(), encoded) {
		t.Fatalf("stdout = %s, want no base64 user data content", stdout.String())
	}
	if strings.Contains(stderr.String(), encoded) {
		t.Fatalf("stderr = %s, want no base64 user data content, including in --debug output", stderr.String())
	}
}

// TestComputeCreateServerUserDataFileOversizedRefused checks that a
// --user-data-file larger than 64 KiB is refused before any request.
func TestComputeCreateServerUserDataFileOversizedRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user-data.sh")
	big := bytes.Repeat([]byte("a"), maxInputFileSize+1)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateServerArgs, "--user-data-file", path)...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a refusal for an oversized --user-data-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeCreateServerReadOnlyRefusedWithZeroRequests checks the design's
// read-only rule: create-server is a Write, so a read-only profile refuses
// it before any request, including the duplicate-name check and the quote.
func TestComputeCreateServerReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers": refuse,
		"/v1/price":          refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs(append([]string{"--profile", "agent"}, validCreateServerArgs...))
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

// --- delete-server ---

// TestComputeDeleteServerWithoutYesExitsWithZeroRequests checks the design's
// --yes rule: delete-server is Write and Destructive.
func TestComputeDeleteServerWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "delete-server", "--server-id", "server-1"})
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

// TestComputeDeleteServerKeepsVolumesByDefaultAndPrintsThem checks that,
// without --delete-volumes, delete-server reads the volumes it found before
// the delete back and prints the ones still there as KeptVolumeIDs. The
// server's own GET flips to a 404 the instant the DELETE runs, so the
// post-delete wait settles on its very first read with no real sleep,
// mirroring computeToggleFixture's own approach for start-server and
// stop-server.
func TestComputeDeleteServerKeepsVolumesByDefaultAndPrintsThem(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				if deleted {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(computeServerJSON("server-1", "web-1", "ACTIVE")))
			case http.MethodDelete:
				deleted = true
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{}}`))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/volumes/servers/server-1": jsonHandler(http.StatusOK,
			`{"data":[{"uuid":"volume-1","name":"data","status":"IN-USE"}]}`),
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK,
			`{"data":{"uuid":"volume-1","name":"data","status":"AVAILABLE"}}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"compute", "delete-server", "--server-id", "server-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-server: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"KeptVolumeIDs": [`) || !strings.Contains(got, "volume-1") {
		t.Fatalf("stdout = %s, want KeptVolumeIDs to name volume-1", got)
	}
}

// TestComputeDeleteServerDeleteVolumesSendsDeleteAllVolume checks that
// --delete-volumes maps to DeleteVolumes true, sent as deleteAllVolume in
// the delete body.
func TestComputeDeleteServerDeleteVolumesSendsDeleteAllVolume(t *testing.T) {
	var deleteBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(computeServerJSON("server-1", "web-1", "ACTIVE")))
			case http.MethodDelete:
				defer func() { _ = r.Body.Close() }()
				deleteBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusAccepted)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/volumes/servers/server-1": jsonHandler(http.StatusOK, `{"data":[]}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"compute", "delete-server", "--server-id", "server-1", "--delete-volumes", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-server: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(deleteBody, &decoded); err != nil {
		t.Fatalf("delete body is not valid JSON: %v (%s)", err, deleteBody)
	}
	if decoded["deleteAllVolume"] != true {
		t.Fatalf("delete body = %+v, want deleteAllVolume true", decoded)
	}
	if !strings.Contains(stdout.String(), `"DeletedVolumeIDs"`) {
		t.Fatalf("stdout = %s, want DeletedVolumeIDs printed", stdout.String())
	}
}

// --- start-server, stop-server, reboot-server ---

// computeToggleFixture serves GetServer for one server whose status starts
// at fromStatus and flips to toStatus the instant its start/stop/reboot PUT
// runs, so the toggle's own post-write wait settles on its very first read,
// with no real sleep, mirroring monitor's own monitorToggleFixture.
func computeToggleFixture(id, action, fromStatus, toStatus string) *svcFixture {
	status := fromStatus
	return newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/" + id: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(computeServerJSON(id, "web-1", status)))
		},
		"/v2/proj-1/servers/" + id + "/" + action: func(w http.ResponseWriter, _ *http.Request) {
			status = toStatus
			w.WriteHeader(http.StatusAccepted)
		},
	})
}

// TestComputeStartServerTogglesAndConfirms drives a STOPPED server through
// start-server end to end: Changed true and the settled server printed, no
// --yes required (start is a Write but not Destructive).
func TestComputeStartServerTogglesAndConfirms(t *testing.T) {
	fixture := computeToggleFixture("server-1", "start", "STOPPED", "ACTIVE")
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "start-server", "--server-id", "server-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("start-server: %v (stderr=%s)", err, stderr.String())
	}
	var out struct {
		Server  compute.Server
		Changed bool
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if !out.Changed || out.Server.Status != "ACTIVE" {
		t.Fatalf("out = %+v, want Changed true and Status ACTIVE", out)
	}
}

// TestComputeStartServerAlreadyActiveSendsNoToggle checks that start-server
// on an already-ACTIVE server sends only the one read and reports Changed:
// false.
func TestComputeStartServerAlreadyActiveSendsNoToggle(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, computeServerJSON("server-1", "web-1", "ACTIVE")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "start-server", "--server-id", "server-1", "--query", "Changed",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("start-server: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read, no toggle)", n)
	}
	if got := stdout.String(); got != "false\n" {
		t.Fatalf("--query Changed = %q, want %q", got, "false\n")
	}
}

// TestComputeStartServerUnexpectedStatusSendsNoToggle checks the design's
// fail-closed rule: start-server on a server that is neither ACTIVE nor
// STOPPED refuses with error code UnexpectedStatus and sends no toggle.
func TestComputeStartServerUnexpectedStatusSendsNoToggle(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, computeServerJSON("server-1", "web-1", "CREATING")),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "start-server", "--server-id", "server-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an UnexpectedStatus refusal")
	}
	if got := classify(err).Code; got != "UnexpectedStatus" {
		t.Fatalf("Code = %q, want UnexpectedStatus (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read, no toggle)", n)
	}
}

// TestComputeStopServerWithoutYesExitsWithZeroRequests checks the design's
// --yes rule: stop-server is Write and Destructive.
func TestComputeStopServerWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "stop-server", "--server-id", "server-1"})
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

// TestComputeStopServerWithYesToggles checks that --yes lets stop-server
// through to a real toggle.
func TestComputeStopServerWithYesToggles(t *testing.T) {
	fixture := computeToggleFixture("server-1", "stop", "ACTIVE", "STOPPED")
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"compute", "stop-server", "--server-id", "server-1", "--query", "Changed",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("stop-server: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); got != "true\n" {
		t.Fatalf("--query Changed = %q, want %q", got, "true\n")
	}
}

// TestComputeRebootServerWithoutYesExitsWithZeroRequests checks the
// design's --yes rule: reboot-server is Write and Destructive.
func TestComputeRebootServerWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "reboot-server", "--server-id", "server-1"})
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

// TestComputeRebootServerWithYesAndNoWaitSendsToggle checks that --yes with
// --no-wait sends exactly the read and the reboot PUT, skipping the
// real-time 10-second confirm wait reboot's own settle condition needs.
func TestComputeRebootServerWithYesAndNoWaitSendsToggle(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, computeServerJSON("server-1", "web-1", "ACTIVE")),
		"/v2/proj-1/servers/server-1/reboot": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"compute", "reboot-server", "--server-id", "server-1", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("reboot-server: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (one read, one reboot)", n)
	}
}

// --- rename-server ---

// TestComputeRenameServerSendsNewNameNoYesNeeded checks that rename-server
// maps --name to newName and needs no --yes: it is Write but not
// Destructive.
func TestComputeRenameServerSendsNewNameNoYesNeeded(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/server-1/rename": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(computeServerJSON("server-1", "web-2", "ACTIVE")))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"compute", "rename-server", "--server-id", "server-1", "--name", "web-2",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("rename-server: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["newName"] != "web-2" {
		t.Fatalf("body = %+v, want newName web-2", decoded)
	}
	if !strings.Contains(stdout.String(), `"Name": "web-2"`) {
		t.Fatalf("stdout = %s, want the renamed server printed", stdout.String())
	}
}

func TestComputeCreateServerLoginInvalidInput(t *testing.T) {
	const secret = "#!/bin/sh\necho super-secret-token"
	path := filepath.Join(t.TempDir(), "user-data.sh")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		args    []string
		message string
	}{
		{"both", append(append([]string(nil), validCreateServerArgs...), "--user-data-file", path),
			"GreenNode refuses user data together with an SSH key; put the key in the cloud-config"},
		{"neither", createServerArgsWithoutSSHKey(),
			"login is required: an SSH key, or user data that installs keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSvcFixture(nil)
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "--debug"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, vngcloud.ErrInvalidInput) || exitCode(err) != 2 {
				t.Fatalf("error = %v, exitCode = %d, want invalid input and 2", err, exitCode(err))
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, want SDK message %q", err, tc.message)
			}
			printError(stderr, err)
			for _, output := range []string{err.Error(), stdout.String(), stderr.String()} {
				if strings.Contains(output, "super-secret-token") {
					t.Fatal("user data leaked in error or output")
				}
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}
