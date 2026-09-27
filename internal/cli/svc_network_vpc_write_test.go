package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// vpcFields builds one VPC's fields the way CreateVPC, UpdateVPC, and
// GetVPC's own response shape uses them: GetVPC returns them at the top
// level, and CreateVPC and UpdateVPC's read-after-write reuse the same GET.
func vpcFields(name, dnsStatus string) map[string]any {
	return map[string]any{
		"id": "vpc-1", "displayName": name, "status": "ACTIVE", "cidr": "10.0.0.0/24",
		"serverCount": 0, "volumeCount": 0, "dnsStatus": dnsStatus,
	}
}

func vpcJSON(name, dnsStatus string) string {
	b, err := json.Marshal(vpcFields(name, dnsStatus))
	if err != nil {
		panic(err)
	}
	return string(b)
}

// vpcCreateJSON builds CreateVPC's own response envelope: the VPC wrapped
// in a "data" field.
func vpcCreateJSON(name string) string {
	b, err := json.Marshal(map[string]any{"data": vpcFields(name, "")})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// subnetFields builds one subnet's fields the way GetSubnet's own response
// shape uses them: at the top level, no "data" wrapper.
func subnetFields(name string) map[string]any {
	return map[string]any{"uuid": "subnet-1", "name": name, "cidr": "10.0.1.0/24", "status": "ACTIVE"}
}

func subnetJSON(name string) string {
	b, err := json.Marshal(subnetFields(name))
	if err != nil {
		panic(err)
	}
	return string(b)
}

// subnetCreateJSON builds CreateSubnet's own response envelope: the subnet
// wrapped in a "data" field.
func subnetCreateJSON(name string) string {
	b, err := json.Marshal(map[string]any{"data": subnetFields(name)})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// emptyNetworkListJSON is ListNetworkInterfaces' and ListVirtualIPAddresses'
// own empty-page envelope, the shape DeleteSubnet's in-use guard reads when
// neither list holds anything for the subnet.
const emptyNetworkListJSON = `{"listData":[],"page":0,"pageSize":0,"totalPage":0,"totalItem":0}`

// --- create-vpc ---

// TestNetworkCreateVPCEndToEnd checks the flag-to-body mapping: --name and
// --cidr become the POST body's only two fields, with no zoneId key at all
// (the design's CreateVPC never sends one), and that the settled VPC comes
// back on stdout.
func TestNetworkCreateVPCEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(vpcCreateJSON("my-vpc")))
		},
		"/v2/proj-1/networks/vpc-1": jsonHandler(http.StatusOK, vpcJSON("my-vpc", "DISABLED")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-vpc", "--name", "my-vpc", "--cidr", "10.0.0.0/24",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-vpc: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 2 || decoded["name"] != "my-vpc" || decoded["cidr"] != "10.0.0.0/24" {
		t.Fatalf("body = %s, want exactly name=my-vpc and cidr=10.0.0.0/24, no zoneId", body)
	}

	if got := stdout.String(); !strings.Contains(got, `"UUID": "vpc-1"`) || !strings.Contains(got, `"Status": "ACTIVE"`) {
		t.Fatalf("stdout = %s, want the settled ACTIVE VPC", got)
	}
}

