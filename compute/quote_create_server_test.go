package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

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
// from one fully populated Input and checks that every priced key the quote
// sends reaches the create with the same value. A priced field added only
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
	}
	testutil.RequireAllFieldsSet(t, in)
	info, err := buildServerQuoteInfo("op", in)
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildCreateServerBody("op", in)
	if err != nil {
		t.Fatal(err)
	}
	if info["encryptionVolume"] != true {
		t.Fatalf("quote encryptionVolume = %v, want true", info["encryptionVolume"])
	}
	testutil.RequireQuoteKeysInCreate(t, info, body, "period", "isPoc")
}
