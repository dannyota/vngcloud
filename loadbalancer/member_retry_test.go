package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestMemberBusyRefusalMergesFreshMembers(t *testing.T) {
	for _, operation := range []string{"add", "update", "remove"} {
		t.Run(operation, func(t *testing.T) {
			var bodies []poolMembersReplaceBody
			reads := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == memberLBPath:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`)
				case r.URL.Path == memberPoolPath:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`)
				case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
					reads++
					members := []PoolMember{{Address: "10.0.0.1", ProtocolPort: 80, Weight: 1}}
					if len(bodies) > 0 {
						members[0].Name = "fresh-name"
						members = append(members, PoolMember{Address: "10.0.0.2", ProtocolPort: 80, Weight: 2})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": members})
				case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
					var body poolMembersReplaceBody
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					bodies = append(bodies, body)
					if len(bodies) == 1 {
						w.WriteHeader(http.StatusConflict)
						_, _ = fmt.Fprint(w, `{"message":"pool is updating"}`)
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			withInstantSleep(c)
			var err error
			switch operation {
			case "add":
				_, err = c.AddPoolMember(context.Background(), &AddPoolMemberInput{
					LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.3", Port: 80, NoWait: true,
				})
			case "update":
				_, err = c.UpdatePoolMember(context.Background(), &UpdatePoolMemberInput{
					LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.1", Port: 80,
					Weight: vngcloud.Ptr(5), NoWait: true,
				})
			case "remove":
				_, err = c.RemovePoolMember(context.Background(), &RemovePoolMemberInput{
					LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.1", Port: 80, NoWait: true,
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 2 || reads != 2 {
				t.Fatalf("writes = %d, member reads = %d; want 2 each", len(bodies), reads)
			}
			if findPoolMemberEntry(bodies[1].Members, poolMemberKeyOf("10.0.0.2", 80)) < 0 {
				t.Fatalf("second body lost concurrent member: %+v", bodies[1])
			}
			if operation == "update" {
				updated := bodies[1].Members[findPoolMemberEntry(bodies[1].Members, poolMemberKeyOf("10.0.0.1", 80))]
				if updated.Name != "fresh-name" || updated.Weight != 5 {
					t.Fatalf("updated member = %+v", updated)
				}
			}
		})
	}
}

func TestMemberAmbiguousFailureNeverResends(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			writes := 0
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut:
					writes++
					if status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect {
						w.Header().Set("Location", memberMembersPath+"/redirected")
					}
					w.WriteHeader(status)
					_, _ = fmt.Fprint(w, `{"message":"pool is updating"}`)
				case r.URL.Path == memberMembersPath:
					_, _ = fmt.Fprint(w, `{"data":[]}`)
				case r.URL.Path == memberPoolPath:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`)
				default:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`)
				}
			})))
			_, err := c.AddPoolMember(context.Background(), &AddPoolMemberInput{
				LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.1", Port: 80, NoWait: true,
			})
			if writes != 1 || !errors.Is(err, ErrNotSettled) {
				t.Fatalf("writes = %d, error = %v; want one write and ErrNotSettled", writes, err)
			}
		})
	}
}
