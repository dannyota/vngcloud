package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func TestQuoteCreateServerFloatingRegionScope(t *testing.T) {
	for _, region := range []string{"han-1", "hcm-3", "other-region"} {
		t.Run(region, func(t *testing.T) {
			var resources []string
			c := newFloatingRegionClient(t, region, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/price" {
					t.Errorf("unexpected read %s", r.URL.Path)
				}
				resources = append(resources, floatingRegionQuote(t, w, r))
			}))
			in := pricedServerInput()
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			if err != nil || out == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			price, types := float64(347800), "server"
			line := pricing.PriceProperty{Name: "FLOATING IP", Description: "Included in server order for the verified configuration"}
			if region == "other-region" {
				price, types = 467800, "server,elastic-ip"
				line = pricing.PriceProperty{Name: "FLOATING IP", Description: "Conservative Elastic IP estimate; extra create charge unconfirmed", OptimumPrice: 120000, MonthlyPrice: 120000}
			}
			if strings.Join(resources, ",") != types || out.OptimumPrice != price || out.OriginalPrice != price || out.DiscountPrice != 0 || out.DiscountPercent != 0 {
				t.Fatalf("resources=%v quote=%+v", resources, out)
			}
			if len(out.Properties) != 3 || !reflect.DeepEqual(out.Properties[2], line) {
				t.Fatalf("properties=%+v want IP line=%+v", out.Properties, line)
			}
		})
	}
}

func TestFloatingQuoteIPFailurePreventsOrder(t *testing.T) {
	for _, status := range []int{200, 401, 500} {
		for _, create := range []bool{false, true} {
			t.Run(fmt.Sprint(status, create), func(t *testing.T) {
				orders, quotes := 0, 0
				base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(http.ResponseWriter, *http.Request) { orders++ }, nil)
				c := newFloatingRegionClient(t, "other-region", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/price" {
						base.ServeHTTP(w, r)
						return
					}
					quotes++
					if quotes == 1 {
						_, _ = w.Write([]byte(quoteServerFixture))
						return
					}
					w.WriteHeader(status)
					if status == 200 {
						_, _ = w.Write([]byte(`{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":0}`))
					} else {
						_, _ = w.Write([]byte(`{"code":"QuoteUnavailable","message":"synthetic failure"}`))
					}
				}))
				in := validCreateServerInput()
				in.AttachFloatingIP, in.NoWait, in.MaxPrice = true, true, 467800
				if create {
					out, err := c.CreateServer(context.Background(), in)
					if out != nil || err == nil {
						t.Fatalf("output=%v error=%v", out, err)
					}
					assertFloatingQuoteError(t, err, status)
				} else {
					out, err := c.QuoteCreateServer(context.Background(), in)
					if out != nil || err == nil {
						t.Fatalf("output=%v error=%v", out, err)
					}
					assertFloatingQuoteError(t, err, status)
				}
				wantQuotes := 2
				if status == 401 {
					wantQuotes++
				}
				if quotes != wantQuotes || orders != 0 {
					t.Fatalf("quotes=%d orders=%d", quotes, orders)
				}
			})
		}
	}
}

func TestFloatingQuoteConservativeTotalsAndProperties(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(strconv.FormatBool(populated), func(t *testing.T) {
			calls := 0
			c := newFloatingRegionClient(t, "other-region", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte(`{"optimumPrice":90,"originalPrice":100,"discountPrice":10,"discountPercent":10,"propertiesPrice":[{"name":"SERVER","description":"keep","optimumPrice":90,"monthlyPrice":100,"currentPrice":80,"discountPercent":10}]}`))
					return
				}
				properties := `[]`
				if populated {
					properties = `[{"name":"Elastic IP","optimumPrice":160,"monthlyPrice":200,"currentPrice":150,"discountPercent":20}]`
				}
				_, _ = fmt.Fprintf(w, `{"optimumPrice":160,"originalPrice":200,"discountPrice":40,"discountPercent":20,"propertiesPrice":%s}`, properties)
			}))
			in := pricedServerInput()
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			if err != nil || out == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if calls != 2 || out.OptimumPrice != 250 || out.OriginalPrice != 300 || out.DiscountPrice != 50 || out.DiscountPercent != 100*(float64(50)/300) {
				t.Fatalf("calls=%d quote=%+v", calls, out)
			}
			want := []pricing.PriceProperty{
				{Name: "SERVER", Description: "keep", OptimumPrice: 90, MonthlyPrice: 100, CurrentPrice: vngcloud.Ptr(float64(80)), DiscountPercent: 10},
				{Name: "FLOATING IP", Description: "Conservative Elastic IP estimate; extra create charge unconfirmed", OptimumPrice: 160, MonthlyPrice: 160, DiscountPercent: 20},
			}
			if populated {
				want[1].Name = "FLOATING IP: Elastic IP"
				want[1].MonthlyPrice = 200
				want[1].CurrentPrice = vngcloud.Ptr(float64(150))
			}
			if !reflect.DeepEqual(out.Properties, want) {
				t.Fatalf("properties=%+v want=%+v", out.Properties, want)
			}
		})
	}
}

