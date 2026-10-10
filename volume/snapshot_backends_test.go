package volume

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestSnapshotBackendsRequestAndFixture(t *testing.T) {
	c, _, requests := snapshotClient(t, "hcm-3", true, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/vserver/vbackup-gateway/v1/backends" || r.URL.RawQuery != "backend=HCM-03+%26%2F%3F" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/snapshot_backends.json")
	}))
	out, err := c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "HCM-03 &/?"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "backend-1" || out.Items[0].Name != "<account>" || *requests != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
}
