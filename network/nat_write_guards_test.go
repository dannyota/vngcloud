package network

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

type natCustomProvider struct{}

func (natCustomProvider) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{AccessToken: "synthetic-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (natCustomProvider) Invalidate(string) {}

func TestNATWriteAuthGuards(t *testing.T) {
	for _, opt := range []vngcloud.LoadOption{vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithCredentialsProvider(natCustomProvider{}), vngcloud.WithRegion("hcm-3")} {
		calls := 0
		c := natWriteTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls++ }), opt)
		_, err := c.CreateNATInstance(context.Background(), natTestInput())
		if !errors.Is(err, vngcloud.ErrInvalidConfig) || calls != 0 {
			t.Fatalf("create guard %v, calls %d", err, calls)
		}
		_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
		if !errors.Is(err, vngcloud.ErrInvalidConfig) || calls != 0 {
			t.Fatalf("delete guard %v, calls %d", err, calls)
		}
	}
}
func TestNATCreateInputGuards(t *testing.T) {
	for _, price := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		calls := 0
		c := natWriteTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls++ }))
		in := natTestInput()
		in.MaxPrice = price
		_, err := c.CreateNATInstance(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) || calls != 0 {
			t.Fatalf("price guard %v, calls %d", err, calls)
		}
	}
	for _, field := range []string{"ZoneID", "AvailabilityZoneID", "PackageID", "VPCID"} {
		for _, value := range []string{"", "../escape"} {
			calls := 0
			c := natWriteTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls++ }))
			in := natTestInput()
			switch field {
			case "ZoneID":
				in.ZoneID = value
			case "AvailabilityZoneID":
				in.AvailabilityZoneID = value
			case "PackageID":
				in.PackageID = value
			case "VPCID":
				in.VPCID = value
			}
			_, err := c.CreateNATInstance(context.Background(), in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) || calls != 0 {
				t.Fatalf("%s guard %v, calls %d", field, err, calls)
			}
		}
	}
}
func TestNATPurchaseGuards(t *testing.T) {
	cases := []struct {
		name, path, body string
		want             error
		code             string
	}{
		{"whitelist false", "/v3-whitelist", `{"success":true,"data":{"enabledForAll":false}}`, vngcloud.ErrInvalidConfig, ""},
		{"whitelist absent", "/v3-whitelist", `{"success":true,"data":{"whitelistedPortalUserIds":["portal-secret-canary"]}}`, nil, "InvalidResponse"},
		{"whitelist null", "/v3-whitelist", `{"success":true,"data":{"enabledForAll":null}}`, nil, "InvalidResponse"},
		{"whitelist wrong type", "/v3-whitelist", `{"success":true,"data":{"enabledForAll":"true"}}`, nil, "InvalidResponse"},
		{"whitelist duplicate", "/v3-whitelist", `{"success":true,"data":{"enabledForAll":true,"enabledForAll":true}}`, nil, "InvalidResponse"},
		{"whitelist casing", "/v3-whitelist", `{"success":true,"data":{"EnabledForAll":true}}`, nil, "InvalidResponse"},
		{"disabled zone", "/zones", strings.Replace(natTestZones, `"isEnabled":true`, `"isEnabled":false`, 1), vngcloud.ErrInvalidInput, ""},
		{"unknown zone type", "/zones", strings.Replace(natTestZones, "AVAILABILITY", "FUTURE", 1), vngcloud.ErrInvalidInput, ""},
		{"absent zone", "/zones", `{"success":true,"code":200,"data":[]}`, vngcloud.ErrInvalidInput, ""},
		{"ambiguous zone", "/zones", `{"success":true,"code":200,"data":[{"uuid":"HAN01-1B","zoneType":"AVAILABILITY","isEnabled":true,"isDefault":false},{"uuid":"HAN01-1B","zoneType":"AVAILABILITY","isEnabled":true,"isDefault":false}]}`, vngcloud.ErrInvalidInput, ""},
		{"package not in zone", "/nat-package", strings.Replace(natTestPackages, "package-1", "package-2", 1), vngcloud.ErrInvalidInput, ""},
		{"wrong VPC zone", "/vpcs", strings.Replace(natTestVPCs, "HAN01-1B", "az-2", 1), vngcloud.ErrInvalidInput, ""},
		{"missing VPC zone", "/vpcs", strings.Replace(natTestVPCs, `"zones":[{"uuid":"HAN01-1B"}]`, `"zones":null`, 1), nil, "InvalidResponse"},
		{"wrong project", "/vpcs", strings.Replace(natTestVPCs, "pro-00000000-0000-0000-0000-000000000001", "project-2", 1), nil, "InvalidResponse"},
		{"VPC absent", "/vpcs", natInventory(""), vngcloud.ErrNotFound, ""},
		{"existing NAT VPC", "/nats", natInventory(strings.Replace(natTestRow, "example", "other", 1)), vngcloud.ErrInvalidInput, ""},
		{"duplicate name", "/nats", natInventory(strings.Replace(natTestRow, "vpc-1", "vpc-2", 1)), vngcloud.ErrInvalidInput, ""},
		{"incomplete inventory", "/nats", `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[]}`, nil, "InvalidResponse"},
		{"duplicate inventory ID", "/nats", `{"success":true,"page":1,"size":1000,"totalPage":1,"total":2,"data":[` + natTestRow + `,` + natTestRow + `]}`, nil, "InvalidResponse"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			prices := 0
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, tt.path) {
					_, _ = w.Write([]byte(tt.body))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/price") {
					prices++
				}
				s.serve(t, w, r)
			}))
			_, err := c.CreateNATInstance(context.Background(), natTestInput())
			if err == nil || s.orders != 0 || s.puts != 0 || prices != 0 {
				t.Fatalf("guard %v, writes %d/%d, prices %d", err, s.orders, s.puts, prices)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("sentinel: %v", err)
			}
			if tt.code != "" && vngcloud.ErrorCode(err) != tt.code {
				t.Errorf("code: %v", err)
			}
		})
	}
}
func TestNATPriceGuards(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cap   float64
		price string
		want  error
		valid bool
	}{
		{"zero cap", 0, natTestPrice, vngcloud.ErrPriceAboveMax, false},
		{"below quote", 99, natTestPrice, vngcloud.ErrPriceAboveMax, false},
		{"equal", 100, natTestPrice, nil, true},
		{"above quote", 101, natTestPrice, nil, true},
		{"zero quote", 100, strings.Replace(natTestPrice, `"optimumPrice":100`, `"optimumPrice":0`, 1), vngcloud.ErrUnpriced, false},
		{"negative quote", 100, strings.Replace(natTestPrice, `"optimumPrice":100`, `"optimumPrice":-1`, 1), vngcloud.ErrUnpriced, false},
		{"missing price", 100, strings.Replace(natTestPrice, `"optimumPrice":100,`, ``, 1), nil, false},
		{"null price", 100, strings.Replace(natTestPrice, `"optimumPrice":100`, `"optimumPrice":null`, 1), nil, false},
		{"overflow", 100, strings.Replace(natTestPrice, `"optimumPrice":100`, `"optimumPrice":1e999`, 1), nil, false},
		{"duplicate", 100, strings.Replace(natTestPrice, `"optimumPrice":100`, `"optimumPrice":100,"optimumPrice":100`, 1), nil, false},
		{"case alias", 100, strings.Replace(natTestPrice, `"optimumPrice":100`, `"OptimumPrice":100`, 1), nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/price") {
					_, _ = w.Write([]byte(tt.price))
					return
				}
				s.serve(t, w, r)
			}))
			in := natTestInput()
			in.MaxPrice = tt.cap
			_, err := c.CreateNATInstance(context.Background(), in)
			if tt.valid {
				if err != nil || s.orders != 1 {
					t.Fatalf("%v", err)
				}
			} else {
				if err == nil || s.orders != 0 {
					t.Fatalf("%v, orders %d", err, s.orders)
				}
				if tt.want != nil && !errors.Is(err, tt.want) {
					t.Errorf("%v", err)
				}
			}
		})
	}
}
func TestNATChangingScans(t *testing.T) {
	for _, resource := range []string{"/nats", "/vpcs"} {
		t.Run(resource, func(t *testing.T) {
			s := new(natScenario)
			calls := 0
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, resource) {
					calls++
					row := natTestRow
					if resource == "/vpcs" {
						row = `{"uuid":"vpc-other","projectId":"66b500000000000000000002","projectUuid":"pro-00000000-0000-0000-0000-000000000001","regionId":"66b500000000000000000001","zones":[]}`
					}
					body := `{"success":true,"page":1,"size":1,"totalPage":2,"total":2,"data":[` + row + `]}`
					if calls > 1 {
						body = strings.Replace(body, `"page":1`, `"page":2`, 1)
						body = strings.Replace(body, `"total":2`, `"total":3`, 1)
					}
					_, _ = w.Write([]byte(body))
					return
				}
				s.serve(t, w, r)
			}))
			_, err := c.CreateNATInstance(context.Background(), natTestInput())
			if vngcloud.ErrorCode(err) != "InvalidResponse" || s.orders != 0 || calls != 2 {
				t.Fatalf("%v, calls %d", err, calls)
			}
		})
	}
}
