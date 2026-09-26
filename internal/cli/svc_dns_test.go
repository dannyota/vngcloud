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
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/dns"
)

// exampleHostedZone is the same sanitized shape dns's own fixtures decode,
// reused here so the golden files below exercise a realistic HostedZone
// rather than an empty struct.
func exampleHostedZone(status string) dns.HostedZone {
	return dns.HostedZone{
		ID:               "hosted-zone-1",
		DomainName:       "app.internal",
		Status:           status,
		Description:      "app zone",
		Type:             "PRIVATE",
		CountRecords:     2,
		AssociatedVPCIDs: []string{"vpc-1"},
		AssocVPCMapRegion: []dns.VPCMapRegion{
			{VPCID: "vpc-1", Region: "hcm-3"},
		},
		PortalUserID: 1,
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 1, 2, 13, 0, 0, 0, time.UTC),
	}
}

// TestGoldenDNSCreateHostedZone checks create-hosted-zone's exact output
// shape, {"HostedZone": {...}}, the same shape get-hosted-zone uses.
func TestGoldenDNSCreateHostedZone(t *testing.T) {
	v := &dns.CreateHostedZoneOutput{HostedZone: exampleHostedZone(dns.StatusActive)}
	checkGolden(t, "dns-create-hosted-zone.json.golden", "json", "", v)
	checkGolden(t, "dns-create-hosted-zone.table.golden", "table", "", v)
	checkGolden(t, "dns-create-hosted-zone.text.golden", "text", "", v)
}

// TestGoldenDNSUpdateHostedZone checks update-hosted-zone's exact output
// shape, the same {"HostedZone": {...}} shape create-hosted-zone uses.
func TestGoldenDNSUpdateHostedZone(t *testing.T) {
	v := &dns.UpdateHostedZoneOutput{HostedZone: exampleHostedZone(dns.StatusActive)}
	checkGolden(t, "dns-update-hosted-zone.json.golden", "json", "", v)
	checkGolden(t, "dns-update-hosted-zone.table.golden", "table", "", v)
	checkGolden(t, "dns-update-hosted-zone.text.golden", "text", "", v)
}

// TestGoldenDNSDeleteHostedZone checks delete-hosted-zone's exact output
// shape: an empty object, since DeleteHostedZoneOutput carries no fields.
func TestGoldenDNSDeleteHostedZone(t *testing.T) {
	v := &dns.DeleteHostedZoneOutput{}
	checkGolden(t, "dns-delete-hosted-zone.json.golden", "json", "", v)
	checkGolden(t, "dns-delete-hosted-zone.table.golden", "table", "", v)
	checkGolden(t, "dns-delete-hosted-zone.text.golden", "text", "", v)
}

// zoneJSON builds a {"data": {...}} hosted zone envelope, the shape
// GetHostedZone and CreateHostedZone both decode, always under id "zone-1",
// the only id every test in this file uses.
func zoneJSON(status, description string, vpcIDs []string) string {
	body := map[string]any{
		"hostedZoneId": "zone-1",
		"domainName":   "app.internal",
		"status":       status,
		"type":         "PRIVATE",
		"description":  description,
		"assocVpcIds":  vpcIDs,
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestDNSCreateHostedZoneEndToEnd drives the real create-hosted-zone command
// against a fixture vDNS server whose very first confirm read already shows
// StatusActive, so the SDK's post-write wait settles at once and this test
// never really sleeps: it checks the POST body VPCIDs (--cli-input-json,
// since VPCIDs has no flag type) built, and that the settled zone comes back
// on stdout.
func TestDNSCreateHostedZoneEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON(dns.StatusCreating, "", []string{"vpc-1"})))
		},
		"/v1/dns/hosted-zone/zone-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON(dns.StatusActive, "", []string{"vpc-1"})))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "dns", "create-hosted-zone",
		"--domain-name", "app.internal",
		"--cli-input-json", `{"VPCIDs":["vpc-1"]}`,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-hosted-zone: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["domainName"] != "app.internal" || decoded["type"] != "PRIVATE" {
		t.Fatalf("body = %s, want domainName and type PRIVATE", body)
	}
	ids, _ := decoded["assocVpcIds"].([]any)
	if len(ids) != 1 || ids[0] != "vpc-1" {
		t.Fatalf("body[assocVpcIds] = %v, want [vpc-1]", decoded["assocVpcIds"])
	}

	var out struct{ HostedZone dns.HostedZone }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.HostedZone.Status != dns.StatusActive {
		t.Fatalf("HostedZone.Status = %q, want %q (the settled zone)", out.HostedZone.Status, dns.StatusActive)
	}
}

