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
	"danny.vn/vngcloud/internal/testutil"
)

// subnetCIDR is the CIDR every subnet write test in this file uses.
const subnetCIDR = "10.20.0.0/24"

// subnetBody builds a {"data": {...}} subnet envelope matching
// CreateSubnet's response shape, always for "sub1": every test using it
// exercises the create and wait paths, which never vary the name.
func subnetBody(status string) string {
	return `{"data":` + subnetGetBody("sub1", status) + `}`
}

// subnetGetBody builds a subnet envelope with its fields at the top level,
// matching GetSubnet and ListSubnetsByVPC's response shape. It never sets
// secondarySubnets: the one test needing that builds its JSON directly.
func subnetGetBody(name, status string) string {
	return `{"uuid":"subnet-1","name":"` + name + `","networkUuid":"vpc-1","cidr":"` + subnetCIDR + `","status":"` + status + `"}`
}

// --- CreateSubnet ---

func TestCreateSubnetRequestBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/networks/vpc-1/subnets" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		if body["name"] != "sub1" || body["cidr"] != "10.20.0.0/24" || body["zoneId"] != "zone-a" {
			t.Fatalf("body = %+v", body)
		}
		if _, ok := body["secondarySubnetRequests"]; ok {
			t.Fatalf("body = %+v, must never send secondarySubnetRequests", body)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(subnetBody("ACTIVE")))
	}))

	out, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24", NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateSubnet() error = %v", err)
	}
	if out.Subnet.UUID != "subnet-1" {
		t.Fatalf("UUID = %q, want subnet-1", out.Subnet.UUID)
	}
}

func TestCreateSubnetDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		testutil.WriteFixture(t, w, "../testdata/network/create_subnet.json")
	}))

	out, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "<name>", CIDR: "10.20.0.0/24", NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateSubnet() error = %v", err)
	}
	if out.Subnet.UUID != "subnet-1" || out.Subnet.Name != "<name>" {
		t.Fatalf("unexpected subnet: %+v", out.Subnet)
	}
}

func TestCreateSubnetRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	base := CreateSubnetInput{VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24"}
	missingZone := base
	missingZone.ZoneID = ""
	if _, err := c.CreateSubnet(context.Background(), &missingZone); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("missing ZoneID: err = %v, want ErrInvalidInput", err)
	}
	missingName := base
	missingName.Name = ""
	if _, err := c.CreateSubnet(context.Background(), &missingName); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("missing Name: err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateSubnetCIDRShapeRefusals(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []string{"10.20.1.0/16", "not-a-cidr", "2001:db8::/32", "10.20.0.0"}
	for _, cidr := range cases {
		in := &CreateSubnetInput{VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: cidr}
		if _, err := c.CreateSubnet(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("CIDR %q: err = %v, want ErrInvalidInput", cidr, err)
		}
	}
}

func TestCreateSubnetPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b"} {
		in := &CreateSubnetInput{VPCID: id, ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24"}
		if _, err := c.CreateSubnet(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestCreateSubnetNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24", NoWait: true,
	})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "match by CIDR") {
		t.Fatalf("err = %v, want a hint to list the VPC's subnets and match by CIDR before creating again", err)
	}
}

func TestCreateSubnetNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"name":"sub1","cidr":"10.20.0.0/24"}}`))
	}))

	_, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24", NoWait: true,
	})
	if !strings.Contains(err.Error(), "match by CIDR") {
		t.Fatalf("err = %v, want a hint to list the VPC's subnets and match by CIDR", err)
	}
}

func TestCreateSubnetWaitSettlesToActive(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedResponses(t, []string{
		subnetGetBody("sub1", "CREATING"),
		subnetGetBody("sub1", "ACTIVE"),
	}, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(subnetBody("CREATING")))
			return true
		}
		return false
	})))

	out, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24",
	})
	if err != nil {
		t.Fatalf("CreateSubnet() error = %v", err)
	}
	if out.Subnet.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.Subnet.Status)
	}
}

func TestCreateSubnetWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedResponses(t, []string{
		subnetGetBody("sub1", "ERROR"),
	}, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(subnetBody("CREATING")))
			return true
		}
		return false
	})))

	_, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24",
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestCreateSubnetWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(subnetBody("CREATING")))
			return
		}
		if getCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(subnetGetBody("sub1", "ACTIVE")))
	})))

	out, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24",
	})
	if err != nil {
		t.Fatalf("CreateSubnet() error = %v", err)
	}
	if out.Subnet.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.Subnet.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateSubnetWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(subnetBody("CREATING")))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(subnetGetBody("sub1", "CREATING")))
	})))

	_, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24",
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestCreateSubnetNoWaitSkipsWait(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(subnetBody("CREATING")))
			return
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(subnetGetBody("sub1", "CREATING")))
	})))

	out, err := c.CreateSubnet(context.Background(), &CreateSubnetInput{
		VPCID: "vpc-1", ZoneID: "zone-a", Name: "sub1", CIDR: "10.20.0.0/24", NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateSubnet() error = %v", err)
	}
	if out.Subnet.Status != "CREATING" {
		t.Fatalf("Status = %q, want CREATING: NoWait must return the create response unwaited", out.Subnet.Status)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GET calls = %d, want 0: NoWait must skip the post-create wait", getCalls.Load())
	}
}

// TestWaitSubnetActivePollParameters checks the literal interval and bound
// waitSubnetActive passes to poll, so that swapping the 2-second interval
// or the 3-minute bound with another wait's values fails this test: the
// handler never settles, so the wait always runs to its bound.
func TestWaitSubnetActivePollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(subnetGetBody("sub1", "CREATING")))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitSubnetActive(context.Background(), "op", "vpc-1", "subnet-1"); !errors.Is(err, ErrNotSettled) {
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

// --- UpdateSubnet ---

func TestUpdateSubnetRequestBody(t *testing.T) {
	getCount := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCount++
			name := "old"
			if getCount > 1 {
				name = "renamed"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(subnetGetBody(name, "ACTIVE")))
		case http.MethodPatch:
			if r.URL.Path != "/v2/project-1/networks/vpc-1/subnets/subnet-1" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if body["name"] != "renamed" {
				t.Fatalf("body = %+v, want name=renamed", body)
			}
			w.WriteHeader(http.StatusOK)
		}
	}))

	out, err := c.UpdateSubnet(context.Background(), &UpdateSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", Name: "renamed"})
	if err != nil {
		t.Fatalf("UpdateSubnet() error = %v", err)
	}
	if out.Subnet.Name != "renamed" {
		t.Fatalf("Name = %q, want renamed", out.Subnet.Name)
	}
	if getCount != 2 {
		t.Fatalf("GET calls = %d, want 2 (pre-read and confirm read)", getCount)
	}
}

func TestUpdateSubnetRefusesWithSecondarySubnets(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"uuid":"subnet-1","name":"old","networkUuid":"vpc-1","cidr":"10.20.0.0/24","status":"ACTIVE","secondarySubnets":[{"uuid":"sub-2","name":"secondary","cidr":"10.20.1.0/28"}]}`))
	}))

	_, err := c.UpdateSubnet(context.Background(), &UpdateSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", Name: "renamed"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateSubnetRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.UpdateSubnet(context.Background(), &UpdateSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateSubnetPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.UpdateSubnet(context.Background(), &UpdateSubnetInput{VPCID: id, SubnetID: "subnet-1", Name: "renamed"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.UpdateSubnet(context.Background(), &UpdateSubnetInput{VPCID: "vpc-1", SubnetID: id, Name: "renamed"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("SubnetID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

// --- DeleteSubnet ---

func TestDeleteSubnetDeletedStatusIsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"DELETED"}`))
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestDeleteSubnetPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: id, SubnetID: "subnet-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("SubnetID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

// TestDeleteSubnetRefusesVPCMismatch checks that DeleteSubnet sends no
// DELETE when the subnet it reads names a different VPC than in.VPCID: the
// caller passed the wrong VPCID, and sending the DELETE anyway would act on
// a subnet outside the VPC the caller named.
func TestDeleteSubnetRefusesVPCMismatch(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected: a subnet in another VPC must send no DELETE")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"uuid":"subnet-1","networkUuid":"vpc-2","status":"ACTIVE"}`))
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteSubnetGuardServers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"uuid":"server-1","status":"ACTIVE"}]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteSubnetGuardInterfaces(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"iface-1","subnetUuid":"subnet-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteSubnetGuardVirtualIPs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"vip-1","subnetId":"subnet-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteSubnet5xxConfirmedByListAbsence(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("unexpected request: " + r.URL.Path)
		}
	}))

	out, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteSubnet() error = %v, want success since the subnet is absent from the list", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want a non-nil &DeleteSubnetOutput{}")
	}
}

func TestDeleteSubnet5xxConfirmedByListPresence(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"uuid":"subnet-1","status":"ACTIVE"}]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("unexpected request: " + r.URL.Path)
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true})
	if err == nil {
		t.Fatal("err = nil, want the original 500 since the subnet is still listed")
	}
}

