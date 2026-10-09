package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// vipData is the "data" envelope GetVirtualIPAddress,
// CreateVirtualIPAddress, and UpdateVirtualIPAddress all share.
type vipData struct {
	UUID           string   `json:"uuid"`
	Name           string   `json:"name"`
	IPAddress      string   `json:"ipAddress"`
	NetworkID      string   `json:"networkId"`
	SubnetID       string   `json:"subnetId"`
	Description    string   `json:"description"`
	Mode           string   `json:"mode"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	AddressPairIPs []string `json:"addressPairIps"`
}

// vipBody builds a {"data": {...}} virtual IP envelope, always id "vip-1"
// and subnet "subnet-1", matching what GetVirtualIPAddress,
// CreateVirtualIPAddress, and UpdateVirtualIPAddress all decode.
func vipBody(name, mode, typ, status string, pairs []string) string {
	b, err := json.Marshal(struct {
		Data vipData `json:"data"`
	}{Data: vipData{
		UUID: "vip-1", Name: name, IPAddress: "203.0.113.10", NetworkID: "vpc-1",
		SubnetID: "subnet-1", Description: "d", Mode: mode, Type: typ,
		Status: status, AddressPairIPs: pairs,
	}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- CreateVirtualIPAddress ---

func TestCreateVirtualIPAddressRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateVirtualIPAddressInput
		want map[string]any
	}{
		{
			name: "with address and description",
			in: &CreateVirtualIPAddressInput{
				SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActivePassive,
				IPAddress: "203.0.113.10", Description: "front door",
			},
			want: map[string]any{
				"subnetId": "subnet-1", "name": "vip1", "mode": "Active/Passive",
				"ipAddress": "203.0.113.10", "description": "front door",
			},
		},
		{
			name: "without address or description",
			in: &CreateVirtualIPAddressInput{
				SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
			},
			want: map[string]any{
				"subnetId": "subnet-1", "name": "vip1", "mode": "Active/Active",
				"ipAddress": "", "description": "",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/virtualIpAddress" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				body := decodeBody(t, r)
				for k, v := range tt.want {
					if body[k] != v {
						t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
					}
				}
				if _, ok := body["tags"]; ok {
					t.Fatalf("body = %+v, must never send tags", body)
				}
				if _, ok := body["zoneId"]; ok {
					t.Fatalf("body = %+v, must never send zoneId", body)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(vipBody(tt.in.Name, tt.in.Mode, "", "ACTIVE", nil)))
			}))

			out, err := c.CreateVirtualIPAddress(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("CreateVirtualIPAddress() error = %v", err)
			}
			if out.VirtualIPAddress.UUID != "vip-1" {
				t.Fatalf("UUID = %q, want vip-1", out.VirtualIPAddress.UUID)
			}
		})
	}
}

func TestCreateVirtualIPAddressDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/network/create_virtual_ip_address.json")
	}))

	out, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "<name>", Mode: VirtualIPModeActivePassive,
	})
	if err != nil {
		t.Fatalf("CreateVirtualIPAddress() error = %v", err)
	}
	if out.VirtualIPAddress.UUID != "vip-1" || out.VirtualIPAddress.Name != "<name>" {
		t.Fatalf("unexpected virtual IP: %+v", out.VirtualIPAddress)
	}
}

func TestCreateVirtualIPAddressRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	base := CreateVirtualIPAddressInput{SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive}

	missingSubnet := base
	missingSubnet.SubnetID = ""
	if _, err := c.CreateVirtualIPAddress(context.Background(), &missingSubnet); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("missing SubnetID: err = %v, want ErrInvalidInput", err)
	}

	missingName := base
	missingName.Name = ""
	if _, err := c.CreateVirtualIPAddress(context.Background(), &missingName); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("missing Name: err = %v, want ErrInvalidInput", err)
	}

	missingMode := base
	missingMode.Mode = ""
	if _, err := c.CreateVirtualIPAddress(context.Background(), &missingMode); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("missing Mode: err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateVirtualIPAddressIPAddressShapeRefusal(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []string{"not-an-ip", "2001:db8::1", "203.0.113.10/24", "999.0.0.1"}
	for _, addr := range cases {
		in := &CreateVirtualIPAddressInput{SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive, IPAddress: addr}
		if _, err := c.CreateVirtualIPAddress(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("IPAddress %q: err = %v, want ErrInvalidInput", addr, err)
		}
	}
}

func TestCreateVirtualIPAddressNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list virtual ip addresses") {
		t.Fatalf("err = %v, want a hint to list virtual ip addresses before creating again", err)
	}
}

func TestCreateVirtualIPAddress4xxIsNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"payment required"}`))
	}))

	_, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *core.APIError", err)
	}
	if strings.Contains(err.Error(), "may have already reached the server") {
		t.Fatalf("err = %v, a 4xx must not be wrapped as ambiguous", err)
	}
}

