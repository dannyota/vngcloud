package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEndpointCatalogInvalidFirstPageStopsScan(t *testing.T) {
	f := newEPFake()
	f.rewrap = func(r *http.Request, resp *http.Response) {
		if !strings.HasSuffix(r.URL.Path, "/packages") {
			return
		}
		var params struct {
			Page int `json:"page"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("params")), &params)
		row := `{"uuid":"` + epPackage + `","serviceName":"vstorage.hcm03","currencyUnit":"VND"}`
		if params.Page == 2 {
			row = `{"uuid":"00000000-0000-0000-0000-0000000000b2","name":"Other","serviceName":"vstorage.hcm03","currencyUnit":"VND"}`
		}
		body := fmt.Sprintf(`{"success":true,"page":%d,"size":1,"totalPage":2,"total":2,"data":[%s]}`, params.Page, row)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(strings.NewReader(body))
	}
	out, err := newEPHarness(t, f).c.CreateEndpoint(context.Background(), epCreateInput())
	if err == nil || out != nil || f.count(http.MethodGet, "/packages") != 1 || f.count(http.MethodPost, "/price") != 0 {
		t.Fatalf("out=%v err=%v package reads=%d", out, err, f.count(http.MethodGet, "/packages"))
	}
}

func TestEndpointCatalogStopsWhenSelectedFieldsExceedBudget(t *testing.T) {
	f := newEPFake()
	large := strings.Repeat("x", 2200000)
	f.rewrap = func(r *http.Request, resp *http.Response) {
		if !strings.HasSuffix(r.URL.Path, "/packages") {
			return
		}
		var params struct {
			Page int `json:"page"`
		}
		_ = json.Unmarshal([]byte(r.URL.Query().Get("params")), &params)
		body := fmt.Sprintf(`{"success":true,"page":%d,"size":1,"totalPage":3,"total":3,"data":[{"uuid":"00000000-0000-0000-0000-%012d","name":"%s","serviceName":"vstorage.hcm03","currencyUnit":"VND"}]}`, params.Page, params.Page, large)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(strings.NewReader(body))
	}
	out, err := newEPHarness(t, f).c.CreateEndpoint(context.Background(), epCreateInput())
	if err == nil || out != nil || f.count(http.MethodGet, "/packages") != 2 || f.count(http.MethodPost, "/price") != 0 {
		t.Fatalf("out=%v err=%v package reads=%d", out, err, f.count(http.MethodGet, "/packages"))
	}
}

func TestEndpointPackagePages(t *testing.T) {
	first := `{"uuid":"` + epPackage + `","name":"Standard","serviceName":"vstorage.hcm03","currencyUnit":"VND"}`
	for _, tc := range []struct {
		name, second string
		ok           bool
	}{
		{"distinct", `{"uuid":"00000000-0000-0000-0000-0000000000b2","name":"Premium","serviceName":"vstorage.hcm03","currencyUnit":"VND"}`, true},
		{"duplicate id", first, false},
		{"duplicate name", `{"uuid":"00000000-0000-0000-0000-0000000000b2","name":"Standard","serviceName":"vstorage.hcm03","currencyUnit":"VND"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.rewrap = func(r *http.Request, resp *http.Response) {
				if !strings.HasSuffix(r.URL.Path, "/packages") {
					return
				}
				var params struct {
					Page int `json:"page"`
				}
				if json.Unmarshal([]byte(r.URL.Query().Get("params")), &params) != nil {
					t.Error("bad params")
					return
				}
				row := first
				if params.Page == 2 {
					row = tc.second
				}
				body := fmt.Sprintf(`{"success":true,"page":%d,"size":1,"totalPage":2,"total":2,"data":[%s]}`, params.Page, row)
				_ = resp.Body.Close()
				resp.Body = io.NopCloser(strings.NewReader(body))
			}
			_, err := newEPHarness(t, f).c.QuoteCreateEndpoint(context.Background(), epCreateInput())
			if (err == nil) != tc.ok || f.count(http.MethodGet, "/packages") != 2 || f.count(http.MethodPost, "/price") != boolInt(tc.ok) {
				t.Fatalf("err=%v packages=%d quotes=%d", err, f.count(http.MethodGet, "/packages"), f.count(http.MethodPost, "/price"))
			}
		})
	}
}

