package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Schema-built internal interfaces use documentation addresses, not live captures.
func TestServerPublicIPCLI(t *testing.T) {
	server := `{"uuid":"server-1","name":"web-1","internalInterfaces":[{"floatingIp":"::ffff:192.0.2.1"}]}`
	for _, tc := range []struct {
		service, command, path, body, query, want string
		flags                                     []string
	}{
		{"compute", "get-server", "/v2/proj-1/servers/server-1", `{"data":` + server + `}`, "Server.PublicIP", "192.0.2.1", []string{"--server-id", "server-1"}},
		{"compute", "list-servers", "/v2/proj-1/servers", `{"listData":[` + server + `],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`, "Items[].PublicIP", "192.0.2.1", nil},
		{"network", "list-servers-by-security-group", "/v2/proj-1/secgroups/group-1/servers", `{"data":[` + server + `]}`, "Items[].PublicIP", "192.0.2.1", []string{"--security-group-id", "group-1"}},
	} {
		for _, format := range []string{"json", "table", "text"} {
			for _, query := range []bool{false, true} {
				t.Run(tc.command+"/"+format+"/query="+map[bool]string{true: "yes", false: "no"}[query], func(t *testing.T) {
					fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){tc.path: jsonHandler(http.StatusOK, tc.body)})
					root, stdout, stderr := newSvcRoot(t, fixture)
					args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--output", format}
					if query {
						args = append(args, "--query", tc.query)
					}
					args = append(args, tc.service, tc.command)
					args = append(args, tc.flags...)
					root.SetArgs(args)
					if err := root.ExecuteContext(context.Background()); err != nil {
						t.Fatalf("command: %v (%s)", err, stderr)
					}
					got := stdout.String()
					if !strings.Contains(got, tc.want) {
						t.Fatalf("output missing canonical public address: %s", got)
					}
					if !query && format != "text" && !strings.Contains(got, "PublicIP") {
						t.Fatal("output omitted PublicIP field")
					}
					if !query && format == "json" {
						var decoded map[string]any
						if err := json.Unmarshal([]byte(got), &decoded); err != nil {
							t.Fatal(err)
						}
						var row map[string]any
						if tc.command == "get-server" {
							row = decoded["Server"].(map[string]any)
						} else {
							row = decoded["Items"].([]any)[0].(map[string]any)
						}
						if row["PublicIP"] != tc.want || row["UUID"] != "server-1" {
							t.Fatal("CLI fields changed")
						}
					}
					if !query && format == "text" && tc.command != "get-server" && !slices.Contains(strings.Fields(got), tc.want) {
						t.Fatal("text row omitted canonical PublicIP value")
					}
					if query && format == "text" && strings.TrimSpace(got) != tc.want {
						t.Fatalf("query text = %q", got)
					}
				})
			}
		}
	}
}
