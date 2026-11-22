package compute

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestServerGroupOffsetLimit(t *testing.T) {
	in := new(ListServerGroupsInput)
	for field, value := range map[string]int{"Offset": 2, "Limit": 100} {
		v := reflect.ValueOf(in).Elem().FieldByName(field)
		if !v.IsValid() {
			t.Fatalf("missing %s", field)
		}
		v.SetInt(int64(value))
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "2" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("query=%s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":100,"totalPage":0,"totalItem":0}`))
	}))
	if _, err := c.ListServerGroups(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestComputeAggregatesWalkAllParents(t *testing.T) {
	for _, groups := range []bool{false, true} {
		for _, mode := range []string{"complete", "repeat page", "repeat ID", "changed totals", "missing list", "null list", "duplicate field", "canceled"} {
			t.Run(strconv.FormatBool(groups)+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					page := calls
					if mode == "repeat page" {
						page = 1
					}
					id := "parent-" + strconv.Itoa(calls)
					if mode == "repeat ID" {
						id = "parent-1"
					}
					total := 2
					if mode == "changed totals" && calls == 2 {
						total = 3
					}
					if groups && r.URL.Query().Get("offset") != strconv.Itoa(calls-1) {
						t.Errorf("offset=%s", r.URL.Query().Get("offset"))
					}
					child := "secGroups"
					if groups {
						child = "servers"
					}
					body := map[string]any{"page": page, "pageSize": 1, "totalPage": 2, "totalItem": total, "listData": []any{map[string]any{"uuid": id, child: []any{map[string]string{"uuid": "child-" + strconv.Itoa(calls), "name": "child"}}}}}
					if mode == "missing list" {
						delete(body, "listData")
					}
					if mode == "null list" {
						body["listData"] = nil
					}
					if mode == "duplicate field" {
						body["TotalItem"] = 2
					}
					if mode == "canceled" {
						cancel()
					}
					_ = json.NewEncoder(w).Encode(body)
				}))
				var length int
				var err error
				if groups {
					var out *ListServerGroupMembersOutput
					out, err = c.ListServerGroupMembers(ctx, nil)
					if out != nil {
						length = len(out.Items)
					}
				} else {
					var out *ListServerSecurityGroupsOutput
					out, err = c.ListServerSecurityGroups(ctx, nil)
					if out != nil {
						length = len(out.Items)
					}
				}
				if mode == "complete" {
					if err != nil || length != 2 || calls != 2 {
						t.Fatalf("err=%v length=%d calls=%d", err, length, calls)
					}
				} else if err == nil || length != 0 {
					t.Fatalf("partial aggregate accepted: err=%v length=%d", err, length)
				}
			})
		}
	}
}

func TestComputePollRejectsLateSuccess(t *testing.T) {
	now := time.Unix(0, 0)
	err := poll(context.Background(), func() time.Time { return now }, contextSleep, time.Second, func(context.Context) (bool, error) { now = now.Add(time.Second); return true, nil }, func() error { return context.DeadlineExceeded })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late success: %v", err)
	}
}

func TestRebootRecoveryUsesRead(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"REBOOTING"}}`))
	})))
	_, err := c.waitServerRebooted(context.Background(), "compute.RebootServer", "server-1", c.now())
	if err == nil || !stringsContainsReadRecovery(err.Error()) {
		t.Fatalf("unsafe recovery: %v", err)
	}
}

func stringsContainsReadRecovery(text string) bool {
	return strings.Contains(text, "get-server") && !strings.Contains(text, "run this operation again")
}
