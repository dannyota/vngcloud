package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// vpcCIDR is the CIDR every VPC write test in this file uses.
const vpcCIDR = "10.20.0.0/24"

// vpcBody builds a {"data": {...}} VPC envelope matching CreateVPC,
// UpdateVPC, and EnableVPCPrivateDNS's response shape, always for "vpc1":
// every test using it exercises the create and wait paths, which never
// vary the name.
func vpcBody(status string) string {
	return `{"data":` + vpcGetBody("vpc1", status) + `}`
}

// vpcGetBody builds a VPC envelope with its fields at the top level,
// matching GetVPC's response shape. dnsStatus is always DISABLED: these
// tests exercise CreateVPC, UpdateVPC, and their waits, none of which read
// it; EnableVPCPrivateDNS's own tests build their VPC bodies directly.
func vpcGetBody(name, status string) string {
	return `{"id":"vpc-1","displayName":"` + name + `","cidr":"` + vpcCIDR + `","status":"` + status + `","dnsStatus":"DISABLED"}`
}

// --- CreateVPC ---

func TestCreateVPCRequestBodyNeverSendsZoneID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/networks" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		if _, ok := body["zoneId"]; ok {
			t.Fatalf("body = %+v, must never send zoneId", body)
		}
		if body["name"] != "vpc1" || body["cidr"] != "10.20.0.0/24" {
			t.Fatalf("body = %+v", body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vpcBody("ACTIVE")))
	}))

	out, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24", NoWait: true})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if out.VPC.UUID != "vpc-1" {
		t.Fatalf("UUID = %q, want vpc-1", out.VPC.UUID)
	}
}

func TestCreateVPCDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		testutil.WriteFixture(t, w, "../testdata/network/create_vpc.json")
	}))

	out, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "<name>", CIDR: "10.20.0.0/24", NoWait: true})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if out.VPC.UUID != "vpc-1" || out.VPC.Name != "<name>" {
		t.Fatalf("unexpected VPC: %+v", out.VPC)
	}
}

func TestCreateVPCRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateVPC(context.Background(), &CreateVPCInput{CIDR: "10.0.0.0/24"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateVPCCIDRShapeRefusals(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []string{
		"10.20.1.0/16", // host bits set
		"not-a-cidr",
		"2001:db8::/32", // IPv6
		"10.20.0.0",     // no prefix length
	}
	for _, cidr := range cases {
		if _, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: cidr}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("CIDR %q: err = %v, want ErrInvalidInput", cidr, err)
		}
	}
}

func TestCreateVPCNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24", NoWait: true})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list vpcs") {
		t.Fatalf("err = %v, want a hint to list vpcs before creating again", err)
	}
}

func TestCreateVPCWaitSettlesToActive(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, scriptedResponses(t, []string{
		vpcGetBody("vpc1", "CREATING"),
		vpcGetBody("vpc1", "ACTIVE"),
	}, func(w http.ResponseWriter, r *http.Request) bool {
		getCalls.Add(1)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vpcBody("CREATING")))
			return true
		}
		return false
	})))

	out, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24"})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if out.VPC.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.VPC.Status)
	}
}

func TestCreateVPCWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedResponses(t, []string{
		vpcGetBody("vpc1", "ERROR"),
	}, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vpcBody("CREATING")))
			return true
		}
		return false
	})))

	out, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.VPC.UUID != "vpc-1" {
		t.Fatalf("Output = %+v, want the last read VPC", out)
	}
}

func TestCreateVPCWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vpcBody("CREATING")))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vpcGetBody("vpc1", "CREATING")))
	})))

	_, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestCreateVPCNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"displayName":"vpc1","cidr":"10.20.0.0/24"}}`))
	}))

	_, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24", NoWait: true})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list vpcs") {
		t.Fatalf("err = %v, want an APIError naming list vpcs before creating again", err)
	}
}

func TestCreateVPCWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vpcBody("CREATING")))
			return
		}
		if getCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vpcGetBody("vpc1", "ACTIVE")))
	})))

	out, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24"})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if out.VPC.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.VPC.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateVPCNoWaitSkipsWait(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vpcBody("CREATING")))
			return
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vpcGetBody("vpc1", "CREATING")))
	})))

	out, err := c.CreateVPC(context.Background(), &CreateVPCInput{Name: "vpc1", CIDR: "10.20.0.0/24", NoWait: true})
	if err != nil {
		t.Fatalf("CreateVPC() error = %v", err)
	}
	if out.VPC.Status != "CREATING" {
		t.Fatalf("Status = %q, want CREATING: NoWait must return the create response unwaited", out.VPC.Status)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GET calls = %d, want 0: NoWait must skip the post-create wait", getCalls.Load())
	}
}

// TestWaitVPCActivePollParameters checks the literal interval and bound
// waitVPCActive passes to poll, so that swapping the 2-second interval or
// the 3-minute bound with another wait's values fails this test: the
// handler never settles, so the wait always runs to its bound.
func TestWaitVPCActivePollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vpcGetBody("vpc1", "CREATING")))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitVPCActive(context.Background(), "op", "vpc-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 90 {
		t.Fatalf("sleep calls = %d, want 90 (a 2s interval over a 3-minute bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Fatalf("sleep duration = %s, want 2s", d)
		}
	}
}

// --- UpdateVPC ---

func TestUpdateVPCRequestBody(t *testing.T) {
	getCount := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			if r.URL.Path != "/v2/project-1/networks/vpc-1" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if body["name"] != "renamed" {
				t.Fatalf("body = %+v, want name=renamed", body)
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCount++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vpcGetBody("renamed", "ACTIVE")))
		}
	}))

	out, err := c.UpdateVPC(context.Background(), &UpdateVPCInput{VPCID: "vpc-1", Name: "renamed"})
	if err != nil {
		t.Fatalf("UpdateVPC() error = %v", err)
	}
	if out.VPC.Name != "renamed" {
		t.Fatalf("Name = %q, want renamed", out.VPC.Name)
	}
	if getCount != 1 {
		t.Fatalf("GET calls = %d, want 1 confirm read", getCount)
	}
}

func TestUpdateVPCRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.UpdateVPC(context.Background(), &UpdateVPCInput{VPCID: "vpc-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateVPCPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.UpdateVPC(context.Background(), &UpdateVPCInput{VPCID: id, Name: "renamed"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestUpdateVPCConfirmReadFailureIsNotSettled(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))

	out, err := c.UpdateVPC(context.Background(), &UpdateVPCInput{VPCID: "vpc-1", Name: "renamed"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.VPC.Name != "renamed" {
		t.Fatalf("Output = %+v, want the fallback with the new name", out)
	}
}

// --- DeleteVPC ---

func TestDeleteVPCGuardServerCount(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","serverCount":2}`))
	}))

	_, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteVPCGuardVolumeCount(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","volumeCount":1}`))
	}))

	_, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteVPCGuardHasSubnets(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/project-1/networks/vpc-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"uuid":"sub-1","status":"ACTIVE"}]`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteVPCSendsDeleteWhenClear(t *testing.T) {
	var deleteCalled atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			deleteCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
		}
	}))

	if _, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteVPC() error = %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("DELETE was never sent")
	}
}

func TestDeleteVPCContainsSubnetIsInUse(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"Cannot delete this VPC because it contains the subnet."}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
		}
	}))

	_, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1", NoWait: true})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
	if !strings.Contains(err.Error(), "rerun is safe") {
		t.Fatalf("err = %v, want a message saying a rerun is safe", err)
	}
}

func TestDeleteVPCWaitSettlesTo404(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			getCalls++
			if getCalls <= 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	})))

	if _, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1"}); err != nil {
		t.Fatalf("DeleteVPC() error = %v", err)
	}
}

func TestDeleteVPCWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ERROR"}`))
		}
	})))

	_, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestDeleteVPCWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
		}
	})))

	_, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestDeleteVPCNoWaitSkipsWait(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			getCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
		}
	})))

	if _, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: "vpc-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteVPC() error = %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-delete read only): NoWait must skip the post-delete wait", getCalls)
	}
}

// TestWaitVPCDeletedPollParameters checks the literal interval and bound
// waitVPCDeleted passes to poll, so that swapping the 2-second interval or
// the 3-minute bound with another wait's values fails this test: the
// handler never returns 404, so the wait always runs to its bound.
func TestWaitVPCDeletedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if err := c.waitVPCDeleted(context.Background(), "op", "vpc-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 90 {
		t.Fatalf("sleep calls = %d, want 90 (a 2s interval over a 3-minute bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Fatalf("sleep duration = %s, want 2s", d)
		}
	}
}

func TestDeleteVPCPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.DeleteVPC(context.Background(), &DeleteVPCInput{VPCID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

// --- EnableVPCPrivateDNS ---

func TestEnableVPCPrivateDNSPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestEnableVPCPrivateDNSAlreadyEnabledSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"ENABLED"}`))
	}))

	out, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("EnableVPCPrivateDNS() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestEnableVPCPrivateDNSUnexpectedStatus(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"WEIRD"}`))
	}))

	_, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus", err)
	}
}

