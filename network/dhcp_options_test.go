package network

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListDHCPOptions(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/dhcp_option" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "prod" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_dhcp_options.json")
	}))

	out, err := c.ListDHCPOptions(context.Background(), &ListDHCPOptionsInput{Name: "prod", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListDHCPOptions() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "dop-1" || len(out.Items[0].DNSServers) != 2 {
		t.Fatalf("unexpected sets: %+v", out)
	}
}

func TestListDHCPOptionsDefaultPageSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := c.ListDHCPOptions(context.Background(), nil); err != nil {
		t.Fatalf("ListDHCPOptions() error = %v", err)
	}
}

func TestGetDHCPOptionsDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/dhcp_option/dop-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/get_dhcp_options.json")
	}))

	out, err := c.GetDHCPOptions(context.Background(), &GetDHCPOptionsInput{DHCPOptionsID: "dop-1"})
	if err != nil {
		t.Fatalf("GetDHCPOptions() error = %v", err)
	}
	set := out.DHCPOptions
	if set.UUID != "dop-1" || set.Status != "ACTIVE" || set.MTU != 1450 ||
		len(set.DNSServers) != 2 || set.DNSServers[0] != "10.166.12.196" ||
		len(set.VPCIDs) != 1 || set.VPCIDs[0] != "vpc-1" {
		t.Fatalf("unexpected set: %+v", set)
	}
}

func TestGetDHCPOptionsRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.GetDHCPOptions(context.Background(), &GetDHCPOptionsInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.GetDHCPOptions(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestGetDHCPOptionsPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range pathIDRejections {
		if _, err := c.GetDHCPOptions(context.Background(), &GetDHCPOptionsInput{DHCPOptionsID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("DHCPOptionsID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

// TestGetVPCDecodesDHCPOptionsFields checks that GetVPC decodes
// dhcpOptionId and dhcpOptionName, the fields SetVPCDHCPOptions and its wait
// read to confirm a set change.
func TestGetVPCDecodesDHCPOptionsFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/network/get_vpc_with_dhcp_options.json")
	}))

	out, err := c.GetVPC(context.Background(), &GetVPCInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("GetVPC() error = %v", err)
	}
	if out.VPC.DHCPOptionID != "dop-1" || out.VPC.DHCPOptionName != "<name>" {
		t.Fatalf("unexpected vpc: %+v", out.VPC)
	}
}

func TestGetDHCPOptionsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.GetDHCPOptions(context.Background(), &GetDHCPOptionsInput{DHCPOptionsID: "dop-missing"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}