// TestNetworkCreateVPCNoWaitSkipsConfirmRead checks that --no-wait sends
// only the create POST, with no confirm GET afterward.
func TestNetworkCreateVPCNoWaitSkipsConfirmRead(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks": jsonHandler(http.StatusOK, vpcCreateJSON("my-vpc")),
		"/v2/proj-1/networks/vpc-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-vpc", "--name", "my-vpc", "--cidr", "10.0.0.0/24", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-vpc: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (--no-wait must send no confirm read)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"UUID": "vpc-1"`) {
		t.Fatalf("stdout = %s, want the mapped create response", got)
	}
}

// TestNetworkCreateVPCCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests
// checks --cli-input-json's strictness: a key that names no Input field is
// refused before any request.
func TestNetworkCreateVPCCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-vpc", "--name", "my-vpc", "--cidr", "10.0.0.0/24",
		"--cli-input-json", `{"Bogus":"x"}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- update-vpc ---

// TestNetworkUpdateVPCEndToEnd checks the PATCH body (name only) and that
// the read-after-write's updated name comes back on stdout.
func TestNetworkUpdateVPCEndToEnd(t *testing.T) {
	var body []byte
	reads := 0
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPatch:
				defer func() { _ = r.Body.Close() }()
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			case http.MethodGet:
				reads++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(vpcJSON("new-name", "DISABLED")))
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "update-vpc", "--vpc-id", "vpc-1", "--name", "new-name",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-vpc: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 1 || decoded["name"] != "new-name" {
		t.Fatalf("body = %s, want exactly name=new-name", body)
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1 (the read-after-write)", reads)
	}
	if got := stdout.String(); !strings.Contains(got, `"Name": "new-name"`) {
		t.Fatalf("stdout = %s, want the renamed VPC", got)
	}
}

// --- delete-vpc ---

func TestNetworkDeleteVPCRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-vpc", "--vpc-id", "vpc-1",
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

// TestNetworkDeleteVPCWithYesSendsGetListDeleteAndWaits checks the success
// path's exact request sequence: the pre-delete VPC read, the subnet-list
// guard, the DELETE, then the post-delete wait's own read settling to 404.
func TestNetworkDeleteVPCWithYesSendsGetListDeleteAndWaits(t *testing.T) {
	getCalls := 0
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls++
				if getCalls == 1 {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(vpcJSON("my-vpc", "DISABLED")))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/networks/vpc-1/subnets": jsonHandler(http.StatusOK, `[]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-vpc", "--vpc-id", "vpc-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-vpc: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 4 {
		t.Fatalf("requestCount = %d, want 4 (vpc read, subnet list, delete, post-delete read)", n)
	}
}

