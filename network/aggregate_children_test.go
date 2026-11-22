package network

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSecurityGroupAggregateRequiresExplicitChildren(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[null]}`, `{"data":[{}]}`, `{"data":[{"id":""}]}`, `{"data":[{"id":".."}]}`, `{"data":[{"id":"rule-1"},{"id":"rule-1"}]}`, `{"data":[],"Data":null}`, `{"data":[]}`, `{"data":[{"id":"rule-2"}]}`} {
		t.Run(body, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/secgroups") {
					_, _ = w.Write([]byte(`{"listData":[{"id":"sg-1"},{"id":"sg-2"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":2}`))
					return
				}
				if strings.Contains(r.URL.Path, "/sg-1/") {
					_, _ = w.Write([]byte(`{"data":[{"id":"rule-1"}]}`))
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			out, err := c.ListAllSecurityGroupRules(context.Background(), nil)
			valid := body == `{"data":[]}` || body == `{"data":[{"id":"rule-2"}]}`
			if valid {
				want := 2
				if body == `{"data":[]}` {
					want = 1
				}
				if err != nil || out == nil || len(out.Items) != want {
					t.Fatalf("valid child array refused: %v", err)
				}
			} else if err == nil || out != nil {
				t.Fatalf("incomplete child array returned output: err=%v", err)
			}
		})
	}
}