func TestEndpointCatalogIncompleteMetadataRefused(t *testing.T) {
	for _, field := range []string{`"page":null`, `"nextPage":2`} {
		t.Run(field, func(t *testing.T) {
			f := newEPFake()
			f.packages = strings.Replace(f.packages, `"success":true`, `"success":true,`+field, 1)
			_, err := newEPHarness(t, f).c.QuoteCreateEndpoint(context.Background(), epCreateInput())
			if err == nil || f.count(http.MethodPost, "/price") != 0 {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEndpointCreateRefusesUnrecognizedPageMarkers(t *testing.T) {
	for _, marker := range []string{`"totalPages":2`, `"continuationToken":"more"`, `"ContinuationToken":"more"`, `"hasMore":true`, `"nextPage":2`} {
		t.Run(marker, func(t *testing.T) {
			f := epCreateFake()
			f.packages = strings.Replace(f.packages, `"success":true`, `"success":true,`+marker, 1)
			f.rewrap = func(r *http.Request, resp *http.Response) {
				if strings.HasSuffix(r.URL.Path, "/packages") && strings.Contains(r.URL.Query().Get("params"), `"page":2`) {
					_ = resp.Body.Close()
					resp.Body = io.NopCloser(strings.NewReader(`{"success":true,"data":[{"uuid":"00000000-0000-0000-0000-0000000000b2","name":"Standard","serviceName":"vstorage.hcm03","currencyUnit":"VND"}]}`))
				}
			}
			out, err := newEPHarness(t, f).c.CreateEndpoint(context.Background(), epCreateInput())
			if err == nil || out != nil || f.count(http.MethodPost, "/price") != 0 || f.count(http.MethodPost, epBase) != 0 {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestEndpointCreateRefusesContradictoryFinalPage(t *testing.T) {
	for _, marker := range []string{`"hasMore":true`, `"nextPage":2`, `"totalPages":2`} {
		t.Run(marker, func(t *testing.T) {
			f := epCreateFake()
			f.packages = strings.Replace(f.packages, `"success":true`, `"success":true,"page":1,"size":1,"total":1,"totalPage":1,`+marker, 1)
			out, err := newEPHarness(t, f).c.CreateEndpoint(context.Background(), epCreateInput())
			if err == nil || out != nil || f.count(http.MethodPost, "/price") != 0 || f.count(http.MethodPost, epBase) != 0 {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestEndpointSubnetPages(t *testing.T) {
	first := `{"uuid":"` + epSubnet + `","status":"ACTIVE","vpcUuid":"` + epVPC + `","zone":{"uuid":"HCM03-1C"}}`
	for _, tc := range []struct {
		name, second string
		ok           bool
	}{
		{"distinct", strings.Replace(first, epSubnet, "sub-00000000-0000-0000-0000-0000000000aa", 1), true},
		{"duplicate", first, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.rewrap = func(r *http.Request, resp *http.Response) {
				if !strings.HasSuffix(r.URL.Path, "/subnets") {
					return
				}
				var params struct {
					Page   int              `json:"page"`
					Search []endpointFilter `json:"search"`
				}
				if json.Unmarshal([]byte(r.URL.Query().Get("params")), &params) != nil || len(params.Search) != 2 || params.Search[1].Value != "HCM03-1C" {
					t.Error("missing subnet filters")
					return
				}
				row := first
				if params.Page == 2 {
					row = tc.second
				}
				body := fmt.Sprintf(`{"success":true,"page":%d,"size":1,"totalPage":2,"total":2,"data":[%s]}`, params.Page, row)
				_ = resp.Body.Close()
				resp.Body = io.NopCloser(strings.NewReader(body))
			}
			_, err := newEPHarness(t, f).c.QuoteCreateEndpoint(context.Background(), epCreateInput())
			if (err == nil) != tc.ok || f.count(http.MethodGet, "/subnets") != 2 || f.count(http.MethodPost, "/price") != boolInt(tc.ok) {
				t.Fatalf("err=%v subnet reads=%d", err, f.count(http.MethodGet, "/subnets"))
			}
		})
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