func TestEnableVPCPrivateDNSSendsPatchOnceAndWaits(t *testing.T) {
	var patchCalls atomic.Int64
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			patchCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls++
			status := "DISABLED"
			if getCalls > 1 {
				status = "ENABLED"
			}
			if getCalls > 2 {
				status = "ENABLED"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"` + status + `"}`))
		}
	})))

	out, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("EnableVPCPrivateDNS() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if patchCalls.Load() != 1 {
		t.Fatalf("PATCH calls = %d, want 1", patchCalls.Load())
	}
}

func TestEnableVPCPrivateDNSPatch502IsNotRetriedAndNotSettled(t *testing.T) {
	var patchCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			patchCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"DISABLED"}`))
		}
	})))

	_, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if patchCalls.Load() != 1 {
		t.Fatalf("PATCH calls = %d, want 1: Once must never be retried", patchCalls.Load())
	}
}

func TestEnableVPCPrivateDNSPatch400ReturnsAPIError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad request"}`))
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"DISABLED"}`))
		}
	})))

	_, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
	if errors.Is(err, ErrNotSettled) {
		t.Fatal("err wraps ErrNotSettled, want the raw APIError since the server never acted")
	}
}

func TestEnableVPCPrivateDNSEnablingWaitsWithoutSending(t *testing.T) {
	var patchCalls atomic.Int64
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			patchCalls.Add(1)
		case http.MethodGet:
			getCalls++
			status := "ENABLING"
			if getCalls > 1 {
				status = "ENABLED"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"` + status + `"}`))
		}
	})))

	out, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("EnableVPCPrivateDNS() error = %v", err)
	}
	if patchCalls.Load() != 0 {
		t.Fatalf("PATCH calls = %d, want 0: ENABLING must send nothing", patchCalls.Load())
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestEnableVPCPrivateDNSNoWaitReturnsAtOnce(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"DISABLED"}`))
		}
	}))

	out, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1", NoWait: true})
	if err != nil {
		t.Fatalf("EnableVPCPrivateDNS() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestEnableVPCPrivateDNSWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"ENABLING"}`))
		}
	})))

	_, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestWaitVPCPrivateDNSEnabledPollParameters checks the literal interval
// and bound waitVPCPrivateDNSEnabled passes to poll, so that swapping its
// 10-second interval or 10-minute bound with another wait's 2-second or
// 3-minute values fails this test: the handler never reaches ENABLED, so
// the wait always runs to its bound.
func TestWaitVPCPrivateDNSEnabledPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"ENABLING"}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitVPCPrivateDNSEnabled(context.Background(), "op", "vpc-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 60 {
		t.Fatalf("sleep calls = %d, want 60 (a 10s interval over a 10-minute bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 10*time.Second {
			t.Fatalf("sleep duration = %s, want 10s", d)
		}
	}
}

func TestEnableVPCPrivateDNSStopsOnUnknownStatusMidWait(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			t.Fatal("no PATCH expected: ENABLING already started")
		case http.MethodGet:
			getCalls++
			status := "ENABLING"
			if getCalls > 1 {
				status = "SUSPENDED"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"` + status + `"}`))
		}
	})))

	_, err := c.EnableVPCPrivateDNS(context.Background(), &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus", err)
	}
	if getCalls != 2 {
		t.Fatalf("GET calls = %d, want 2: the wait must stop at once on an unrecognized dnsStatus rather than poll toward the bound", getCalls)
	}
}

func TestEnableVPCPrivateDNSCanceledContext(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE","dnsStatus":"DISABLED"}`))
		}
	})))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.EnableVPCPrivateDNS(ctx, &EnableVPCPrivateDNSInput{VPCID: "vpc-1"})
	if err == nil {
		t.Fatal("err = nil, want an error from the canceled wait")
	}
}

// scriptedResponses serves each body in bodies in order to successive GET
// requests, and delegates any other request (a POST, say) to onOther, which
// reports whether it fully handled the request. Once bodies is exhausted,
// the last body keeps being served, so a test does not need to size the
// script exactly to the number of poll iterations the wait bound allows.
func scriptedResponses(t *testing.T, bodies []string, onOther func(w http.ResponseWriter, r *http.Request) bool) http.HandlerFunc {
	t.Helper()
	var calls atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		if onOther != nil && onOther(w, r) {
			return
		}
		i := int(calls.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bodies[i]))
	}
}