func TestDeleteSubnetWaitSettlesByListAbsence(t *testing.T) {
	listCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			listCalls++
			w.WriteHeader(http.StatusOK)
			if listCalls <= 1 {
				_, _ = w.Write([]byte(`[{"uuid":"subnet-1","status":"ACTIVE"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		}
	})))

	if _, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"}); err != nil {
		t.Fatalf("DeleteSubnet() error = %v", err)
	}
}

func TestDeleteSubnetWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"uuid":"subnet-1","status":"ERROR"}]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		}
	})))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestDeleteSubnetWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"uuid":"subnet-1","status":"ACTIVE"}]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		}
	})))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestDeleteSubnetNoWaitSkipsWait(t *testing.T) {
	listCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			listCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"uuid":"subnet-1","status":"ACTIVE"}]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		}
	})))

	if _, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteSubnet() error = %v", err)
	}
	if listCalls != 0 {
		t.Fatalf("ListSubnetsByVPC calls = %d, want 0: NoWait must skip the post-delete wait", listCalls)
	}
}

// TestWaitSubnetDeletedPollParameters checks the literal interval and
// bound waitSubnetDeleted passes to poll, so that swapping the 2-second
// interval or the 3-minute bound with another wait's values fails this
// test: the handler always lists the subnet as still present, so the wait
// always runs to its bound.
func TestWaitSubnetDeletedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"uuid":"subnet-1","status":"ACTIVE"}]`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if err := c.waitSubnetDeleted(context.Background(), "op", "vpc-1", "subnet-1"); !errors.Is(err, ErrNotSettled) {
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

func TestDeleteSubnetRefusedWhenNetworkACLHoldsIt(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","name":"acl-name","networkId":"vpc-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatalf("unexpected request: %s %s: a subnet a network ACL still holds must send no DELETE", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteSubnetProceedsWhenNoNetworkACLHoldsIt(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			// One ACL exists in this VPC, but it does not list this subnet.
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","name":"acl-name","networkId":"vpc-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-9"})))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	out, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteSubnet() error = %v, want success: no network ACL in this VPC holds this subnet", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want a non-nil &DeleteSubnetOutput{}")
	}
}

// TestDeleteSubnetRefusedWhenEmptyNetworkIDACLHoldsIt checks the live case
// where ListNetworkACLs answers an ACL's NetworkID as empty even though the
// ACL does belong to this VPC and holds this subnet: listNetworkACLsInVPC
// must still surface it for a detail read, and checkSubnetNotHeldByNetworkACL
// must still refuse the delete.
func TestDeleteSubnetRefusedWhenEmptyNetworkIDACLHoldsIt(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			// networkId comes back empty, as seen live for an ACL that does
			// belong to this VPC.
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","name":"acl-name","networkId":""}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			// The detail read carries VPCID (interfaceNetworkUuid) matching
			// this VPC, and lists the subnet.
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatalf("unexpected request: %s %s: a subnet an empty-NetworkID ACL holds must send no DELETE", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

// TestDeleteSubnetProceedsWhenEmptyNetworkIDACLBelongsToAnotherVPC checks
// that an ACL listed with an empty NetworkID, but whose own detail names a
// different VPC, never blocks a delete in this VPC, even if it happens to
// list this subnet id.
func TestDeleteSubnetProceedsWhenEmptyNetworkIDACLBelongsToAnotherVPC(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","name":"acl-name","networkId":""}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"uuid":"acl-1","status":"ACTIVE","defaultAcl":false,"interfaceNetworkUuid":"vpc-2","subnetAssociationList":["subnet-1"]}}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	out, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteSubnet() error = %v, want success: the empty-NetworkID ACL's own detail names a different VPC", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want a non-nil &DeleteSubnetOutput{}")
	}
}

// TestDeleteSubnetProceedsWhenNetworkACLVanishesBetweenListAndRead checks
// the live race where an ACL the list just returned is deleted before its
// own detail read runs: that read then answers 404, and
// checkSubnetNotHeldByNetworkACL must skip the ACL rather than fail closed,
// since a gone ACL cannot hold the subnet.
func TestDeleteSubnetProceedsWhenNetworkACLVanishesBetweenListAndRead(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","name":"acl-name","networkId":"vpc-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	out, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteSubnet() error = %v, want success: a 404 on the ACL detail read means it cannot hold the subnet", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want a non-nil &DeleteSubnetOutput{}")
	}
}

func TestDeleteSubnetNetworkACLListFailureRefuses(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected: a failed network ACL list must refuse the delete rather than proceed")
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if err == nil {
		t.Fatal("err = nil, want an error: a failed network ACL list must fail closed")
	}
}

func TestDeleteSubnetNetworkACLGetFailureRefuses(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/servers/subnets/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "network-interfaces-elastic"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case strings.HasSuffix(r.URL.Path, "virtualIpAddress"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","name":"acl-name","networkId":"vpc-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE"}`))
		default:
			t.Fatal("no write expected: a failed network ACL read must refuse the delete rather than proceed")
		}
	}))

	_, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
	if err == nil {
		t.Fatal("err = nil, want an error: a failed network ACL read must fail closed")
	}
}

// --- ListServersBySubnet ---

func TestListServersBySubnet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers/subnets/subnet-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_servers_by_subnet.json")
	}))

	out, err := c.ListServersBySubnet(context.Background(), &ListServersBySubnetInput{SubnetID: "subnet-1"})
	if err != nil {
		t.Fatalf("ListServersBySubnet() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "<name>" {
		t.Fatalf("unexpected servers: %+v", out.Items)
	}
}

func TestListServersBySubnetPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.ListServersBySubnet(context.Background(), &ListServersBySubnetInput{SubnetID: "a/b"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