// TestDNSCreateHostedZoneWriteFailedPrintsOutputOnStdout drives a real
// create-hosted-zone call whose very first confirm read already shows
// StatusError, so the SDK's settleZone wait fails at once with no real
// sleep: this exercises the real dns.ErrFailed path (unlike ErrZoneBusy
// below, which the SDK's public API gives no way to reach quickly; see
// TestOpZoneBusyPrintsNoOutput for why that one is driven through a fake Op
// instead). TestDNSCreateHostedZoneNotSettledOnCanceledContext below covers
// the real dns.ErrNotSettled path.
func TestDNSCreateHostedZoneWriteFailedPrintsOutputOnStdout(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON(dns.StatusCreating, "", []string{"vpc-1"})))
		},
		"/v1/dns/hosted-zone/zone-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON(dns.StatusError, "", []string{"vpc-1"})))
		},
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "dns", "create-hosted-zone",
		"--domain-name", "app.internal",
		"--cli-input-json", `{"VPCIDs":["vpc-1"]}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "WriteFailed" {
		t.Fatalf("Code = %q, want WriteFailed", got)
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}

	// The CLI's own JSON uses Go field names ("ID"), not dns.HostedZone's API
	// tags ("hostedZoneId"), so a substring check is used instead of
	// unmarshaling into dns.HostedZone itself, whose tags would silently fail
	// to match those Go-named keys.
	got := stdout.String()
	if !strings.Contains(got, `"ID": "zone-1"`) || !strings.Contains(got, `"Status": "ERROR"`) {
		t.Fatalf("stdout = %s, want the ERROR zone with its id printed alongside the error", got)
	}
}

// TestDNSCreateHostedZoneNotSettledOnCanceledContext drives a real
// create-hosted-zone call whose POST succeeds and whose settle GET is
// interrupted by canceling the command's own context, mirroring a Ctrl-C
// during the post-write wait. The zone fixture answers the settle GET once,
// with the zone still CREATING (the write reached the server), then cancels
// the context the command runs under; the settle poll's next step then
// fails on that canceled context, not on a real 60-second bound.
//
// Per the vDNS design, that failure must still surface as an error wrapping
// dns.ErrNotSettled with the last zone the SDK read as a non-nil Output,
// not as the plain canceled-context path the CLI otherwise falls back to.
func TestDNSCreateHostedZoneNotSettledOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON(dns.StatusCreating, "", []string{"vpc-1"})))
		},
		"/v1/dns/hosted-zone/zone-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON(dns.StatusCreating, "", []string{"vpc-1"})))
			cancel()
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "dns", "create-hosted-zone",
		"--domain-name", "app.internal",
		"--cli-input-json", `{"VPCIDs":["vpc-1"]}`,
	})
	err := root.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotSettled" {
		t.Fatalf("Code = %q, want NotSettled, not the plain canceled-context path (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"ID": "zone-1"`) {
		t.Fatalf("stdout = %s, want the Output with the new zone ID", got)
	}
}

// TestDNSUpdateHostedZoneVPCIDsMerge checks the CLI's own contract point for
// VPCIDs: a missing key resends the zone's current VPCs unchanged, and an
// explicit empty list sends [] and detaches every VPC. Both cases use
// --no-wait so the fixture needs only one confirm read, matching
// UpdateHostedZoneInput.NoWait's own single-read behavior; the deeper
// merge and wait semantics are the dns package's own tests, not repeated
// here.
func TestDNSUpdateHostedZoneVPCIDsMerge(t *testing.T) {
	cases := []struct {
		name       string
		cliJSON    string
		wantVPCIDs []any
	}{
		{
			name:       "missing key keeps the current VPCs",
			cliJSON:    "",
			wantVPCIDs: []any{"vpc-1", "vpc-2"},
		},
		{
			name:       "explicit empty list detaches every VPC",
			cliJSON:    `{"VPCIDs":[]}`,
			wantVPCIDs: []any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/dns/hosted-zone/zone-1": func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(zoneJSON(dns.StatusActive, "old description", []string{"vpc-1", "vpc-2"})))
					case http.MethodPut:
						defer func() { _ = r.Body.Close() }()
						body, _ = io.ReadAll(r.Body)
						w.WriteHeader(http.StatusNoContent)
					default:
						t.Fatalf("unexpected method %s", r.Method)
					}
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			args := []string{
				"--region", "hcm-3", "dns", "update-hosted-zone",
				"--hosted-zone-id", "zone-1", "--no-wait",
			}
			if tc.cliJSON != "" {
				args = append(args, "--cli-input-json", tc.cliJSON)
			} else {
				args = append(args, "--description", "new description")
			}
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("update-hosted-zone: %v (stderr=%s)", err, stderr.String())
			}

			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("body is not valid JSON: %v (%s)", err, body)
			}
			ids, _ := decoded["assocVpcIds"].([]any)
			if len(ids) != len(tc.wantVPCIDs) {
				t.Fatalf("body[assocVpcIds] = %v, want %v", decoded["assocVpcIds"], tc.wantVPCIDs)
			}
			for i, want := range tc.wantVPCIDs {
				if ids[i] != want {
					t.Fatalf("body[assocVpcIds] = %v, want %v", decoded["assocVpcIds"], tc.wantVPCIDs)
				}
			}
		})
	}
}

