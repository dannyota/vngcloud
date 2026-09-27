package network

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

// --- AssociateNetworkACLSubnet ---

func TestAssociateNetworkACLSubnetRequestBodyChecksSubnetVPCFirst(t *testing.T) {
	var getSubnetCalls int
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			getSubnetCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v2/project-1/network-acl/acl-1/subnets":
			body := decodeBody(t, r)
			if body["aclId"] != "acl-1" {
				t.Fatalf("aclId in body = %v, want acl-1", body["aclId"])
			}
			ids, _ := body["subnetUuids"].([]any)
			if len(ids) != 2 || ids[0] != "subnet-1" || ids[1] != "subnet-2" {
				t.Fatalf("subnetUuids in body = %+v, want [subnet-1 subnet-2]", ids)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2", NoWait: true})
	if err != nil {
		t.Fatalf("AssociateNetworkACLSubnet() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getSubnetCalls != 1 {
		t.Fatalf("GetSubnet calls = %d, want 1", getSubnetCalls)
	}
	if len(out.ACL.SubnetIDs) != 2 {
		t.Fatalf("SubnetIDs = %+v, want 2 entries", out.ACL.SubnetIDs)
	}
}

func TestAssociateNetworkACLSubnetAlreadyAssociatedNoOp(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		default:
			t.Fatalf("unexpected method %s: an unchanged associate must send no PUT and no GetSubnet", r.Method)
		}
	}))

	out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-1"})
	if err != nil {
		t.Fatalf("AssociateNetworkACLSubnet() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAssociateNetworkACLSubnetOfDifferentVPCReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-x":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Fatalf("unexpected request: %s %s: a subnet of another VPC must send no PUT", r.Method, r.URL.Path)
		}
	}))

	_, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestAssociateNetworkACLSubnetPreWriteBoundErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("UPDATING", false, nil, nil)))
		default:
			t.Fatalf("unexpected method %s: a busy ACL must send no PUT", r.Method)
		}
	})))

	_, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-1"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// --- DisassociateNetworkACLSubnet ---

func TestDisassociateNetworkACLSubnetRequestBodyDropsOnlyTheNamedSubnet(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, nil, []string{"subnet-1", "subnet-2"}), // pre-write read
		aclJSON("ACTIVE", false, nil, []string{"subnet-1"}),             // confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		body := decodeBody(t, r)
		ids, _ := body["subnetUuids"].([]any)
		if len(ids) != 1 || ids[0] != "subnet-1" {
			t.Fatalf("subnetUuids in body = %+v, want [subnet-1]", ids)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if err != nil {
		t.Fatalf("DisassociateNetworkACLSubnet() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(out.ACL.SubnetIDs) != 1 || out.ACL.SubnetIDs[0] != "subnet-1" {
		t.Fatalf("SubnetIDs = %+v, want [subnet-1]", out.ACL.SubnetIDs)
	}
}

func TestDisassociateNetworkACLSubnetAbsentNoOp(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		default:
			t.Fatalf("unexpected method %s: disassociating an absent subnet must send no PUT", r.Method)
		}
	}))

	out, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-x"})
	if err != nil {
		t.Fatalf("DisassociateNetworkACLSubnet() error = %v, want nil: an absent subnet is a no-op, not an error", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestDisassociateNetworkACLSubnetNoWaitSkipsPostWritePoll(t *testing.T) {
	var getCalls int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-1", NoWait: true})
	if err != nil {
		t.Fatalf("DisassociateNetworkACLSubnet() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-write read only): NoWait must skip the post-write poll", getCalls)
	}
	if len(out.ACL.SubnetIDs) != 0 {
		t.Fatalf("SubnetIDs = %+v, want none left", out.ACL.SubnetIDs)
	}
}

// --- stringSetsEqual ---

func TestStringSetsEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both empty", nil, nil, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different content", []string{"a", "b"}, []string{"a", "c"}, false},
		{"duplicate mismatch", []string{"a", "a"}, []string{"a", "b"}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := stringSetsEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("stringSetsEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
