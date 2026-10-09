package compute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func validCreateServerInput() *CreateServerInput {
	return &CreateServerInput{
		Name:             "web-1",
		ZoneID:           "zone-1",
		FlavorID:         "flavor-1",
		ImageID:          "image-1",
		VPCID:            "vpc-1",
		SubnetID:         "subnet-1",
		SecurityGroupIDs: []string{"sg-1"},
		SSHKeyID:         "key-1",
		RootDiskSize:     20,
		RootDiskTypeID:   "voltype-1",
	}
}

const quoteServerFixture = `{"optimumPrice":347800,"originalPrice":347800,"discountPrice":0,"discountPercent":0,"propertiesPrice":[{"name":"INSTANCE TYPE","description":"","optimumPrice":283800,"monthlyPrice":283800,"currentPrice":null,"discountPercent":0},{"name":"ROOT DISK","description":"","optimumPrice":64000,"monthlyPrice":64000,"currentPrice":null,"discountPercent":0}]}`

func TestQuoteCreateServerSendsCreateBody(t *testing.T) {
	var gotPath string
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, data)
		}
		_, _ = w.Write([]byte(quoteServerFixture))
	}))

	out, err := c.QuoteCreateServer(context.Background(), validCreateServerInput())
	if err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
	if gotPath != "/v1/price" {
		t.Fatalf("path = %s, want /v1/price", gotPath)
	}
	if body["resourceType"] != "server" || body["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	info, ok := body["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", body)
	}
	want := map[string]any{
		"name": "web-1", "zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1",
		"networkId": "vpc-1", "subnetId": "subnet-1", "sshKeyId": "key-1",
		"rootDiskSize": float64(20), "rootDiskTypeId": "voltype-1",
		"encryptionVolume": false, "isEnableAutoRenew": false,
		"period": float64(1), "isPoc": false,
	}
	for k, v := range want {
		if info[k] != v {
			t.Fatalf("resourceInfo[%q] = %v, want %v", k, info[k], v)
		}
	}
	if _, ok := info["userData"]; ok {
		t.Fatal("resourceInfo has userData, want it excluded")
	}
	if out.OptimumPrice != 347800 {
		t.Fatalf("OptimumPrice = %v, want 347800", out.OptimumPrice)
	}
	var hasInstance, hasRootDisk bool
	for _, p := range out.Properties {
		switch p.Name {
		case "INSTANCE TYPE":
			hasInstance = true
		case "ROOT DISK":
			hasRootDisk = true
		}
	}
	if !hasInstance || !hasRootDisk {
		t.Fatalf("missing expected price lines: %+v", out.Properties)
	}
}

func TestQuoteCreateServerExcludesUserData(t *testing.T) {
	var raw []byte
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		raw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, raw)
		}
		_, _ = w.Write([]byte(quoteServerFixture))
	}))

	in := validCreateServerInput()
	in.UserData = "#!/bin/sh\necho supersecret"
	if _, err := c.QuoteCreateServer(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
	info := body["resourceInfo"].(map[string]any)
	if _, ok := info["userData"]; ok {
		t.Fatal("resourceInfo has userData, want it excluded")
	}
	if _, ok := info["userDataBase64Encoded"]; ok {
		t.Fatal("resourceInfo has userDataBase64Encoded, want it excluded")
	}
	if strings.Contains(string(raw), "supersecret") {
		t.Fatalf("quote body leaked user data: %s", raw)
	}
}

func TestQuoteCreateServerIgnoresMaxPriceAndNoWait(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(quoteServerFixture))
	}))
	in := validCreateServerInput()
	in.MaxPrice = 1
	in.NoWait = true
	if _, err := c.QuoteCreateServer(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
}

func TestQuoteCreateServerRejectsEmptySecurityGroups(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateServerInput()
	in.SecurityGroupIDs = nil
	if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}

	in2 := validCreateServerInput()
	in2.SecurityGroupIDs = []string{}
	if _, err := c.QuoteCreateServer(context.Background(), in2); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("empty slice: err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteCreateServerRejectsHalfDataDisk(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateServerInput()
	in.DataDiskSize = 10
	if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("size only: err = %v, want ErrInvalidInput", err)
	}

	in2 := validCreateServerInput()
	in2.DataDiskTypeID = "voltype-2"
	if _, err := c.QuoteCreateServer(context.Background(), in2); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("type only: err = %v, want ErrInvalidInput", err)
	}
}

// TestQuoteCreateServerRejectsNonPositiveRootDiskSize checks that a zero or
// negative RootDiskSize refuses before any request: CheckRequired already
// catches 0 through the vngcloud:"required" tag, so this exercises the
// explicit check that also catches a negative value, which CheckRequired's
// zero-value test cannot.
func TestQuoteCreateServerRejectsNonPositiveRootDiskSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, size := range []int{0, -1, -20} {
		in := validCreateServerInput()
		in.RootDiskSize = size
		if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("RootDiskSize=%d: err = %v, want ErrInvalidInput", size, err)
		}
	}
}

// TestQuoteCreateServerRejectsNegativeDataDiskSize checks that a negative
// DataDiskSize refuses before any request, even with DataDiskTypeID empty,
// where the paired-fields check alone would not catch it: a negative size
// left unpaired would otherwise reach the wire.
func TestQuoteCreateServerRejectsNegativeDataDiskSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateServerInput()
	in.DataDiskSize = -5
	if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteCreateServerAllowsFullDataDisk(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, data)
		}
		info := body["resourceInfo"].(map[string]any)
		if info["dataDiskSize"] != float64(10) || info["dataDiskTypeId"] != "voltype-2" {
			t.Fatalf("unexpected data disk fields: %+v", info)
		}
		_, _ = w.Write([]byte(quoteServerFixture))
	}))
	in := validCreateServerInput()
	in.DataDiskSize = 10
	in.DataDiskTypeID = "voltype-2"
	if _, err := c.QuoteCreateServer(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
}

func TestQuoteCreateServerRejectsBadBodyIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	badValues := []string{"..", ".", "/", "?"}
	fields := []struct {
		name string
		set  func(in *CreateServerInput, v string)
	}{
		{"FlavorID", func(in *CreateServerInput, v string) { in.FlavorID = v }},
		{"ImageID", func(in *CreateServerInput, v string) { in.ImageID = v }},
		{"VPCID", func(in *CreateServerInput, v string) { in.VPCID = v }},
		{"SubnetID", func(in *CreateServerInput, v string) { in.SubnetID = v }},
		{"SSHKeyID", func(in *CreateServerInput, v string) { in.SSHKeyID = v }},
		{"RootDiskTypeID", func(in *CreateServerInput, v string) { in.RootDiskTypeID = v }},
		{"ServerGroupID", func(in *CreateServerInput, v string) { in.ServerGroupID = v }},
		{"SecurityGroupIDs[0]", func(in *CreateServerInput, v string) { in.SecurityGroupIDs = []string{v} }},
	}
	for _, f := range fields {
		for _, bad := range badValues {
			in := validCreateServerInput()
			f.set(in, bad)
			if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("%s=%q err = %v, want ErrInvalidInput", f.name, bad, err)
			}
		}
	}

	in := validCreateServerInput()
	in.DataDiskSize = 10
	in.DataDiskTypeID = "/"
	if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("DataDiskTypeID=/ err = %v, want ErrInvalidInput", err)
	}
}
