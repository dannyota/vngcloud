//go:build livewrite

package livetest_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

func TestNATHarnessUnprovenReplacementIsNotDeleted(t *testing.T) {
	const project = "pro-00000000-0000-0000-0000-000000000001"
	const zone = "66b500000000000000000001"
	const row = `{"uuid":%q,"natName":"example","status":"PROVISIONING","projectUuid":"` + project + `","zoneUuid":"HAN01-1B","natPackage":{"uuid":"package-1"},"vpc":{"uuid":"vpc-1","projectId":"66b500000000000000000002","projectUuid":null,"regionUuid":null,"regionId":"` + zone + `"}}`
	ordered, polls, deletes := false, 0, 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := ""
		switch {
		case strings.HasSuffix(r.URL.Path, "/v3-whitelist"):
			body = `{"code":200,"success":true,"data":{"enabledForAll":true,"whitelistedPortalUserIds":[]}}`
		case strings.HasSuffix(r.URL.Path, "/zones"):
			body = `{"code":200,"success":true,"data":[{"uuid":"HAN01-1B","name":"Example","zoneType":"AVAILABILITY","isEnabled":true,"isDefault":false,"description":"Example"}]}`
		case strings.HasSuffix(r.URL.Path, "/nat-package"):
			body = `{"code":200,"success":true,"data":[{"uuid":"package-1","name":"Standard","packageId":"standard","resourceServiceId":"service-1","billingSku":"nat.s-standard","serviceName":"nat","description":null,"currencyUnit":"VND","createdAt":"2026-01-01","isDefault":true,"monthlyPrice":100,"price":{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"discountPercent":0}}]}`
		case strings.HasSuffix(r.URL.Path, "/vpcs"):
			body = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"vpc-1","projectId":"66b500000000000000000002","regionId":"` + zone + `","zones":[{"uuid":"HAN01-1B"}],"projectUuid":"` + project + `","regionUuid":"` + zone + `"}]}`
		case strings.HasSuffix(r.URL.Path, "/price"):
			body = `{"code":0,"success":true,"data":{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"discountPercent":null,"propertiesPrice":[{"optimumPrice":100,"monthlyPrice":100,"currentPrice":100,"discountPercent":0,"name":"Standard","description":"Example"}]}}`
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/nats/"):
			deletes++
			body = `{"code":0,"success":true}`
		case strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodPost:
			ordered = true
			w.WriteHeader(http.StatusCreated)
			body = `{"code":0,"success":true}`
		case strings.HasSuffix(r.URL.Path, "/nats"):
			if !ordered {
				body = `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0,"data":[]}`
			} else {
				polls++
				id := "nat-b"
				if polls == 1 {
					id = "nat-a"
				}
				body = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[` + fmt.Sprintf(row, id) + `]}`
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer api.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"synthetic-token","expiresIn":3600}`))
	}))
	defer token.Close()
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Redirect(w, r, token.URL+"/callback?code=synthetic-code", http.StatusFound)
		} else {
			_, _ = w.Write([]byte(`<input name="_csrf" value="synthetic-csrf">`))
		}
	}))
	defer signin.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithProjectID(project), vngcloud.WithRetry(0, 0), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: api.URL + "/", VServer: api.URL + "/", Billing: api.URL + "/"}))
	if err != nil {
		t.Fatal(err)
	}
	client := network.New(cfg)
	out, err := client.CreateNATInstance(context.Background(), &network.CreateNATInstanceInput{Name: "example", ZoneID: zone, AvailabilityZoneID: "HAN01-1B", PackageID: "package-1", VPCID: "vpc-1", MaxPrice: 100})
	if !errors.Is(err, network.ErrNotSettled) || out == nil || out.NATInstance != nil || polls != 2 {
		t.Fatalf("unexpected SDK result: %v polls=%d", err, polls)
	}
	report := map[string]any{}
	if liveNATCleanupDelete(context.Background(), client, report, nil, "example", project, zone, "vpc-1", "package-1", "", false) {
		t.Fatal("unproven NAT was deleted")
	}
	if deletes != 0 || polls != 3 || !reflect.DeepEqual(report["cleanupCandidateIDs"], []string{"nat-b"}) {
		t.Fatalf("deletes=%d polls=%d candidates=%v", deletes, polls, report["cleanupCandidateIDs"])
	}
}
