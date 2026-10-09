package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

func TestGoldenLoadBalancerAddPoolMember(t *testing.T) {
	v := &loadbalancer.AddPoolMemberOutput{Pool: examplePool(), Changed: true}
	checkGolden(t, "loadbalancer-add-pool-member.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-add-pool-member.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerUpdatePoolMember(t *testing.T) {
	v := &loadbalancer.UpdatePoolMemberOutput{Pool: examplePool(), Changed: true}
	checkGolden(t, "loadbalancer-update-pool-member.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-update-pool-member.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerRemovePoolMember(t *testing.T) {
	v := &loadbalancer.RemovePoolMemberOutput{Pool: examplePool(), Changed: true}
	checkGolden(t, "loadbalancer-remove-pool-member.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-remove-pool-member.table.golden", "table", "", v)
}

// memberPoolReadyJSON is a not-busy GetPool response for pool-1.
const memberPoolReadyJSON = `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`

// memberListJSON is the members list every pool member test below starts
// from: one member at 10.0.0.1:80.
const memberListJSON = `{"data":[{"uuid":"member-1","address":"10.0.0.1","protocolPort":80,"weight":1,"backup":false}]}`

// TestLoadBalancerAddPoolMemberSendsMergedList drives add-pool-member end to
// end with --no-wait: it reads the existing members and sends them back
// plus the new one, per the vLB writes design's read-merge write.
func TestLoadBalancerAddPoolMemberSendsMergedList(t *testing.T) {
	var putBody map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1":              jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": jsonHandler(http.StatusOK, memberPoolReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/members": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, memberListJSON)(w, r)
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &putBody); err != nil {
					t.Fatalf("decode body: %v (%s)", err, data)
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "add-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1",
		"--address", "10.0.0.2", "--port", "8080", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("add-pool-member: %v (stderr=%s)", err, stderr.String())
	}
	members, ok := putBody["members"].([]any)
	if !ok || len(members) != 2 {
		t.Fatalf("putBody[members] = %+v, want 2 entries", putBody["members"])
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestLoadBalancerAddPoolMemberAlreadyPresentIsANoOp checks that an add of a
// member already present with the same fields returns Changed false and
// sends no PUT.
func TestLoadBalancerAddPoolMemberAlreadyPresentIsANoOp(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1":              jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": jsonHandler(http.StatusOK, memberPoolReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/members": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want no PUT for an unchanged add", r.Method)
			}
			jsonHandler(http.StatusOK, memberListJSON)(w, r)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "add-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1",
		"--address", "10.0.0.1", "--port", "80", "--weight", "1", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("add-pool-member: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", got)
	}
}

// TestLoadBalancerUpdatePoolMemberSendsMergedList drives update-pool-member
// end to end with --no-wait: it changes the matching member's field and
// resends the whole list.
func TestLoadBalancerUpdatePoolMemberSendsMergedList(t *testing.T) {
	var putBody map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1":              jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": jsonHandler(http.StatusOK, memberPoolReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/members": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, memberListJSON)(w, r)
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &putBody); err != nil {
					t.Fatalf("decode body: %v (%s)", err, data)
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1",
		"--address", "10.0.0.1", "--port", "80", "--weight", "5", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-pool-member: %v (stderr=%s)", err, stderr.String())
	}
	members, ok := putBody["members"].([]any)
	if !ok || len(members) != 1 {
		t.Fatalf("putBody[members] = %+v, want 1 entry", putBody["members"])
	}
	entry, ok := members[0].(map[string]any)
	if !ok || entry["weight"] != 5.0 {
		t.Fatalf("members[0] = %+v, want weight 5", members[0])
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestLoadBalancerUpdatePoolMemberNotFoundExitsWithZeroPUTs checks that
// updating a member address:port the pool does not have exits NotFound with
// no PUT sent.
func TestLoadBalancerUpdatePoolMemberNotFoundExitsWithZeroPUTs(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1":              jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": jsonHandler(http.StatusOK, memberPoolReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/members": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected method %s, want no PUT for a missing member", r.Method)
			}
			jsonHandler(http.StatusOK, memberListJSON)(w, r)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1",
		"--address", "10.0.0.9", "--port", "80", "--weight", "5", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a NotFound refusal")
	}
	if got := exitCode(err); got != 4 {
		t.Fatalf("exitCode = %d, want 4 (stderr=%s)", got, stderr.String())
	}
}

// TestLoadBalancerUpdatePoolMemberNoFieldSetExitsWithZeroRequests checks
// that update-pool-member with no optional field set is refused with
// InvalidUsage before any request.
func TestLoadBalancerUpdatePoolMemberNoFieldSetExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--address", "10.0.0.1", "--port", "80",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an InvalidUsage refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerRemovePoolMemberWithoutYesExitsWithZeroRequests checks the
// design's --yes rule: remove-pool-member is refused without --yes before
// any request, even though it is not Destructive.
func TestLoadBalancerRemovePoolMemberWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "remove-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--address", "10.0.0.1", "--port", "80", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a --yes refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerRemovePoolMemberWithYesSendsRemainingList drives
// remove-pool-member with --yes and --no-wait: it sends back every member
// except the match.
func TestLoadBalancerRemovePoolMemberWithYesSendsRemainingList(t *testing.T) {
	var putBody map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1":              jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": jsonHandler(http.StatusOK, memberPoolReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/members": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, memberListJSON)(w, r)
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &putBody); err != nil {
					t.Fatalf("decode body: %v (%s)", err, data)
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "remove-pool-member",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1",
		"--address", "10.0.0.1", "--port", "80", "--yes", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("remove-pool-member: %v (stderr=%s)", err, stderr.String())
	}
	members, ok := putBody["members"].([]any)
	if !ok || len(members) != 0 {
		t.Fatalf("putBody[members] = %+v, want an empty list", putBody["members"])
	}
}

// TestLoadBalancerPoolMemberWritesReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule across every pool member write.
func TestLoadBalancerPoolMemberWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	tests := []struct {
		name string
		args []string
	}{
		{"add-pool-member", []string{"loadbalancer", "add-pool-member", "--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--address", "10.0.0.1", "--port", "80"}},
		{"update-pool-member", []string{"loadbalancer", "update-pool-member", "--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--address", "10.0.0.1", "--port", "80", "--weight", "2"}},
		{"remove-pool-member", []string{"loadbalancer", "remove-pool-member", "--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--address", "10.0.0.1", "--port", "80", "--yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refuse := func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/loadBalancers/lb-1":                      refuse,
				"/v2/proj-1/loadBalancers/lb-1/pools/pool-1":         refuse,
				"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/members": refuse,
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent"}, tt.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := fixture.requestCount(); got != 0 {
				t.Fatalf("requestCount = %d, want 0", got)
			}
		})
	}
}
