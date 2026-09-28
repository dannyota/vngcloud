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

// --- CreateDHCPOptions ---

func TestCreateDHCPOptionsRequestBody(t *testing.T) {
	mtu := 1400
	cases := []struct {
		name string
		in   *CreateDHCPOptionsInput
		want map[string]any
	}{
		{
			name: "without mtu",
			in:   &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"10.166.12.196", "10.166.12.197"}},
			want: map[string]any{"name": "corp", "dnsServers": []any{"10.166.12.196", "10.166.12.197"}},
		},
		{
			name: "with mtu",
			in:   &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"10.166.12.196"}, MTU: &mtu},
			want: map[string]any{"name": "corp", "dnsServers": []any{"10.166.12.196"}, "mtu": float64(1400)},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/dhcp_option" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				body := decodeBody(t, r)
				if _, ok := body["tags"]; ok {
					t.Fatalf("body = %+v, must never send tags", body)
				}
				if _, ok := body["zoneId"]; ok {
					t.Fatalf("body = %+v, must never send zoneId", body)
				}
				if _, wantMTU := tt.want["mtu"]; !wantMTU {
					if _, ok := body["mtu"]; ok {
						t.Fatalf("body = %+v, must not send mtu when unset", body)
					}
				}
				for k, v := range tt.want {
					if diff := deepDiff(body[k], v); diff {
						t.Fatalf("body[%s] = %+v, want %+v", k, body[k], v)
					}
				}
				w.WriteHeader(http.StatusCreated)
				testutil.WriteFixture(t, w, "../testdata/network/create_dhcp_options.json")
			}))

			if _, err := c.CreateDHCPOptions(context.Background(), tt.in); err != nil {
				t.Fatalf("CreateDHCPOptions() error = %v", err)
			}
		})
	}
}

// deepDiff reports whether a and b differ, comparing slices element-wise
// since map[string]any from a decoded JSON body holds []any, not []string.
func deepDiff(a, b any) bool {
	as, aok := a.([]any)
	bs, bok := b.([]any)
	if aok || bok {
		if !aok || !bok || len(as) != len(bs) {
			return true
		}
		for i := range as {
			if as[i] != bs[i] {
				return true
			}
		}
		return false
	}
	return a != b
}

func TestCreateDHCPOptionsDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/network/create_dhcp_options.json")
	}))

	out, err := c.CreateDHCPOptions(context.Background(), &CreateDHCPOptionsInput{Name: "<name>", DNSServers: []string{"10.166.12.196"}})
	if err != nil {
		t.Fatalf("CreateDHCPOptions() error = %v", err)
	}
	if out.DHCPOptions.UUID != "dop-1" || out.DHCPOptions.MTU != 1450 {
		t.Fatalf("unexpected set: %+v", out.DHCPOptions)
	}
}

func TestCreateDHCPOptionsRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateDHCPOptions(context.Background(), &CreateDHCPOptionsInput{DNSServers: []string{"10.0.0.1"}}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateDHCPOptions(context.Background(), &CreateDHCPOptionsInput{Name: "corp"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateDHCPOptionsShapeRefusals(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	cases := []struct {
		name string
		in   *CreateDHCPOptionsInput
	}{
		{"empty DNS list", &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{}}},
		{"non-IPv4 DNS server", &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"not-an-ip"}}},
		{"IPv6 DNS server", &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"2001:db8::1"}}},
		{"system-prefixed name", &CreateDHCPOptionsInput{Name: "dhcp-option-dns-1", DNSServers: []string{"10.0.0.1"}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.CreateDHCPOptions(context.Background(), tt.in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateDHCPOptionsNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateDHCPOptions(context.Background(), &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"10.0.0.1"}})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list-dhcp-options") {
		t.Fatalf("err = %v, want a hint to run list-dhcp-options before creating again", err)
	}
}

func TestCreateDHCPOptionsNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"name":"corp"}}`))
	}))

	_, err := c.CreateDHCPOptions(context.Background(), &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"10.0.0.1"}})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list-dhcp-options") {
		t.Fatalf("err = %v, want an APIError naming list-dhcp-options before creating again", err)
	}
}

func TestCreateDHCPOptionsServerRefusalPassesThrough(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"maximum number of DHCP options sets reached"}`))
	}))

	_, err := c.CreateDHCPOptions(context.Background(), &CreateDHCPOptionsInput{Name: "corp", DNSServers: []string{"10.0.0.1"}})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
}

// --- DeleteDHCPOptions ---

func TestDeleteDHCPOptionsGuardAttachedToVPC(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"uuid":"dop-1","status":"ACTIVE","associatedNetworks":["vpc-1"]}`))
	}))

	_, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
	if !strings.Contains(err.Error(), "vpc-1") {
		t.Fatalf("err = %v, want it to name vpc-1", err)
	}
}

func TestDeleteDHCPOptionsSendsDeleteWhenUnattached(t *testing.T) {
	var deleteCalled atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deleteCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-1","status":"ACTIVE","associatedNetworks":[]}`))
		}
	}))

	if _, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-1"}); err != nil {
		t.Fatalf("DeleteDHCPOptions() error = %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("DELETE was never sent")
	}
}

// TestDeleteDHCPOptionsAllowsUnattachedSystemSet checks that an unattached
// set left by a deleted Private DNS VPC deletes like any other set, with no
// extra guard on its dhcp-option-dns- name.
func TestDeleteDHCPOptionsAllowsUnattachedSystemSet(t *testing.T) {
	var deleteCalled atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deleteCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-2","name":"dhcp-option-dns-1","status":"ACTIVE","associatedNetworks":[]}`))
		}
	}))

	if _, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-2"}); err != nil {
		t.Fatalf("DeleteDHCPOptions() error = %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("DELETE was never sent for an unattached system set")
	}
}

func TestDeleteDHCPOptionsRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteDHCPOptionsPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("DHCPOptionsID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestDeleteDHCPOptionsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-missing"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

// TestDeleteDHCPOptionsRefusesUnexpectedRead checks the rule that the
// pre-delete read's UUID must equal the requested DHCPOptionsID: a read that
// names a different set is never acted on, whatever caused the mismatch.
func TestDeleteDHCPOptionsRefusesUnexpectedRead(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			t.Fatal("no DELETE expected")
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-other","status":"ACTIVE","associatedNetworks":[]}`))
		}
	}))

	_, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-1"})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if !strings.Contains(err.Error(), "dop-other") || !strings.Contains(err.Error(), "dop-1") {
		t.Fatalf("err = %v, want it to name both the unexpected read dop-other and the requested dop-1", err)
	}
}

