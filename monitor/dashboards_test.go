package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
)

func TestListDashboardsDefaults(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/vmonitor-api/api/v1/dashboards" || r.URL.Query().Get("filter") != "system:false" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		_, _ = fmt.Fprint(w, `{"lstData":[],"page":1,"pageSize":10,"totalItem":0,"totalPage":0}`)
	}))
	defer s.Close()
	c := New(lifecycleProfileConfig(t, s, "false"))
	out, err := c.ListDashboards(context.Background(), nil)
	if err != nil || out == nil || len(out.Items) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestDashboardFixtures(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		system  bool
		detail  bool
	}{{"list_dashboards_system", true, false}, {"list_dashboards_custom_empty", false, false}, {"get_dashboard_system", true, true}} {
		t.Run(tc.fixture, func(t *testing.T) {
			data, err := os.ReadFile("../testdata/monitor/" + tc.fixture + ".json")
			if err != nil {
				t.Fatal(err)
			}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("method")
				}
				if tc.detail {
					if r.URL.RequestURI() != "/vmonitor-api/api/v1/dashboards/dash-1" {
						t.Error(r.URL)
					}
				} else {
					q := r.URL.Query()
					filter := "system:false"
					if tc.system {
						filter = "systemType:productVng"
					}
					if r.URL.Path != "/vmonitor-api/api/v1/dashboards" || len(q) != 5 || q.Get("filter") != filter || q.Get("page") != "1" || q.Get("size") != "10" || !q.Has("searching-field") || !q.Has("searching-text") {
						t.Error(r.URL)
					}
				}
				_, _ = w.Write(data)
			}))
			defer s.Close()
			c := New(lifecycleProfileConfig(t, s, "false"))
			if tc.detail {
				out, e := c.GetDashboard(context.Background(), &GetDashboardInput{DashboardID: "dash-1"})
				if e != nil || out.Dashboard != (Dashboard{ID: "dash-1", Name: "Synthetic", System: true, Favorite: true, CreatedAt: "created", UpdatedAt: "updated"}) {
					t.Fatalf("%+v %v", out, e)
				}
			} else {
				out, e := c.ListDashboards(context.Background(), &ListDashboardsInput{System: tc.system})
				if e != nil {
					t.Fatal(e)
				}
				if tc.system && (len(out.Items) != 1 || out.Items[0].ID != "dash-1") || !tc.system && len(out.Items) != 0 {
					t.Fatal(out)
				}
			}
		})
	}
}

func TestDashboardInvalidResponses(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"data":{"id":"dash-1","system":true}}`, `{}`, `{"Id":"dash-1","system":true}`, `{"id":"","system":true}`, `{"id":"dash-1"}`, `{"id":"dash-1","system":null}`, `{"id":"dash-1","system":1}`, `{"id":"other","system":true}`, `{"id":"dash-1","id":"dash-1","system":true}`, `{"id":"dash-1","system":true,"extra":{"x":1,"x":2}}`, `{"id":"dash-1","system":true,"name":"synthetic-token"}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer s.Close()
			out, err := New(lifecycleProfileConfig(t, s, "false")).GetDashboard(context.Background(), &GetDashboardInput{DashboardID: "dash-1"})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatalf("%v %v", out, err)
			}
		})
	}
	for _, body := range []string{`{}`, `{"lstData":null,"page":1,"pageSize":10,"totalItem":0,"totalPage":0}`, `{"lstData":[],"page":2,"pageSize":10,"totalItem":0,"totalPage":0}`, `{"lstData":[],"page":1,"pageSize":9,"totalItem":0,"totalPage":0}`, `{"lstData":[],"page":1,"pageSize":10,"totalItem":-1,"totalPage":0}`, `{"lstData":[],"page":1,"pageSize":10,"totalItem":0,"totalPage":1.5}`, `{"lstData":[{"id":"x","system":true}],"page":1,"pageSize":10,"totalItem":1,"totalPage":1}`, `{"lstData":[{"id":"x","system":false},{"id":"x","system":false}],"page":1,"pageSize":10,"totalItem":2,"totalPage":1}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer s.Close()
			out, err := New(lifecycleProfileConfig(t, s, "false")).ListDashboards(context.Background(), nil)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("%v %v", out, err)
			}
		})
	}
}

func TestDashboardInputAndOversize(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = fmt.Fprint(w, `{"id":"dash-1","system":true,"x":"`+strings.Repeat("x", 4<<20)+`"}`)
	}))
	defer s.Close()
	c := New(lifecycleProfileConfig(t, s, "false"))
	for _, in := range []*ListDashboardsInput{{Page: -1}, {Size: -1}, {Size: 101}} {
		if out, err := c.ListDashboards(context.Background(), in); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatal(out, err)
		}
	}
	for _, in := range []*GetDashboardInput{nil, {}, {DashboardID: "../secret"}} {
		if out, err := c.GetDashboard(context.Background(), in); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || strings.Contains(err.Error(), "secret") {
			t.Fatal(out, err)
		}
	}
	if requests != 0 {
		t.Fatal(requests)
	}
	out, err := c.GetDashboard(context.Background(), &GetDashboardInput{DashboardID: "dash-1"})
	var api *vngcloud.APIError
	if out != nil || !errors.Is(err, transport.ErrBodyTooLarge) || !errors.As(err, &api) || api.StatusCode != 200 || api.Operation != "monitor.GetDashboard" {
		t.Fatal(out, err)
	}
}

func TestDashboardRefreshReflection(t *testing.T) {
	for _, field := range []string{"name", "createdAt", "updatedAt"} {
		for _, credential := range []string{"first-secret", "second-secret"} {
			t.Run(field+credential, func(t *testing.T) {
				c := monitorRefreshClient(t, "first-secret", "second-secret", "/vmonitor-api/api/v1/dashboards/dash-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"id":"dash-1","system":true,%q:%q}`, field, credential)
				}))
				out, err := c.GetDashboard(context.Background(), &GetDashboardInput{DashboardID: "dash-1"})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
					t.Fatal(out, err)
				}
				assertMonitorCredentialsAbsent(t, out, err, "first-secret", "second-secret")
			})
		}
	}
}

