package network

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestSecurityGroupAggregateWalksAllParents(t *testing.T) {
	for _, mode := range []string{"complete", "repeat page", "repeat ID", "changed totals", "missing list", "null list", "duplicate key"} {
		t.Run(mode, func(t *testing.T) {
			parents := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/secGroupRules") {
					_, _ = w.Write([]byte(`{"data":[{"id":"rule-1"}]}`))
					return
				}
				parents++
				page := parents
				if mode == "repeat page" {
					page = 1
				}
				id := "group-" + strconv.Itoa(parents)
				if mode == "repeat ID" {
					id = "group-1"
				}
				total := 2
				if mode == "changed totals" && parents == 2 {
					total = 3
				}
				body := map[string]any{"listData": []any{map[string]string{"id": id}}, "page": page, "pageSize": 1, "totalPage": 2, "totalItem": total}
				if mode == "missing list" {
					delete(body, "listData")
				}
				if mode == "null list" {
					body["listData"] = nil
				}
				if mode == "duplicate key" {
					body["TotalItem"] = 2
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			out, err := c.ListAllSecurityGroupRules(context.Background(), nil)
			if mode == "complete" {
				if err != nil || len(out.Items) != 2 || parents != 2 {
					t.Fatalf("out=%+v err=%v parents=%d", out, err, parents)
				}
			} else if err == nil || out != nil {
				t.Fatalf("incomplete aggregate: out=%+v err=%v", out, err)
			}
		})
	}
}
