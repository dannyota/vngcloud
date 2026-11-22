package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestDatabaseReadOnlyProfile(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"code":200,"data":[]}`)) }))
	defer server.Close()
	withTestOptions(t, vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}))
	root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--profile", "agent", "database", "list-database-zones"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseReadCommands(t *testing.T) {
	cases := []struct {
		name, path, body string
		args             []string
	}{
		{"list-database-engine-versions", "datastore", `{"code":200,"data":[]}`, nil},
		{"list-database-zones", "zones", `{"code":200,"data":[]}`, nil},
		{"list-database-flavors", "flavors", `{"code":200,"data":[]}`, []string{"--engine", "mysql", "--engine-version", "8.0", "--zone-id", "zone"}},
		{"list-database-storage-types", "volume/types", `{"code":200,"data":{"data":[]}}`, []string{"--zone-id", "zone"}},
		{"list-database-networks", "networks/subnets", `{"code":200,"data":[]}`, []string{"--zone-id", "zone"}},
		{"list-database-instances", "", `{"code":200,"data":{"data":[],"pageObject":{}}}`, nil},
		{"get-database-instance", "id/instance", `{"code":200,"data":{"id":"instance","name":"db","status":"ACTIVE"}}`, []string{"--id", "instance"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCleanEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expected := "/vdb-relational/v1/database-instances"
				if tc.path != "" {
					expected += "/" + tc.path
				}
				if r.URL.Path != expected || r.Method != "GET" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			withTestOptions(t, vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: server.URL}))
			root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			root.SetArgs(append([]string{"--region", "hcm-3", "--read-only", "database", tc.name}, tc.args...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			help := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			help.SetArgs([]string{"database", tc.name, "--help"})
			if err := help.Execute(); err != nil && !strings.Contains(err.Error(), "help") {
				t.Fatal(err)
			}
		})
	}
}