// TestNetworkDeleteVPCInUseWithSubnetsNoDelete checks that a VPC still
// listing a subnet stops the delete before any DELETE, with error code
// ResourceInUse.
func TestNetworkDeleteVPCInUseWithSubnetsNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(vpcJSON("my-vpc", "DISABLED")))
		},
		"/v2/proj-1/networks/vpc-1/subnets": jsonHandler(http.StatusOK, `[{"uuid":"subnet-1","name":"web","status":"ACTIVE"}]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-vpc", "--vpc-id", "vpc-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (vpc read, subnet list, no delete)", n)
	}
}

// --- enable-vpc-private-dns ---

func TestNetworkEnableVPCPrivateDNSRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "enable-vpc-private-dns", "--vpc-id", "vpc-1",
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

// TestNetworkEnableVPCPrivateDNSWithYesSendsPatchAndWaits checks the success
// path: a DISABLED pre-read, the enable PATCH, and the wait's own read
// settling to ENABLED.
func TestNetworkEnableVPCPrivateDNSWithYesSendsPatchAndWaits(t *testing.T) {
	getCalls := 0
	patched := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": func(w http.ResponseWriter, r *http.Request) {
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			if getCalls == 1 {
				_, _ = w.Write([]byte(vpcJSON("my-vpc", "DISABLED")))
				return
			}
			_, _ = w.Write([]byte(vpcJSON("my-vpc", "ENABLED")))
		},
		"/v2/proj-1/networks/vpc-1/enableDns": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch {
				t.Fatalf("method = %s, want PATCH", r.Method)
			}
			patched = true
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "enable-vpc-private-dns", "--vpc-id", "vpc-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("enable-vpc-private-dns: %v (stderr=%s)", err, stderr.String())
	}
	if !patched {
		t.Fatal("the enable PATCH was never sent")
	}
	if got := stdout.String(); !strings.Contains(got, `"DNSStatus": "ENABLED"`) || !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want dnsStatus ENABLED and Changed true", got)
	}
}

// TestNetworkEnableVPCPrivateDNSAlreadyEnabledSendsNoPatch checks that a
// pre-read already showing ENABLED sends no PATCH and reports Changed
// false, even though --yes is still required.
func TestNetworkEnableVPCPrivateDNSAlreadyEnabledSendsNoPatch(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": jsonHandler(http.StatusOK, vpcJSON("my-vpc", "ENABLED")),
		"/v2/proj-1/networks/vpc-1/enableDns": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "enable-vpc-private-dns", "--vpc-id", "vpc-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("enable-vpc-private-dns: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-read only)", n)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", got)
	}
}

// TestNetworkEnableVPCPrivateDNSUnexpectedStatusNoPatch checks that a
// dnsStatus the SDK does not recognize fails closed with error code
// UnexpectedStatus and sends no PATCH.
func TestNetworkEnableVPCPrivateDNSUnexpectedStatusNoPatch(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1": jsonHandler(http.StatusOK, vpcJSON("my-vpc", "WEIRD")),
		"/v2/proj-1/networks/vpc-1/enableDns": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "enable-vpc-private-dns", "--vpc-id", "vpc-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "UnexpectedStatus" {
		t.Fatalf("Code = %q, want UnexpectedStatus (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-read only)", n)
	}
}

// --- create-subnet ---

// TestNetworkCreateSubnetEndToEnd checks the flag-to-body mapping: --name,
// --cidr, and --zone-id (sent as zoneId) become the POST body's only three
// fields, with no secondarySubnetRequests key.
func TestNetworkCreateSubnetEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1/subnets": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(subnetCreateJSON("web")))
		},
		"/v2/proj-1/networks/vpc-1/subnets/subnet-1": jsonHandler(http.StatusOK, subnetJSON("web")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-subnet", "--vpc-id", "vpc-1", "--zone-id", "zone-1",
		"--name", "web", "--cidr", "10.0.1.0/24",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-subnet: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 3 || decoded["name"] != "web" || decoded["cidr"] != "10.0.1.0/24" || decoded["zoneId"] != "zone-1" {
		t.Fatalf("body = %s, want exactly name, cidr, and zoneId, no secondarySubnetRequests", body)
	}
	if got := stdout.String(); !strings.Contains(got, `"UUID": "subnet-1"`) {
		t.Fatalf("stdout = %s, want the settled subnet", got)
	}
}

// TestNetworkCreateSubnetMissingZoneIDIsUsageErrorWithZeroRequests checks
// the design's own requirement that create-subnet always needs --zone-id.
func TestNetworkCreateSubnetMissingZoneIDIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1/subnets": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-subnet", "--vpc-id", "vpc-1", "--name", "web", "--cidr", "10.0.1.0/24",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --zone-id")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- update-subnet ---

func TestNetworkUpdateSubnetEndToEnd(t *testing.T) {
	var body []byte
	reads := 0
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1/subnets/subnet-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				reads++
				w.Header().Set("Content-Type", "application/json")
				if reads == 1 {
					_, _ = w.Write([]byte(subnetJSON("old")))
					return
				}
				_, _ = w.Write([]byte(subnetJSON("new")))
			case http.MethodPatch:
				defer func() { _ = r.Body.Close() }()
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "update-subnet", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1", "--name", "new",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-subnet: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if len(decoded) != 1 || decoded["name"] != "new" {
		t.Fatalf("body = %s, want exactly name=new", body)
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want 2 (pre-read and read-after-write)", reads)
	}
	if got := stdout.String(); !strings.Contains(got, `"Name": "new"`) {
		t.Fatalf("stdout = %s, want the renamed subnet", got)
	}
}

// --- delete-subnet ---

func TestNetworkDeleteSubnetRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1/subnets/subnet-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-subnet", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1",
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

// TestNetworkDeleteSubnetWithYesSendsChecksDeleteAndWaits checks the
// success path's full request sequence: the pre-delete subnet read, the
// three in-use checks, the DELETE, then the post-delete wait's own
// ListSubnetsByVPC settling to an empty list.
func TestNetworkDeleteSubnetWithYesSendsChecksDeleteAndWaits(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1/subnets/subnet-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(subnetJSON("web")))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/servers/subnets/subnet-1":   jsonHandler(http.StatusOK, `{"data":[]}`),
		"/v2/proj-1/network-interfaces-elastic": jsonHandler(http.StatusOK, emptyNetworkListJSON),
		"/v2/proj-1/virtualIpAddress":           jsonHandler(http.StatusOK, emptyNetworkListJSON),
		"/v2/proj-1/networks/vpc-1/subnets":     jsonHandler(http.StatusOK, `[]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-subnet", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-subnet: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 6 {
		t.Fatalf("requestCount = %d, want 6 (subnet read, 3 in-use checks, delete, post-delete list)", n)
	}
}

