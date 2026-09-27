package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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
	if !strings.Contains(err.Error(), "subnets") {
		t.Fatalf("err = %v, want a hint to list the VPC's subnets before creating again", err)
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

	if _, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteSubnet() error = %v, want success since the subnet is absent from the list", err)
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
