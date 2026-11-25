package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func dashboardCLI(t *testing.T, handler http.Handler, extra ...vngcloud.LoadOption) func(...string) (int, string, string) {
	t.Helper()
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	opts := []vngcloud.LoadOption{vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL}), vngcloud.WithHTTPClient(&http.Client{Transport: refusingTransport{base: server.Client().Transport}}), vngcloud.WithRetry(0, 0)}
	withTestOptions(t, append(opts, extra...)...)
	return func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
}

func TestDashboardCLI(t *testing.T) {
	for _, system := range []bool{false, true} {
		t.Run(strconv.FormatBool(system), func(t *testing.T) {
			run := dashboardCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				filter := "system:false"
				if system {
					filter = "systemType:productVng"
				}
				if r.Method != "GET" || r.URL.Path != "/vmonitor-api/api/v1/dashboards" || r.URL.Query().Get("filter") != filter || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10" {
					t.Error(r.URL)
				}
				_, _ = fmt.Fprintf(w, `{"lstData":[{"id":"dash-1","name":"Synthetic","system":%t,"favorite":true,"updatedAt":"today","widgets":["SECRET-MARKER"]}],"page":1,"pageSize":10,"totalItem":1,"totalPage":1}`, system)
			}))
			args := []string{"--profile", "agent", "--read-only", "monitor", "list-dashboards"}
			if system {
				args = append(args, "--system")
			}
			code, out, errout := run(args...)
			if code != 0 || !strings.Contains(out, "dash-1") || strings.Contains(out+errout, "SECRET-MARKER") {
				t.Fatal(code, out, errout)
			}
		})
	}
}

func TestDashboardCLIOutputAndSafety(t *testing.T) {
	requests := 0
	run := dashboardCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.HasSuffix(r.URL.Path, "/dash-1") {
			_, _ = fmt.Fprint(w, `{"id":"dash-1","name":"Synthetic","system":true,"favorite":true,"updatedAt":"today","widgets":["SECRET-MARKER"]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"lstData":[{"id":"dash-1","name":"Synthetic","system":true,"favorite":true,"updatedAt":"today","widgets":["SECRET-MARKER"]}],"page":1,"pageSize":10,"totalItem":1,"totalPage":1}`)
	}))
	for _, format := range []string{"json", "table", "text"} {
		for _, command := range [][]string{{"list-dashboards", "--system"}, {"get-dashboard", "--dashboard-id", "dash-1"}} {
			args := append([]string{"--profile", "agent", "--read-only", "--output", format, "monitor"}, command...)
			code, out, errout := run(args...)
			want := dashboardCLIGolden[format]
			if format == "json" && command[0] == "get-dashboard" {
				want = dashboardCLIGetJSON
			}
			if out != want {
				t.Fatalf("unexpected %s output: %q", format, out)
			}
			if code != 0 || !strings.Contains(out, "dash-1") || strings.Contains(out+errout, "SECRET-MARKER") {
				t.Fatal(code, out, errout)
			}
		}
	}
	code, out, errout := run("--profile", "agent", "--output", "json", "--query", "Items[*].Name", "monitor", "list-dashboards", "--system")
	if code != 0 || !strings.Contains(out, "Synthetic") || strings.Contains(out+errout, "SECRET-MARKER") {
		t.Fatal(code, out, errout)
	}
	before := requests
	for _, args := range [][]string{{"monitor", "list-dashboards", "--size", "101"}, {"monitor", "get-dashboard", "--dashboard-id", "../SECRET-MARKER"}} {
		args = append([]string{"--profile", "agent"}, args...)
		code, out, errout = run(args...)
		if code != 2 || out != "" || strings.Contains(errout, "SECRET-MARKER") || requests != before {
			t.Fatal(code, out, errout, requests)
		}
	}
	code, out, errout = run("--profile", "agent", "monitor", "list-dashboards")
	if code == 0 || out != "" || !strings.Contains(errout, "InvalidResponse") {
		t.Fatal(code, out, errout)
	}
}

var dashboardCLIGolden = map[string]string{
	"json": `{
  "Items": [
    {
      "ID": "dash-1",
      "Name": "Synthetic",
      "System": true,
      "Favorite": true,
      "CreatedAt": "",
      "UpdatedAt": "today"
    }
  ],
  "Page": 1,
  "PageSize": 10,
  "TotalPage": 1,
  "TotalItem": 1
}
`,
	"table": "+----------+--------+-----------+--------+-----------+\n| Favorite | ID     | Name      | System | UpdatedAt |\n+----------+--------+-----------+--------+-----------+\n| true     | dash-1 | Synthetic | true   | today     |\n+----------+--------+-----------+--------+-----------+\n",
	"text":  "true\tdash-1\tSynthetic\ttrue\ttoday\n",
}

const dashboardCLIGetJSON = `{
  "Dashboard": {
    "ID": "dash-1",
    "Name": "Synthetic",
    "System": true,
    "Favorite": true,
    "CreatedAt": "",
    "UpdatedAt": "today"
  }
}
`

func TestDashboardCLIReflectionAndCapture(t *testing.T) {
	var captures []vngcloud.ResponseCapture
	reflected := false
	run := dashboardCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "safe"
		if reflected {
			name = "prefix-test-token"
		}
		row := fmt.Sprintf(`{"id":"dash-1","name":%q,"system":true,"widgets":["BODY-SECRET-MARKER"]}`, name)
		if strings.HasSuffix(r.URL.Path, "/dash-1") {
			_, _ = fmt.Fprint(w, row)
		} else {
			_, _ = fmt.Fprintf(w, `{"lstData":[%s],"page":1,"pageSize":10,"totalItem":1,"totalPage":1}`, row)
		}
	}), vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { captures = append(captures, c) }))
	for _, command := range [][]string{{"list-dashboards", "--system"}, {"get-dashboard", "--dashboard-id", "dash-1"}} {
		for _, format := range []string{"json", "table", "text"} {
			for _, query := range []bool{false, true} {
				args := []string{"--profile", "agent", "--debug", "--output", format}
				if query {
					expression := "Items[*].Name"
					if command[0] == "get-dashboard" {
						expression = "Dashboard.Name"
					}
					args = append(args, "--query", expression)
				}
				args = append(args, "monitor")
				args = append(args, command...)
				reflected = false
				code, out, errout := run(args...)
				if code != 0 || strings.Contains(out+errout, "BODY-SECRET-MARKER") {
					t.Fatal(out, errout)
				}
				reflected = true
				code, out, errout = run(args...)
				if code == 0 || out != "" || !strings.Contains(errout, "InvalidResponse") || strings.Contains(errout, "test-token") || strings.Contains(errout, "BODY-SECRET-MARKER") {
					t.Fatal(code, out, errout)
				}
			}
		}
	}
	for _, capture := range captures {
		if len(capture.Body) != 0 {
			t.Fatal("sensitive response reached capture hook")
		}
	}
}

func TestDashboardGetNoteExcludesSystemFlag(t *testing.T) {
	if strings.Contains(docOpNotes["monitor get-dashboard"], "--system") {
		t.Fatal(docOpNotes["monitor get-dashboard"])
	}
}
