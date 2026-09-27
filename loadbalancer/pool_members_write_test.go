package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

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

// TestAddPoolMemberWriteStatusesPassThrough checks that a 404, 409, or 500
// on the members PUT itself reaches the caller as an unwrapped
// *vngcloud.APIError naming that status.
func TestAddPoolMemberWriteStatusesPassThrough(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
			case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
			case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
				_, _ = w.Write([]byte(memberFixture))
			case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080}
		_, err := c.AddPoolMember(context.Background(), in)
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: err = %v, want *vngcloud.APIError with that status", status, err)
		}
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

// TestAddPoolMemberBusyRefusalResendsOnce checks that a busy refusal on the
// members PUT itself, a race after the pre-write wait passed, is followed by
// one more wait and exactly one more send: the server did not act on the
// first attempt.
func TestAddPoolMemberBusyRefusalResendsOnce(t *testing.T) {
	var putCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
			if putCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"pool id pool-1 is updating"}`))
				return
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080, NoWait: true}
	if _, err := c.AddPoolMember(context.Background(), in); err != nil {
		t.Fatalf("AddPoolMember() error = %v", err)
	}
	if got := putCalls.Load(); got != 2 {
		t.Fatalf("PUT calls = %d, want 2 (busy refusal, then one resend)", got)
	}
}

// TestAddPoolMemberNonBusyRefusalNoResend checks that a refusal not matching
// a busy message is never resent.
func TestAddPoolMemberNonBusyRefusalNoResend(t *testing.T) {
	var putCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
			putCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080, NoWait: true}
	if _, err := c.AddPoolMember(context.Background(), in); err == nil {
		t.Fatal("AddPoolMember() error = nil, want an error")
	}
	if got := putCalls.Load(); got != 1 {
		t.Fatalf("PUT calls = %d, want 1 (a non-busy refusal is never resent)", got)
	}
}

// TestAddPoolMemberSettleWaitSleepsBeforeConfirmRead checks that the settle
// wait after the members PUT sleeps one childPollInterval before its first
// read, rather than reading immediately: a member replace leaves the pool's
// own progressStatus at CREATED throughout in this fixture, exactly the
// case where an immediate read cannot be told apart from one taken before
// the write was applied, and the confirm read right after the wait would
// otherwise risk reading the pool before the server finished the change.
func TestAddPoolMemberSettleWaitSleepsBeforeConfirmRead(t *testing.T) {
	var events []string
	var putSent bool
	var sentBody []byte
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			if putSent {
				events = append(events, "get-pool")
			}
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			if sentBody == nil {
				_, _ = w.Write([]byte(memberFixture))
				return
			}
			// The confirm read after the PUT reflects exactly what was sent,
			// simulating a server that has already applied the write.
			var sent poolMembersReplaceBody
			_ = json.Unmarshal(sentBody, &sent)
			var out struct {
				Data []PoolMember `json:"data"`
			}
			for _, e := range sent.Members {
				out.Data = append(out.Data, PoolMember{Address: e.Address, ProtocolPort: e.Port, Backup: e.Backup, Weight: e.Weight, Name: e.Name, MonitorPort: e.MonitorPort})
			}
			data, _ := json.Marshal(out)
			_, _ = w.Write(data)
		case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			sentBody = data
			putSent = true
			events = append(events, "put")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		events = append(events, "sleep:"+d.String())
		clock = clock.Add(d)
		return ctx.Err()
	}

	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080}
	if _, err := c.AddPoolMember(context.Background(), in); err != nil {
		t.Fatalf("AddPoolMember() error = %v", err)
	}

	want := []string{"put", "sleep:" + childPollInterval.String(), "get-pool"}
	if len(events) < 3 || events[0] != want[0] || events[1] != want[1] || events[2] != want[2] {
		t.Fatalf("events = %v, want to start with %v", events, want)
	}
}

// TestAddPoolMemberCrossCheckRefusesUnexpectedEmptyMembers checks the
// all-versus-none edge of the disagreement refusal: a ListPoolMembers read
// of zero members when the pool's own embedded Members, from the pre-write
// GetPool read, named at least one. ListPoolMembers already refuses a null
// or missing data key on its own, so this also guards any other way that
// read could wrongly come back empty. Without it, AddPoolMember would
// resend that empty read as the pool's whole member list, plus the one
// being added, silently dropping every member the pool actually has.
func TestAddPoolMemberCrossCheckRefusesUnexpectedEmptyMembers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q,"members":[{"uuid":"member-1","address":"10.0.0.1","protocolPort":80}]}}`,
				memberTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080}
	if _, err := c.AddPoolMember(context.Background(), in); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// memberDisagreementPoolBody is the pool read's body when GetPool's
