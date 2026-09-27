package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

const (
	memberTestLBID   = "lb-1"
	memberTestPoolID = "pool-1"
)

var (
	memberLBPath      = "/v2/project-1/loadBalancers/" + memberTestLBID
	memberPoolPath    = memberLBPath + "/pools/" + memberTestPoolID
	memberMembersPath = memberPoolPath + "/members"
)

// memberFixture is the two members every pool member test starts from:
// 10.0.0.1:80 and 10.0.0.2:80.
const memberFixture = `{"data":[
	{"uuid":"member-1","address":"10.0.0.1","protocolPort":80,"weight":1,"backup":false},
	{"uuid":"member-2","address":"10.0.0.2","protocolPort":80,"weight":2,"backup":false,"name":"m2"}
]}`

// memberTestHandler serves a not-busy load balancer and pool, the member
// list, and captures the PUT body into body when non-nil. confirmBody, when
// non-empty, is what ListPoolMembers returns for the confirm read after the
// PUT; empty means it returns the same body as the PUT captured (built from
// the request itself), simulating a server that reflects exactly what was
// sent.
func memberTestHandler(t *testing.T, body *map[string]any, confirmBody string) http.HandlerFunc {
	t.Helper()
	var putBody []byte
	var getCalls int
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			getCalls++
			if getCalls > 1 && confirmBody != "" {
				_, _ = w.Write([]byte(confirmBody))
				return
			}
			if getCalls > 1 && putBody != nil {
				// Reflect what was sent, wrapped as a members list read, so
				// the confirm read matches by default.
				var sent poolMembersReplaceBody
				_ = json.Unmarshal(putBody, &sent)
				var out struct {
					Data []PoolMember `json:"data"`
				}
				for _, e := range sent.Members {
					out.Data = append(out.Data, PoolMember{Address: e.Address, ProtocolPort: e.Port, Backup: e.Backup, Weight: e.Weight, Name: e.Name, MonitorPort: e.MonitorPort})
				}
				data, _ := json.Marshal(out)
				_, _ = w.Write(data)
				return
			}
			_, _ = w.Write([]byte(memberFixture))
		case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			putBody = data
			if body != nil {
				if err := json.Unmarshal(data, body); err != nil {
					t.Fatalf("decode body: %v, raw = %s", err, data)
				}
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestAddPoolMemberRequestBodyKeepsExistingMembers(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, memberTestHandler(t, &body, ""))
	withInstantSleep(c)

	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080}
	out, err := c.AddPoolMember(context.Background(), in)
	if err != nil {
		t.Fatalf("AddPoolMember() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	members, ok := body["members"].([]any)
	if !ok || len(members) != 3 {
		t.Fatalf("body[members] = %+v, want 3 entries", body["members"])
	}
	addrs := map[string]bool{}
	for _, m := range members {
		entry := m.(map[string]any)
		addrs[entry["ipAddress"].(string)] = true
	}
	for _, want := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		if !addrs[want] {
			t.Fatalf("members = %+v, missing %s", members, want)
		}
	}
}

func TestAddPoolMemberDefaultsWeightToOne(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, memberTestHandler(t, &body, ""))
	withInstantSleep(c)

	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080}
	if _, err := c.AddPoolMember(context.Background(), in); err != nil {
		t.Fatalf("AddPoolMember() error = %v", err)
	}
	for _, m := range body["members"].([]any) {
		entry := m.(map[string]any)
		if entry["ipAddress"] == "10.0.0.3" && entry["weight"] != float64(1) {
			t.Fatalf("new member weight = %v, want 1", entry["weight"])
		}
	}
}

func TestAddPoolMemberSameFieldsNoOp(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		default:
			t.Fatal("no PUT expected for an unchanged add")
		}
	}))
	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80, Weight: 1}
	out, err := c.AddPoolMember(context.Background(), in)
	if err != nil {
		t.Fatalf("AddPoolMember() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAddPoolMemberConflictingFieldsRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		default:
			t.Fatal("no PUT expected for a conflicting add")
		}
	}))
	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80, Weight: 5}
	_, err := c.AddPoolMember(context.Background(), in)
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdatePoolMemberAbsentIsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		default:
			t.Fatal("no PUT expected for an absent update")
		}
	}))
	in := &UpdatePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.9", Port: 80, Weight: vngcloud.Ptr(2)}
	if _, err := c.UpdatePoolMember(context.Background(), in); !errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdatePoolMemberKeepsOtherMembersAndFields(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, memberTestHandler(t, &body, ""))
	withInstantSleep(c)

	in := &UpdatePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.2", Port: 80, Weight: vngcloud.Ptr(9)}
	out, err := c.UpdatePoolMember(context.Background(), in)
	if err != nil {
		t.Fatalf("UpdatePoolMember() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	members := body["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members = %+v, want 2 (no member dropped)", members)
	}
	for _, m := range members {
		entry := m.(map[string]any)
		if entry["ipAddress"] == "10.0.0.2" {
			if entry["weight"] != float64(9) {
				t.Fatalf("updated member weight = %v, want 9", entry["weight"])
			}
			if entry["name"] != "m2" {
				t.Fatalf("updated member name = %v, want m2 (kept from read)", entry["name"])
			}
		}
	}
}

func TestUpdatePoolMemberNoChangeIsNoOp(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		default:
			t.Fatal("no PUT expected when nothing changes")
		}
	}))
	in := &UpdatePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80, Weight: vngcloud.Ptr(1)}
	out, err := c.UpdatePoolMember(context.Background(), in)
	if err != nil {
		t.Fatalf("UpdatePoolMember() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestUpdatePoolMemberRejectsNoFieldsSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := &UpdatePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80}
	if _, err := c.UpdatePoolMember(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRemovePoolMemberAbsentIsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		default:
			t.Fatal("no PUT expected for an absent remove")
		}
	}))
	in := &RemovePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.9", Port: 80}
	if _, err := c.RemovePoolMember(context.Background(), in); !errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRemovePoolMemberKeepsOtherMembers(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, memberTestHandler(t, &body, ""))
	withInstantSleep(c)

	in := &RemovePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80}
	out, err := c.RemovePoolMember(context.Background(), in)
	if err != nil {
		t.Fatalf("RemovePoolMember() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	members := body["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("members = %+v, want 1 remaining", members)
	}
	if members[0].(map[string]any)["ipAddress"] != "10.0.0.2" {
		t.Fatalf("remaining member = %+v, want 10.0.0.2", members[0])
	}
}

func TestPoolMemberConfirmMismatchIsNotSettled(t *testing.T) {
	c := newTestClient(t, memberTestHandler(t, nil, memberFixture))
	withInstantSleep(c)

	in := &RemovePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80}
	_, err := c.RemovePoolMember(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestPoolMemberRejectsNonIPv4Address(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, addr := range []string{"not-an-ip", "::1", "2001:db8::1"} {
		in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: addr, Port: 80}
		if _, err := c.AddPoolMember(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("Address=%q err = %v, want ErrInvalidInput", addr, err)
		}
	}
}

func TestPoolMemberRejectsBadPort(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, port := range []int{0, 70000, -1} {
		in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: port}
		if _, err := c.AddPoolMember(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("Port=%d err = %v, want ErrInvalidInput", port, err)
		}
	}
}

func TestPoolMemberRejectsBadPathIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := &AddPoolMemberInput{LoadBalancerID: bad, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80}
		if _, err := c.AddPoolMember(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