// TestDNSDeleteHostedZoneRequiresYes checks the CLI design's --yes rule for
// delete-hosted-zone: it is Write and Destructive, so it fails with exit
// code 2 and sends no request unless --yes is given.
func TestDNSDeleteHostedZoneRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone/zone-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "dns", "delete-hosted-zone", "--hosted-zone-id", "zone-1"})
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

// TestDNSDeleteHostedZoneWithYesAndNoWait checks that --yes together with
// --no-wait sends exactly the pre-write read and the DELETE, with no confirm
// read afterward, per DeleteHostedZoneInput.NoWait.
func TestDNSDeleteHostedZoneWithYesAndNoWait(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone/zone-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				if deleted {
					t.Fatal("unexpected GET after DELETE: --no-wait must send no confirm read")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneJSON(dns.StatusActive, "d", []string{"vpc-1"})))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--yes", "dns", "delete-hosted-zone",
		"--hosted-zone-id", "zone-1", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-hosted-zone: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	// One pre-write GET (the zone was already ACTIVE) and the DELETE itself;
	// --no-wait must add no confirm read after it.
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2", n)
	}
}

// TestDNSWritesReadOnlyRefusedWithZeroRequests checks the vDNS design's
// read-only rule: create-hosted-zone, update-hosted-zone, and
// delete-hosted-zone are all Write operations, so a read-only profile
// refuses each with exit 2 before any request. delete-hosted-zone also
// passes --yes, so the read-only refusal is unambiguously the reason, not a
// missing --yes.
func TestDNSWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-hosted-zone", []string{"create-hosted-zone", "--domain-name", "app.internal", "--cli-input-json", `{"VPCIDs":["vpc-1"]}`}},
		{"update-hosted-zone", []string{"update-hosted-zone", "--hosted-zone-id", "zone-1", "--description", "new"}},
		{"delete-hosted-zone", []string{"delete-hosted-zone", "--hosted-zone-id", "zone-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/dns/hosted-zone": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v1/dns/hosted-zone/zone-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "dns"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

// fakeWaitInput and fakeWaitOutput back the fake Op method below: the dns
// package exposes no way to inject a fake clock from outside it (Client.sleep
// is unexported), and dns's own pre-write poll loop never wraps a context
// cancellation in ErrZoneBusy (a plain read failure or canceled sleep during
// that wait propagates unwrapped), so there is no way to reach that
// sentinel through a real dns.Client without the real 60-second bound
// elapsing. FakeZoneBusy returns it synthetically instead, to test the
// CLI's own handling of it (classify, exitCode, and the Output-on-stdout
// rule) rather than the SDK's own wait, which dns/zones_wait_test.go already
// covers. ErrNotSettled needs no such fake: canceling the context during a
// real settle GET reaches it directly, per
// TestDNSCreateHostedZoneNotSettledOnCanceledContext above.
type fakeWaitInput struct{ ID string }

type fakeWaitOutput struct {
	ID     string
	Status string
}

func (c *fakeClient) FakeZoneBusy(_ context.Context, in *fakeWaitInput) (*fakeWaitOutput, error) {
	return nil, fmt.Errorf("%w: %s did not leave CREATING", dns.ErrZoneBusy, in.ID)
}

// TestOpZoneBusyPrintsNoOutput checks that runOp prints nothing on stdout
// for dns.ErrZoneBusy: unlike ErrFailed and ErrNotSettled, a zone-busy
// refusal sends nothing, so there is no Output worth printing (the SDK
// returns a nil Output for it).
func TestOpZoneBusyPrintsNoOutput(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(Service(h.e, "fakewait", "fake wait errors for tests", h.newClient,
		Write[fakeClient, fakeWaitInput, fakeWaitOutput]("fake-zone-busy", (*fakeClient).FakeZoneBusy)))

	err := execCmd(t, root, []string{"fakewait", "fake-zone-busy", "--id", "zone-1"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ZoneBusy" {
		t.Fatalf("Code = %q, want ZoneBusy", got)
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := h.stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty: ZoneBusy prints no Output", got)
	}
}