func TestCreateVirtualIPAddressNoWaitWhenAlreadyActive(t *testing.T) {
	var getCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getCalls.Add(1)
			t.Fatal("no GET expected: the create response was already ACTIVE")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "", "ACTIVE", nil)))
	}))

	out, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	if err != nil {
		t.Fatalf("CreateVirtualIPAddress() error = %v", err)
	}
	if out.VirtualIPAddress.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.VirtualIPAddress.Status)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GET calls = %d, want 0", getCalls.Load())
	}
}

func TestCreateVirtualIPAddressWaitSettlesToActive(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedResponses(t, []string{
		vipBody("vip1", VirtualIPModeActiveActive, "", "CREATING", nil),
		vipBody("vip1", VirtualIPModeActiveActive, "", "ACTIVE", nil),
	}, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "", "CREATING", nil)))
			return true
		}
		return false
	})))

	out, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	if err != nil {
		t.Fatalf("CreateVirtualIPAddress() error = %v", err)
	}
	if out.VirtualIPAddress.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.VirtualIPAddress.Status)
	}
}

func TestCreateVirtualIPAddressWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedResponses(t, []string{
		vipBody("vip1", VirtualIPModeActiveActive, "", "ERROR", nil),
	}, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "", "CREATING", nil)))
			return true
		}
		return false
	})))

	out, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.VirtualIPAddress.UUID != "vip-1" {
		t.Fatalf("Output = %+v, want the last read virtual IP", out)
	}
}

func TestCreateVirtualIPAddressWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "", "CREATING", nil)))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "", "CREATING", nil)))
	})))

	_, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestCreateVirtualIPAddressNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"name":"vip1","status":"ACTIVE"}}`))
	}))

	_, err := c.CreateVirtualIPAddress(context.Background(), &CreateVirtualIPAddressInput{
		SubnetID: "subnet-1", Name: "vip1", Mode: VirtualIPModeActiveActive,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list virtual ip addresses") {
		t.Fatalf("err = %v, want an APIError naming list virtual ip addresses before creating again", err)
	}
}

// --- UpdateVirtualIPAddress ---

func TestUpdateVirtualIPAddressRequestBodyResendsUnchangedFields(t *testing.T) {
	getCount := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCount++
			if getCount == 1 {
				_, _ = w.Write([]byte(vipBody("old-name", VirtualIPModeActivePassive, "", "ACTIVE", nil)))
				return
			}
			_, _ = w.Write([]byte(vipBody("new-name", VirtualIPModeActivePassive, "", "ACTIVE", nil)))
		case http.MethodPut:
			body := decodeBody(t, r)
			if body["name"] != "new-name" {
				t.Fatalf("body[name] = %v, want new-name", body["name"])
			}
			if body["description"] != "d" {
				t.Fatalf("body[description] = %v, want the read value %q", body["description"], "d")
			}
			if body["mode"] != VirtualIPModeActivePassive {
				t.Fatalf("body[mode] = %v, want the read value %q", body["mode"], VirtualIPModeActivePassive)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(vipBody("new-name", VirtualIPModeActivePassive, "", "ACTIVE", nil)))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	out, err := c.UpdateVirtualIPAddress(context.Background(), &UpdateVirtualIPAddressInput{
		VirtualIPAddressID: "vip-1", Name: vngcloud.Ptr("new-name"),
	})
	if err != nil {
		t.Fatalf("UpdateVirtualIPAddress() error = %v", err)
	}
	if out.VirtualIPAddress.Name != "new-name" {
		t.Fatalf("Name = %q, want new-name", out.VirtualIPAddress.Name)
	}
}

func TestUpdateVirtualIPAddressRequiresAtLeastOneField(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.UpdateVirtualIPAddress(context.Background(), &UpdateVirtualIPAddressInput{VirtualIPAddressID: "vip-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateVirtualIPAddressPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		_, err := c.UpdateVirtualIPAddress(context.Background(), &UpdateVirtualIPAddressInput{
			VirtualIPAddressID: id, Name: vngcloud.Ptr("x"),
		})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VirtualIPAddressID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestUpdateVirtualIPAddressConfirmReadFailureFallsBackToFixture(t *testing.T) {
	getCount := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCount++
			if getCount == 1 {
				_, _ = w.Write([]byte(vipBody("old-name", VirtualIPModeActivePassive, "", "ACTIVE", nil)))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/network/update_virtual_ip_address.json")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	out, err := c.UpdateVirtualIPAddress(context.Background(), &UpdateVirtualIPAddressInput{
		VirtualIPAddressID: "vip-1", Mode: vngcloud.Ptr(VirtualIPModeActiveActive),
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.VirtualIPAddress.Mode != VirtualIPModeActiveActive {
		t.Fatalf("Output = %+v, want the fallback decoded from the PUT's own fixture response", out)
	}
}

func TestUpdateVirtualIPAddressNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.UpdateVirtualIPAddress(context.Background(), &UpdateVirtualIPAddressInput{
		VirtualIPAddressID: "vip-x", Name: vngcloud.Ptr("x"),
	})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

// --- isPrivateVirtualIPType ---

func TestIsPrivateVirtualIPType(t *testing.T) {
	cases := []struct {
		typ  string
		want bool
	}{
		{"private", true},
		{"PRIVATE", false},
		{"Private", false},
		{"public", false},
		{"public-vm", false},
		{"public-mkp", false},
		{"", false},
	}
	for _, tt := range cases {
		if got := isPrivateVirtualIPType(tt.typ); got != tt.want {
			t.Errorf("isPrivateVirtualIPType(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

// --- DeleteVirtualIPAddress ---

func TestDeleteVirtualIPAddressGuardAddressPairIPsOnRead(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/vip-1") {
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, virtualIPTypePrivate, "ACTIVE", []string{"203.0.113.20"})))
	}))

	_, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteVirtualIPAddressGuardListedAddressPair(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/vip-1"):
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, virtualIPTypePrivate, "ACTIVE", nil)))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/addressPairs"):
			_, _ = w.Write([]byte(`{"data":[{"uuid":"pair-1","networkInterfaceIp":"203.0.113.20"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteVirtualIPAddressGuardPublicType(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/vip-1"):
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "public-vm", "ACTIVE", nil)))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDeleteVirtualIPAddressGuardEmptyType checks that an empty Type fails
// closed: the design records no meaning for it, so it must not be treated
// as a private virtual IP.
func TestDeleteVirtualIPAddressGuardEmptyType(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/vip-1"):
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "", "ACTIVE", nil)))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/addressPairs"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestDeleteVirtualIPAddressGuardUnrecordedPrivateType checks that only the
// lowercase type recorded by the API permits deletion.
func TestDeleteVirtualIPAddressGuardUnrecordedPrivateType(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/vip-1"):
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, "PRIVATE", "ACTIVE", nil)))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteVirtualIPAddressSuccess(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/vip-1"):
			_, _ = w.Write([]byte(vipBody("vip1", VirtualIPModeActiveActive, virtualIPTypePrivate, "ACTIVE", nil)))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/addressPairs"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-1"}); err != nil {
		t.Fatalf("DeleteVirtualIPAddress() error = %v", err)
	}
}

func TestDeleteVirtualIPAddressPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VirtualIPAddressID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestDeleteVirtualIPAddressNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.DeleteVirtualIPAddress(context.Background(), &DeleteVirtualIPAddressInput{VirtualIPAddressID: "vip-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

// --- Path ID checks on the existing reads ---

func TestGetVirtualIPAddressPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.GetVirtualIPAddress(context.Background(), &GetVirtualIPAddressInput{VirtualIPAddressID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VirtualIPAddressID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestListAddressPairsByVirtualIPAddressPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.ListAddressPairsByVirtualIPAddress(context.Background(), &ListAddressPairsByVirtualIPAddressInput{VirtualIPAddressID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VirtualIPAddressID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}