// pricedServerInput holds only the fields that change a server's price.
func pricedServerInput() *CreateServerInput {
	return &CreateServerInput{
		ZoneID:         "zone-1",
		FlavorID:       "flavor-1",
		ImageID:        "image-1",
		RootDiskSize:   20,
		RootDiskTypeID: "voltype-1",
	}
}

func quoteInfoOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, raw)
	}
	info, ok := body["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing: %s", raw)
	}
	return info
}

func serverQuoteRecorder(t *testing.T, bodies *[][]byte) http.HandlerFunc {
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/price" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			mu.Lock()
			*bodies = append(*bodies, data)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(data))
		}
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, staticGetServer(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
	}
}

func TestQuoteCreateServerPricedFieldsOnlySendsPricedBody(t *testing.T) {
	var bodies [][]byte
	c := newTestClient(t, serverQuoteRecorder(t, &bodies))
	out, err := c.QuoteCreateServer(context.Background(), pricedServerInput())
	if err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
	if out.OptimumPrice != 347800 {
		t.Fatalf("OptimumPrice = %v, want 347800", out.OptimumPrice)
	}
	var body map[string]any
	if err := json.Unmarshal(bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	if body["resourceType"] != "server" || body["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	want := map[string]any{
		"zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1",
		"rootDiskSize": float64(20), "rootDiskTypeId": "voltype-1",
		"encryptionVolume": false, "period": float64(1), "isPoc": false,
	}
	if got := quoteInfoOf(t, bodies[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("resourceInfo = %v, want %v", got, want)
	}
}

func TestQuoteCreateServerSetUnpricedFieldsStayOutOfBody(t *testing.T) {
	var bodies [][]byte
	c := newTestClient(t, serverQuoteRecorder(t, &bodies))
	in := validCreateServerInput()
	in.DataDiskSize = 80
	in.DataDiskTypeID = "voltype-2"
	in.DataDiskName = "data"
	in.ServerGroupID = "group-1"
	in.AutoRenew = true
	in.UserData = "#!/bin/sh\necho supersecret"
	if _, err := c.QuoteCreateServer(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
	want := map[string]any{
		"zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1",
		"rootDiskSize": float64(20), "rootDiskTypeId": "voltype-1",
		"dataDiskSize": float64(80), "dataDiskTypeId": "voltype-2",
		"encryptionVolume": false, "period": float64(1), "isPoc": false,
	}
	if got := quoteInfoOf(t, bodies[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("resourceInfo = %v, want %v", got, want)
	}
	if bytes.Contains(bodies[0], []byte("supersecret")) {
		t.Fatalf("quote body leaked user data: %s", bodies[0])
	}
}

func TestQuoteCreateServerRequiresPricedFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	fields := map[string]func(in *CreateServerInput){
		"ZoneID":         func(in *CreateServerInput) { in.ZoneID = "" },
		"FlavorID":       func(in *CreateServerInput) { in.FlavorID = "" },
		"ImageID":        func(in *CreateServerInput) { in.ImageID = "" },
		"RootDiskSize":   func(in *CreateServerInput) { in.RootDiskSize = 0 },
		"RootDiskTypeID": func(in *CreateServerInput) { in.RootDiskTypeID = "" },
	}
	for name, zero := range fields {
		in := pricedServerInput()
		zero(in)
		if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("%s empty: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := c.QuoteCreateServer(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input: err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteCreateServerOptionalFieldsStillShapeChecked(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	fields := map[string]func(in *CreateServerInput, v string){
		"VPCID":               func(in *CreateServerInput, v string) { in.VPCID = v },
		"SubnetID":            func(in *CreateServerInput, v string) { in.SubnetID = v },
		"SSHKeyID":            func(in *CreateServerInput, v string) { in.SSHKeyID = v },
		"ServerGroupID":       func(in *CreateServerInput, v string) { in.ServerGroupID = v },
		"SecurityGroupIDs[0]": func(in *CreateServerInput, v string) { in.SecurityGroupIDs = []string{"sg-1", v} },
	}
	for name, set := range fields {
		for _, bad := range []string{"..", ".", "/", "?"} {
			in := pricedServerInput()
			set(in, bad)
			if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("%s=%q: err = %v, want ErrInvalidInput", name, bad, err)
			}
		}
	}
}

func TestCreateServerStillRequiresUnpricedFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	fields := map[string]func(in *CreateServerInput){
		"Name":                    func(in *CreateServerInput) { in.Name = "" },
		"VPCID":                   func(in *CreateServerInput) { in.VPCID = "" },
		"SubnetID":                func(in *CreateServerInput) { in.SubnetID = "" },
		"SSHKeyID":                func(in *CreateServerInput) { in.SSHKeyID = "" },
		"SecurityGroupIDs nil":    func(in *CreateServerInput) { in.SecurityGroupIDs = nil },
		"SecurityGroupIDs empty":  func(in *CreateServerInput) { in.SecurityGroupIDs = []string{} },
		"SecurityGroupIDs bad id": func(in *CreateServerInput) { in.SecurityGroupIDs = []string{"sg-1", "/"} },
	}
	for name, zero := range fields {
		in := validCreateServerInput()
		in.MaxPrice = 347800
		zero(in)
		if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("%s: err = %v, want ErrInvalidInput", name, err)
		}
		if _, err := buildCreateServerBody("op", in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("%s: build err = %v, want ErrInvalidInput", name, err)
		}
	}
}

// TestCreateServerGuardQuotesSamePricedBodyAsQuote checks that the quote
// the price guard sends is byte for byte the request QuoteCreateServer
// sends for the same Input, so the quote command and the guard price one
// request.
func TestCreateServerGuardQuotesSamePricedBodyAsQuote(t *testing.T) {
	var bodies [][]byte
	c := withInstantSleep(newTestClient(t, serverQuoteRecorder(t, &bodies)))
	in := validCreateServerInput()
	in.DataDiskSize = 80
	in.DataDiskTypeID = "voltype-2"
	in.DataDiskName = "data"
	in.ServerGroupID = "group-1"
	in.AutoRenew = true
	in.SSHKeyID = ""
	in.UserData = "#!/bin/sh\necho supersecret"
	in.MaxPrice = 347800

	if _, err := c.QuoteCreateServer(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateServer() error = %v", err)
	}
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("price requests = %d, want 2", len(bodies))
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("guard body = %s\nquote body = %s", bodies[1], bodies[0])
	}
}

