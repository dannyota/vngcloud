package compute

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestComputeAggregatesRequireExplicitChildren(t *testing.T) {
	for _, groups := range []bool{false, true} {
		for _, child := range []string{"", `null`, `[null]`, `[{}]`, `[{"uuid":""}]`, `[{"uuid":".."}]`, `[{"uuid":"child-1"},{"uuid":"child-1"}]`, `[]`, `[{"uuid":"child-2"}]`} {
			t.Run(fmt.Sprintf("groups=%v/%s", groups, child), func(t *testing.T) {
				key := "secGroups"
				if groups {
					key = "servers"
				}
				field := ""
				if child != "" {
					field = fmt.Sprintf(",%q:%s", key, child)
				}
				body := fmt.Sprintf(`{"listData":[{"uuid":"parent-1",%q:[{"uuid":"child-1"}]},{"uuid":"parent-2"%s}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":2}`, key, field)
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
				var out any
				var err error
				if groups {
					out, err = c.ListServerGroupMembers(context.Background(), nil)
				} else {
					out, err = c.ListServerSecurityGroups(context.Background(), nil)
				}
				valid := child == `[]` || child == `[{"uuid":"child-2"}]`
				if valid {
					if err != nil {
						t.Fatal(err)
					}
					want := 2
					if child == `[]` {
						want = 1
					}
					if reflect.ValueOf(out).IsNil() || reflect.ValueOf(out).Elem().FieldByName("Items").Len() != want {
						t.Fatal("valid children were lost")
					}
				} else if err == nil || !reflect.ValueOf(out).IsNil() {
					t.Fatalf("incomplete child inventory returned output: err=%v", err)
				}
			})
		}
	}
}
