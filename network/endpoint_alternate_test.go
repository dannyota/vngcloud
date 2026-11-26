package network

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestEndpointSelectorRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*CreateEndpointInput, *epFake)
	}{
		{"unknown zone", func(in *CreateEndpointInput, _ *epFake) { in.AvailabilityZoneID = "HCM03-2A" }},
		{"unknown category", func(in *CreateEndpointInput, _ *epFake) { in.CategoryID = "66b5000000000000000000ff" }},
		{"unknown service", func(in *CreateEndpointInput, _ *epFake) { in.ServiceID = "00000000-0000-0000-0000-0000000000ff" }},
		{"unknown package", func(in *CreateEndpointInput, _ *epFake) { in.PackageID = "00000000-0000-0000-0000-0000000000ff" }},
		{"duplicate service ID", func(in *CreateEndpointInput, f *epFake) {
			in.ServiceID = epService
			f.services = strings.Replace(f.services, `"uuid":"00000000-0000-0000-0000-0000000000f1"`, `"uuid":"`+epService+`"`, 1)
		}},
		{"duplicate package name", func(in *CreateEndpointInput, f *epFake) {
			in.PackageID = epPackage
			f.packages = strings.Replace(f.packages, `]}`, `,{"uuid":"00000000-0000-0000-0000-0000000000ff","name":"Standard","serviceName":"vstorage.hcm03","currencyUnit":"VND"}]}`, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			in := epCreateInput()
			tc.apply(in, f)
			_, err := newEPHarness(t, f).c.QuoteCreateEndpoint(context.Background(), in)
			if err == nil || f.count(http.MethodPost, "/price") != 0 {
				t.Fatalf("err=%v quoted=%d", err, f.count(http.MethodPost, "/price"))
			}
		})
	}
}

func TestEndpointAlternativeOffer(t *testing.T) {
	f := epCreateFake()
	f.zones = strings.ReplaceAll(f.zones, `"isEnabled":false`, `"isEnabled":true`)
	f.services = strings.ReplaceAll(f.services, "vstorage.hcm03", "alternate")
	f.packages = strings.ReplaceAll(strings.ReplaceAll(f.packages, "vstorage.hcm03", "alternate"), "Standard", "Premium")
	f.subnets = strings.ReplaceAll(f.subnets, "HCM03-1C", "HCM03-1B")
	f.price = strings.ReplaceAll(f.price, "Standard", "Premium")
	f.list = func(n int) string {
		if n == 1 {
			return epList()
		}
		row := strings.ReplaceAll(epRow(epID, "demo", "ACTIVE", epSubnet), "HCM03-1C", "HCM03-1B")
		return epList(strings.ReplaceAll(row, "vstorage.hcm03", "alternate"))
	}
	in := epCreateInput()
	values := map[string]string{"AvailabilityZoneID": "HCM03-1B", "CategoryID": epCategory, "ServiceID": epService, "PackageID": epPackage}
	for name, value := range values {
		field := reflect.ValueOf(in).Elem().FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("missing selector %s", name)
		}
		field.SetString(value)
	}
	h := newEPHarness(t, f)
	if out, err := h.c.QuoteCreateEndpoint(context.Background(), in); err != nil || out.MonthlyPrice != 712400 {
		t.Fatalf("quote=%v err=%v", out, err)
	}
	if out, err := h.c.CreateEndpoint(context.Background(), in); err != nil || out.Status != "ACTIVE" {
		t.Fatalf("create=%v err=%v", out, err)
	}
	if f.count(http.MethodPost, epBase) != 1 {
		t.Fatal("create not sent")
	}
}