// embedded Members names three members (10.0.0.1:80, 10.0.0.2:80, and
// 10.0.0.3:80) but a fresh ListPoolMembers, served from memberFixture, only
// lists the first two: a write from another process landed between the two
// reads.
const memberDisagreementPoolBody = `{"data":{"uuid":"pool-1","progressStatus":"CREATED","members":[
	{"address":"10.0.0.1","protocolPort":80},
	{"address":"10.0.0.2","protocolPort":80},
	{"address":"10.0.0.3","protocolPort":80}
]}}`

// memberAgreeingPoolBody is the pool read's body when GetPool's embedded
// Members names exactly the two members memberFixture's ListPoolMembers
// read also names: the two reads agree.
const memberAgreeingPoolBody = `{"data":{"uuid":"pool-1","progressStatus":"CREATED","members":[
	{"address":"10.0.0.1","protocolPort":80},
	{"address":"10.0.0.2","protocolPort":80}
]}}`

// memberDisagreementHandler serves memberDisagreementPoolBody for the pool
// read and memberFixture for ListPoolMembers, and fails the test if a PUT
// ever reaches the members endpoint: every write must refuse before sending
// anything when the two reads disagree.
func memberDisagreementHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = w.Write([]byte(memberDisagreementPoolBody))
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		default:
			t.Fatalf("unexpected request: %s %s (no PUT expected when the reads disagree)", r.Method, r.URL.Path)
		}
	}
}

func TestAddPoolMemberDisagreeingReadsRefuseWithoutPUT(t *testing.T) {
	c := newTestClient(t, memberDisagreementHandler(t))
	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.4", Port: 8080}
	_, err := c.AddPoolMember(context.Background(), in)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestUpdatePoolMemberDisagreeingReadsRefuseWithoutPUT(t *testing.T) {
	c := newTestClient(t, memberDisagreementHandler(t))
	in := &UpdatePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80, Weight: vngcloud.Ptr(5)}
	_, err := c.UpdatePoolMember(context.Background(), in)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestRemovePoolMemberDisagreeingReadsRefuseWithoutPUT(t *testing.T) {
	c := newTestClient(t, memberDisagreementHandler(t))
	in := &RemovePoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.1", Port: 80}
	_, err := c.RemovePoolMember(context.Background(), in)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// TestAddPoolMemberAgreeingReadsProceed checks that a non-nil embedded
// Members list that names exactly the same members ListPoolMembers reads
// does not trip the disagreement refusal.
func TestAddPoolMemberAgreeingReadsProceed(t *testing.T) {
	var putSent bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == memberLBPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, memberTestLBID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == memberPoolPath:
			_, _ = w.Write([]byte(memberAgreeingPoolBody))
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			_, _ = w.Write([]byte(memberFixture))
		case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
			putSent = true
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	in := &AddPoolMemberInput{LoadBalancerID: memberTestLBID, PoolID: memberTestPoolID, Address: "10.0.0.3", Port: 8080, NoWait: true}
	out, err := c.AddPoolMember(context.Background(), in)
	if err != nil {
		t.Fatalf("AddPoolMember() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if !putSent {
		t.Fatal("PUT not sent; want the write to proceed when the reads agree")
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