// TestDeleteDHCPOptionsServerRefusalAfterUnattachedRead checks that the
// server's own refusal on the DELETE itself passes through unchanged even
// though the SDK's own pre-delete read found the set unattached: the read is
// a guard, not a guarantee, and the server's answer is the final word.
func TestDeleteDHCPOptionsServerRefusalAfterUnattachedRead(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"400", http.StatusBadRequest},
		{"409", http.StatusConflict},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"uuid":"dop-1","status":"ACTIVE","associatedNetworks":[]}`))
				case http.MethodDelete:
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(`{"message":"still in use"}`))
				}
			}))

			_, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-1"})
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v, want a %d *core.APIError", err, tt.status)
			}
		})
	}
}

// TestDeleteDHCPOptionsRetriesThenNotFound checks that a DELETE the
// transport retries after a 503 (idempotent, per the design) can still land
// on the set already gone, in which case DeleteDHCPOptions returns NotFound.
func TestDeleteDHCPOptionsRetriesThenNotFound(t *testing.T) {
	var deleteCalls atomic.Int64
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-1","status":"ACTIVE","associatedNetworks":[]}`))
		case http.MethodDelete:
			if deleteCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	})))

	_, err := c.DeleteDHCPOptions(context.Background(), &DeleteDHCPOptionsInput{DHCPOptionsID: "dop-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
	if deleteCalls.Load() != 2 {
		t.Fatalf("DELETE calls = %d, want 2 (the transport retries a 503)", deleteCalls.Load())
	}
}

// --- SetVPCDHCPOptions ---

// dhcpVPCBody builds a GetVPC-shaped body for the given DNS status and
// current DHCP options set.
func dhcpVPCBody(dnsStatus, dhcpOptionID, dhcpOptionName string) string {
	return `{"id":"vpc-1","status":"ACTIVE","dnsStatus":"` + dnsStatus + `","dhcpOptionId":"` + dhcpOptionID + `","dhcpOptionName":"` + dhcpOptionName + `"}`
}

func TestSetVPCDHCPOptionsRequestBody(t *testing.T) {
	var patched atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			if r.URL.Path != "/v2/project-1/networks/vpc-1/updateDhcpOption" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if len(body) != 1 || body["dhcpOptionId"] != "dop-2" {
				t.Fatalf("body = %+v, want only dhcpOptionId=dop-2", body)
			}
			patched.Store(true)
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/dhcp_option/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-2","status":"ACTIVE","associatedNetworks":[]}`))
		case r.Method == http.MethodGet:
			// Before the PATCH lands, the VPC still names its old set; the
			// confirm wait needs a read that only shows dop-2 once patched is
			// true, or SetVPCDHCPOptions would return Changed=false at once
			// without ever sending the PATCH this test exists to check.
			current := "dop-1"
			if patched.Load() {
				current = "dop-2"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", current, "corp")))
		}
	})))

	out, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if err != nil {
		t.Fatalf("SetVPCDHCPOptions() error = %v", err)
	}
	if !patched.Load() {
		t.Fatal("PATCH was never sent")
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestSetVPCDHCPOptionsAlreadySetSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
	}))

	out, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-1"})
	if err != nil {
		t.Fatalf("SetVPCDHCPOptions() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestSetVPCDHCPOptionsGuardPrivateDNSEnabled(t *testing.T) {
	for _, status := range []string{"ENABLED", "ENABLING"} {
		t.Run(status, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatal("no write expected")
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(dhcpVPCBody(status, "dop-sys", "dhcp-option-dns-1")))
			}))

			_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
			if !errors.Is(err, ErrDefaultResource) {
				t.Fatalf("err = %v, want ErrDefaultResource", err)
			}
		})
	}
}

func TestSetVPCDHCPOptionsGuardCurrentSystemSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-sys", "dhcp-option-dns-1")))
	}))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

// TestSetVPCDHCPOptionsGuardCurrentSystemSetEmptyName checks that when the
// VPC read gives a current DHCPOptionID but an empty DHCPOptionName,
// SetVPCDHCPOptions reads that set by id rather than assuming it is not a
// system set, and still refuses when that read shows the system prefix.
func TestSetVPCDHCPOptionsGuardCurrentSystemSetEmptyName(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			t.Fatal("no PATCH expected")
		case strings.Contains(r.URL.Path, "/dhcp_option/dop-2"):
			t.Fatal("target set must not be read after refusing on the current system set")
		case strings.Contains(r.URL.Path, "/dhcp_option/dop-sys"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-sys","name":"dhcp-option-dns-1","status":"ACTIVE","associatedNetworks":["vpc-1"]}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-sys", "")))
		}
	}))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