// TestNetworkDeleteSubnetInUseWithServerNoDelete checks that a subnet with
// an attached server stops the delete before any DELETE, with error code
// ResourceInUse.
func TestNetworkDeleteSubnetInUseWithServerNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/networks/vpc-1/subnets/subnet-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want only GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(subnetJSON("web")))
		},
		"/v2/proj-1/servers/subnets/subnet-1": jsonHandler(http.StatusOK, `{"data":[{"uuid":"server-1","name":"server-1"}]}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-subnet", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (subnet read, servers check, no delete)", n)
	}
}

// --- list-servers-by-subnet ---

// TestNetworkListServersBySubnetEndToEnd checks the flag mapping for a
// plain read: --subnet-id becomes the path segment, and needs no --yes.
func TestNetworkListServersBySubnetEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/subnets/subnet-1": jsonHandler(http.StatusOK, `{"data":[{"uuid":"server-1","name":"web-1"}]}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "list-servers-by-subnet", "--subnet-id", "subnet-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-servers-by-subnet: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"UUID": "server-1"`) {
		t.Fatalf("stdout = %s, want the listed server", got)
	}
}

// --- read-only ---

// TestNetworkVPCAndSubnetWritesReadOnlyRefusedWithZeroRequests checks that
// read-only refuses every VPC and subnet write command before any request,
// the same way it already does for security group writes.
func TestNetworkVPCAndSubnetWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-vpc", []string{"create-vpc", "--name", "v", "--cidr", "10.0.0.0/24"}},
		{"update-vpc", []string{"update-vpc", "--vpc-id", "vpc-1", "--name", "v2"}},
		{"delete-vpc", []string{"delete-vpc", "--vpc-id", "vpc-1", "--yes"}},
		{"enable-vpc-private-dns", []string{"enable-vpc-private-dns", "--vpc-id", "vpc-1", "--yes"}},
		{"create-subnet", []string{"create-subnet", "--vpc-id", "vpc-1", "--zone-id", "zone-1", "--name", "s", "--cidr", "10.0.1.0/24"}},
		{"update-subnet", []string{"update-subnet", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1", "--name", "s2"}},
		{"delete-subnet", []string{"delete-subnet", "--vpc-id", "vpc-1", "--subnet-id", "subnet-1", "--yes"}},
	}
	unexpected := func(t *testing.T) func(http.ResponseWriter, *http.Request) {
		return func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/networks":                        unexpected(t),
				"/v2/proj-1/networks/vpc-1":                  unexpected(t),
				"/v2/proj-1/networks/vpc-1/subnets":          unexpected(t),
				"/v2/proj-1/networks/vpc-1/subnets/subnet-1": unexpected(t),
				"/v2/proj-1/networks/vpc-1/enableDns":        unexpected(t),
				"/v2/proj-1/servers/subnets/subnet-1":        unexpected(t),
				"/v2/proj-1/network-interfaces-elastic":      unexpected(t),
				"/v2/proj-1/virtualIpAddress":                unexpected(t),
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
			root.SetArgs(append([]string{"--profile", "agent", "network"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly", got)
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
