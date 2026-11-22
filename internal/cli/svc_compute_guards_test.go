package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestComputeServerGroupOffsetLimitFlags(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/serverGroups": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("offset") != "2" || r.URL.Query().Get("limit") != "100" {
				t.Errorf("query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":100,"totalPage":0,"totalItem":0}`))
		},
	})
	for _, input := range [][]string{{"--offset", "2", "--limit", "100"}, {"--cli-input-json", `{"Offset":2,"Limit":100}`}} {
		root, _, stderr := newSvcRoot(t, fixture)
		root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "list-server-groups"}, input...))
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("err=%v stderr=%s", err, stderr.String())
		}
	}
	for _, flag := range []string{"--page", "--size"} {
		root, _, _ := newSvcRoot(t, fixture)
		root.SetArgs([]string{"compute", "list-server-groups", flag, "2"})
		if err := root.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("old flag accepted: %v", err)
		}
	}
}