// TestSetVPCDHCPOptionsCurrentSetEmptyNameNotSystemProceeds checks the other
// side of the empty-name case: reading the current set by id and finding it
// is not a system set lets the write proceed normally.
func TestSetVPCDHCPOptionsCurrentSetEmptyNameNotSystemProceeds(t *testing.T) {
	var patched atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			patched.Store(true)
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/dhcp_option/dop-1"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-1","name":"corp-current","status":"ACTIVE","associatedNetworks":["vpc-1"]}`))
		case strings.Contains(r.URL.Path, "/dhcp_option/dop-2"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-2","status":"ACTIVE","associatedNetworks":[]}`))
		case r.Method == http.MethodGet:
			current := "dop-1"
			if patched.Load() {
				current = "dop-2"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", current, "")))
		}
	})))

	out, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if err != nil {
		t.Fatalf("SetVPCDHCPOptions() error = %v", err)
	}
	if !patched.Load() {
		t.Fatal("PATCH was never sent")
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestSetVPCDHCPOptionsGuardTargetNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			t.Fatal("no PATCH expected")
		case strings.Contains(r.URL.Path, "/dhcp_option/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
		}
	}))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-missing"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestSetVPCDHCPOptionsGuardTargetSystemSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			t.Fatal("no PATCH expected")
		case strings.Contains(r.URL.Path, "/dhcp_option/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-sys","name":"dhcp-option-dns-1","status":"ACTIVE","associatedNetworks":[]}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
		}
	}))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-sys"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

func TestSetVPCDHCPOptionsGuardTargetNotActive(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			t.Fatal("no PATCH expected")
		case strings.Contains(r.URL.Path, "/dhcp_option/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-2","status":"CREATING","associatedNetworks":[]}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
		}
	}))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// TestSetVPCDHCPOptionsVPCNotFound checks that a 404 from the initial
// GetVPC read passes straight through as NotFound.
func TestSetVPCDHCPOptionsVPCNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-missing", DHCPOptionsID: "dop-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

// TestSetVPCDHCPOptionsPatchFails checks the PATCH's own failure statuses: a
// 400 or 404 is a 4xx *core.APIError returned as is, since the server never
// acted on it, while a 5xx additionally wraps a hint that the change may
// already be in place and that get-vpc shows the VPC's current set.
func TestSetVPCDHCPOptionsPatchFails(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantHint bool
	}{
		{"400", http.StatusBadRequest, false},
		{"404", http.StatusNotFound, false},
		{"500", http.StatusInternalServerError, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPatch:
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(`{"message":"failed"}`))
				case strings.Contains(r.URL.Path, "/dhcp_option/"):
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"uuid":"dop-2","status":"ACTIVE","associatedNetworks":[]}`))
				case r.Method == http.MethodGet:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
				}
			}))

			_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v, want a %d *core.APIError", err, tt.status)
			}
			if strings.Contains(err.Error(), "get-vpc") != tt.wantHint {
				t.Fatalf("err = %v, want hint to run get-vpc = %v", err, tt.wantHint)
			}
		})
	}
}

func TestSetVPCDHCPOptionsRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{DHCPOptionsID: "dop-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestSetVPCDHCPOptionsPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: id, DHCPOptionsID: "dop-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("DHCPOptionsID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestSetVPCDHCPOptionsWaitFailsOnError(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/dhcp_option/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-2","status":"ACTIVE","associatedNetworks":[]}`))
		case r.Method == http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ERROR","dnsStatus":"DISABLED","dhcpOptionId":"dop-1"}`))
		}
	})))

	out, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || !out.Changed {
		t.Fatalf("Output = %+v, want Changed true (the PATCH was sent)", out)
	}
}

func TestSetVPCDHCPOptionsWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/dhcp_option/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-2","status":"ACTIVE","associatedNetworks":[]}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
		}
	})))

	_, err := c.SetVPCDHCPOptions(context.Background(), &SetVPCDHCPOptionsInput{VPCID: "vpc-1", DHCPOptionsID: "dop-2"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// --- ClearVPCDHCPOptions ---

func TestClearVPCDHCPOptionsRequestBody(t *testing.T) {
	var patched atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			if r.URL.Path != "/v2/project-1/networks/vpc-1/updateDhcpOption" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if len(body) != 0 {
				t.Fatalf("body = %+v, want an empty object", body)
			}
			patched.Store(true)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			current, name := "dop-1", "corp"
			if patched.Load() {
				current, name = "", ""
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", current, name)))
		}
	})))

	out, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("ClearVPCDHCPOptions() error = %v", err)
	}
	if !patched.Load() {
		t.Fatal("PATCH was never sent")
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestClearVPCDHCPOptionsAlreadyEmptySendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "", "")))
	}))

	out, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("ClearVPCDHCPOptions() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestClearVPCDHCPOptionsGuardPrivateDNSEnabled(t *testing.T) {
	for _, status := range []string{"ENABLED", "ENABLING"} {
		t.Run(status, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatal("no write expected")
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(dhcpVPCBody(status, "dop-1", "corp")))
			}))

			_, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
			if !errors.Is(err, ErrDefaultResource) {
				t.Fatalf("err = %v, want ErrDefaultResource", err)
			}
		})
	}
}

func TestClearVPCDHCPOptionsGuardCurrentSystemSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-sys", "dhcp-option-dns-1")))
	}))

	_, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

// TestClearVPCDHCPOptionsGuardCurrentSystemSetEmptyName checks that, like
// SetVPCDHCPOptions, an empty DHCPOptionName on the VPC read makes
// ClearVPCDHCPOptions read the current set by id rather than assume it is
// not a system set.
func TestClearVPCDHCPOptionsGuardCurrentSystemSetEmptyName(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			t.Fatal("no PATCH expected")
		case strings.Contains(r.URL.Path, "/dhcp_option/dop-sys"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"dop-sys","name":"dhcp-option-dns-1","status":"ACTIVE","associatedNetworks":["vpc-1"]}`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-sys", "")))
		}
	}))

	_, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

func TestClearVPCDHCPOptionsVPCNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-missing"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

// TestClearVPCDHCPOptionsPatchFails checks the PATCH's own failure statuses,
// mirroring TestSetVPCDHCPOptionsPatchFails: a 400 or 404 is a 4xx
// *core.APIError returned as is, while a 5xx additionally wraps a hint that
// the change may already be in place and that get-vpc shows the VPC's
// current set.
func TestClearVPCDHCPOptionsPatchFails(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantHint bool
	}{
		{"400", http.StatusBadRequest, false},
		{"404", http.StatusNotFound, false},
		{"500", http.StatusInternalServerError, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPatch:
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(`{"message":"failed"}`))
				case http.MethodGet:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
				}
			}))

			_, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("err = %v, want a %d *core.APIError", err, tt.status)
			}
			if strings.Contains(err.Error(), "get-vpc") != tt.wantHint {
				t.Fatalf("err = %v, want hint to run get-vpc = %v", err, tt.wantHint)
			}
		})
	}
}

func TestClearVPCDHCPOptionsRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestClearVPCDHCPOptionsPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("VPCID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestClearVPCDHCPOptionsWaitFailsOnError(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ERROR","dnsStatus":"DISABLED","dhcpOptionId":"dop-1"}`))
		}
	})))

	out, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || !out.Changed {
		t.Fatalf("Output = %+v, want Changed true (the PATCH was sent)", out)
	}
}

func TestClearVPCDHCPOptionsWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
		}
	})))

	_, err := c.ClearVPCDHCPOptions(context.Background(), &ClearVPCDHCPOptionsInput{VPCID: "vpc-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestWaitVPCDHCPOptionsSetPollParameters checks the literal interval and
// bound waitVPCDHCPOptionsSet passes to poll (the package's generic 2s
// interval and 60s bound), so that swapping either with another wait's
// values fails this test: the handler never shows the target set, so the
// wait always runs to its bound.
func TestWaitVPCDHCPOptionsSetPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(dhcpVPCBody("DISABLED", "dop-1", "corp")))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitVPCDHCPOptionsSet(context.Background(), "op", "vpc-1", "dop-2"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 30 {
		t.Fatalf("sleep calls = %d, want 30 (a 2s interval over a 60s bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Fatalf("sleep duration = %s, want 2s", d)
		}
	}
}