// TestCreateServerCreateBodyKeepsUnpricedFields checks that the order still
// carries every field the quote leaves out.
func TestCreateServerCreateBodyKeepsUnpricedFields(t *testing.T) {
	body, err := buildCreateServerBody("op", validCreateServerInput())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "networkId", "subnetId", "securityGroup", "sshKeyId", "isEnableAutoRenew"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("create body lacks %q: %s", k, raw)
		}
	}
}

// TestServerQuoteBodyKeysMatchCreateBody builds the quote and the create
// from an Input with every priced field set and checks that every quote
// key reaches the create with the same value. A priced field added only
// to the create body would let the quote understate the bill.
func TestServerQuoteBodyKeysMatchCreateBody(t *testing.T) {
	in := &CreateServerInput{
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
		DataDiskSize:     80,
		DataDiskTypeID:   "voltype-2",
		DataDiskName:     "data",

		RootDiskEncryptionTypeID: "aes-xts-plain64_256",
		DataDiskEncryptionTypeID: "aes-xts-plain64_128",
		ServerGroupID:            "group-1",
		AutoRenew:                true,
		UserData:                 "#!/bin/sh\necho hi",
		MaxPrice:                 1,
		NoWait:                   true,
		AttachFloatingIP:         true,
		AllowOpenLogin:           true,
	}
	testutil.RequireAllFieldsSet(t, in)
	info, err := buildServerQuoteInfo("op", in)
	if err != nil {
		t.Fatal(err)
	}
	in.SSHKeyID = ""
	body, err := buildCreateServerBody("op", in)
	if err != nil {
		t.Fatal(err)
	}
	if info["encryptionVolume"] != true {
		t.Fatalf("quote encryptionVolume = %v, want true", info["encryptionVolume"])
	}
	testutil.RequireQuoteKeysInCreate(t, info, body, "period", "isPoc")
}
