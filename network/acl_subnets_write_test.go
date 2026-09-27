package network

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
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
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1","interfaceAclPolicyUuid":"acl-old"}`))
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
	if out.PreviousNetworkACLID != "acl-old" {
		t.Fatalf("PreviousNetworkACLID = %q, want acl-old", out.PreviousNetworkACLID)
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

func TestAssociateNetworkACLSubnetPreWriteRecheckMismatchErrBusy(t *testing.T) {
	var aclGets int
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			aclGets++
			w.Header().Set("Content-Type", "application/json")
			if aclGets == 1 {
				_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
				return
			}
			// The recheck, and any read after it, sees a subnet list the
			// pre-write read never saw: another writer changed the ACL.
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1", "subnet-9"})))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut:
			t.Fatal("unexpected PUT: a mismatched recheck must send nothing")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	_, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAssociateNetworkACLSubnetPUT4xxSurfacesAPIError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad subnet list"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	_, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
}

// TestAssociateNetworkACLSubnetPUTBusyMapsToErrBusy checks the busy window
// confirmed live: a subnets PUT sent while the ACL is still busy from a
// previous write returns 400 with a message naming the ACL busy; this SDK
// maps that to ErrBusy, since the PUT was rejected outright and changed
// nothing.
func TestAssociateNetworkACLSubnetPUTBusyMapsToErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The ACL with id acl-1 is busy doing something"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if out != nil {
		t.Fatalf("out = %+v, want nil: nothing was changed", out)
	}
}

// TestAssociateNetworkACLSubnetPUT5xxNotSettledSingleAttempt checks that
// the subnets PUT is sent with Once true: a 502, 503, or 504 is never
// retried by the transport, which could otherwise land a second attempt in
// the ACL's own roughly 18-second busy window and be misread as ErrBusy
// when the first attempt may already have reached the server. Such a
// failure wraps ErrNotSettled instead, never ErrBusy.
func TestAssociateNetworkACLSubnetPUT5xxNotSettledSingleAttempt(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var putCalls atomic.Int64
			c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
				case r.Method == http.MethodPut:
					putCalls.Add(1)
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"upstream error"}`))
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			})))

			out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
			if !errors.Is(err, ErrNotSettled) {
				t.Fatalf("err = %v, want ErrNotSettled", err)
			}
			if errors.Is(err, ErrBusy) {
				t.Fatalf("err = %v, must not also be ErrBusy: the PUT may have reached the server", err)
			}
			if out != nil {
				t.Fatalf("out = %+v, want nil: whether the write landed is unknown", out)
			}
			if putCalls.Load() != 1 {
				t.Fatalf("PUT calls = %d, want exactly 1: Once must stop the transport from retrying a %d", putCalls.Load(), status)
			}
		})
	}
}

// TestAssociateNetworkACLSubnetPUT404SurfacesNotFound checks that a plain
// 404 on the subnets PUT, unlike a busy 400 or a 5xx, is returned as is: it
// is a 4xx the server rejected outright, so it is not wrapped in ErrBusy or
// ErrNotSettled.
func TestAssociateNetworkACLSubnetPUT404SurfacesNotFound(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if !core.IsNotFound(err) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
	if errors.Is(err, ErrBusy) || errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, must not be ErrBusy or ErrNotSettled: a plain 404 is a clean rejection", err)
	}
	if out != nil {
		t.Fatalf("out = %+v, want nil", out)
	}
}

// TestAssociateNetworkACLSubnetWaitErrFailed checks that the ACL reaching
// ERROR after a successful subnets PUT returns an error wrapping ErrFailed,
// the same as the rules replace wait. aclGets counts only the ACL's own
// GET, since the subnet lookup between the pre-write read and the pre-PUT
// recheck is also a GET and must not consume one of this script's steps.
func TestAssociateNetworkACLSubnetWaitErrFailed(t *testing.T) {
	var aclGets int
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			aclGets++
			w.Header().Set("Content-Type", "application/json")
			if aclGets <= 2 {
				_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
				return
			}
			_, _ = w.Write([]byte(aclJSON("ERROR", false, nil, nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.ACL.Status != "ERROR" {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR ACL", out)
	}
}

// TestAssociateNetworkACLSubnetConfirmMismatchErrNotSettled checks that a
// confirm read naming a different subnet list than the one just sent
// returns an error wrapping ErrNotSettled, even though the PUT itself
// succeeded. aclGets counts only the ACL's own GET, for the reason
// TestAssociateNetworkACLSubnetWaitErrFailed's doc comment gives.
func TestAssociateNetworkACLSubnetConfirmMismatchErrNotSettled(t *testing.T) {
	var aclGets int
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			aclGets++
			w.Header().Set("Content-Type", "application/json")
			if aclGets <= 2 {
				_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
				return
			}
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-9"})))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets/subnet-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"subnet-2","networkUuid":"vpc-1"}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || !out.Changed {
		t.Fatal("out = nil or Changed = false: the PUT was sent even though the confirm read did not match")
	}
}

// --- DisassociateNetworkACLSubnet ---

func TestDisassociateNetworkACLSubnetRequestBodyDropsOnlyTheNamedSubnet(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, nil, []string{"subnet-1", "subnet-2"}), // pre-write read
		aclJSON("ACTIVE", false, nil, []string{"subnet-1", "subnet-2"}), // pre-PUT recheck: unchanged
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
	if getCalls != 2 {
		t.Fatalf("GET calls = %d, want 2 (the pre-write read and the pre-PUT recheck): NoWait must skip only the post-write poll", getCalls)
	}
	if len(out.ACL.SubnetIDs) != 0 {
		t.Fatalf("SubnetIDs = %+v, want none left", out.ACL.SubnetIDs)
	}
}

func TestDisassociateNetworkACLSubnetPreWriteRecheckMismatchErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, nil, []string{"subnet-1", "subnet-2"}), // pre-write read
		aclJSON("ACTIVE", false, nil, []string{"subnet-1", "subnet-9"}), // pre-PUT recheck: changed
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT: a mismatched recheck must send nothing")
		}
	})))

	_, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-2"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestDisassociateNetworkACLSubnetPUT4xxSurfacesAPIError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		case http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad subnet list"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: "subnet-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
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