func TestDashboardStatusesAndRedirect(t *testing.T) {
	for _, status := range []int{301, 401, 403, 404, 429, 500, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			destinationCalls := 0
			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls++ }))
			defer destination.Close()
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"code":"SECRET-MARKER","message":"SECRET-MARKER"}`)
			}))
			defer s.Close()
			c := New(lifecycleProfileConfig(t, s, "false"))
			out, err := c.GetDashboard(context.Background(), &GetDashboardInput{DashboardID: "dash-1"})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != status || strings.Contains(fmt.Sprintf("%+v", err), "SECRET-MARKER") || destinationCalls != 0 {
				t.Fatal(out, err, destinationCalls)
			}
		})
	}
}

func TestDashboardListRowLimitAndShapes(t *testing.T) {
	for _, body := range []string{
		`{"lstData":[{"id":"a","system":false},{"id":"b","system":false}],"page":1,"pageSize":1,"totalItem":2,"totalPage":2}`,
		`{"lstData":[{"id":"a","system":false,"system":false}],"page":1,"pageSize":1,"totalItem":1,"totalPage":1}`,
		`{"lstData":[{"id":"a","system":false,"System":true}],"page":1,"pageSize":1,"totalItem":1,"totalPage":1}`,
		`{"lstData":[{"id":"a","System":false}],"page":1,"pageSize":1,"totalItem":1,"totalPage":1}`,
		`{"lstData":[{"id":"a","system":"false"}],"page":1,"pageSize":1,"totalItem":1,"totalPage":1}`,
		`{"lstData":[],"page":1,"pageSize":1,"totalItem":0,"totalPage":-1}`,
		`{"data":{"lstData":[],"page":1,"pageSize":1,"totalItem":0,"totalPage":0}}`, `[]`, `null`,
	} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer s.Close()
			out, err := New(lifecycleProfileConfig(t, s, "false")).ListDashboards(context.Background(), &ListDashboardsInput{Size: 1})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatal(out, err)
			}
		})
	}
}

func TestDashboardListRefreshReflection(t *testing.T) {
	for _, field := range []string{"id", "name", "createdAt", "updatedAt"} {
		for _, credential := range []string{"first-secret", "second-secret"} {
			t.Run(field+credential, func(t *testing.T) {
				c := monitorRefreshClient(t, "first-secret", "second-secret", "/vmonitor-api/api/v1/dashboards", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					id := `"id":"dash-1",`
					if field == "id" {
						id = ""
					}
					_, _ = fmt.Fprintf(w, `{"lstData":[{%s"system":false,%q:%q}],"page":1,"pageSize":10,"totalItem":1,"totalPage":1}`, id, field, credential)
				}))
				out, err := c.ListDashboards(context.Background(), nil)
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
					t.Fatal(out, err)
				}
				assertMonitorCredentialsAbsent(t, out, err, "first-secret", "second-secret")
			})
		}
	}
}
