package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/tagging"
)

func TestTaggingUncertainReplacementKeepsPartialOutput(t *testing.T) {
	writes := 0
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/tag/resource/res-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`[{"key":"env","value":"old","systemTag":false}]`))
				return
			}
			writes++
			w.WriteHeader(503)
		},
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--query", "Previous", "tagging", "tag-resource", "--resource-id", "res-1", "--resource-type", "SERVER", "--key", "env", "--value", "new"})
	err := root.ExecuteContext(context.Background())
	if !errors.Is(err, tagging.ErrNotSettled) || classify(err).Code != "NotSettled" || exitCode(err) != 1 || writes != 1 || !strings.Contains(stdout.String(), `"Changed": true`) || !strings.Contains(stdout.String(), `"Key": "env"`) {
		t.Fatalf("uncertain tags lost: output=%s writes=%d error=%v", stdout.String(), writes, err)
	}
}
