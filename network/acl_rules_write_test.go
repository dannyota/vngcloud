package network

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// --- AddNetworkACLRule and RemoveNetworkACLRule shape checks ---

func TestAddNetworkACLRuleBadPriority(t *testing.T) {
	cases := []int{-1, 2000, 100000}
	for _, priority := range cases {
		t.Run(strconv.Itoa(priority), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: priority, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAddNetworkACLRuleZeroPriorityRequiredField(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	// Priority 0 fails CheckRequired before checkACLRulePriority ever runs,
	// since 0 is Priority's zero value; the message differs from the
	// explicit "at least 1" one but the sentinel is the same.
	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 0, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
	})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestAddNetworkACLRuleBadCIDR(t *testing.T) {
	cases := []string{"203.0.113.1/24", "not-a-cidr", ""}
	for _, cidr := range cases {
		t.Run(cidr, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: cidr, Action: "pass",
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAddNetworkACLRuleBadPorts(t *testing.T) {
	cases := []struct {
		name     string
		min, max int
	}{
		{"min negative", -1, 0},
		{"max too large", 0, 70000},
		{"min above max", 500, 100},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
				PortRangeMin: tt.min, PortRangeMax: tt.max,
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAddNetworkACLRuleExplicitFullPortRangeSends0To65535(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case http.MethodPut:
			body := decodeBody(t, r)
			rules, _ := body["detailAclRuleList"].([]any)
			entry, _ := rules[0].(map[string]any)
			if entry["port"] != "0-65535" {
				t.Fatalf("port in body = %v, want 0-65535", entry["port"])
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "ANY", CIDR: "203.0.113.0/24", Action: "pass",
		PortRangeMin: 0, PortRangeMax: 65535, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
}

// TestAddNetworkACLRuleProtocolAndPortEncoding checks checkACLRuleProtocol
// and checkACLRulePorts together: the exact protocol spelling and port
// string each valid combination sends, confirmed live.
func TestAddNetworkACLRuleProtocolAndPortEncoding(t *testing.T) {
	cases := []struct {
		name             string
		protocol         string
		portMin, portMax int
		wantProtocol     string
		wantPort         string
	}{
		{"tcp single port", "TCP", 22, 22, "tcp", "22"},
		{"udp port range", "Udp", 53, 54, "udp", "53-54"},
		{"icmp zero", "ICMP", 0, 0, "icmp", "0"},
		{"icmp full range", "icmp", 0, 65535, "icmp", "0-65535"},
		{"any lowercase input sends uppercase ANY", "any", 0, 65535, "ANY", "0-65535"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
				case http.MethodPut:
					body := decodeBody(t, r)
					rules, _ := body["detailAclRuleList"].([]any)
					entry, _ := rules[0].(map[string]any)
					if entry["protocol"] != tt.wantProtocol {
						t.Fatalf("protocol in body = %v, want %s", entry["protocol"], tt.wantProtocol)
					}
					if entry["port"] != tt.wantPort {
						t.Fatalf("port in body = %v, want %s", entry["port"], tt.wantPort)
					}
					w.WriteHeader(http.StatusOK)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: tt.protocol,
				CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: tt.portMin, PortRangeMax: tt.portMax, NoWait: true,
			})
			if err != nil {
				t.Fatalf("AddNetworkACLRule() error = %v", err)
			}
		})
	}
}

// TestAddNetworkACLRuleBadProtocol checks that a value outside "ANY",
// "tcp", "udp", and "icmp" (case-insensitive) is refused before any
// request, unlike Direction and Action, which the server checks itself.
func TestAddNetworkACLRuleBadProtocol(t *testing.T) {
	cases := []string{"foo", "ip", "tcpx", " tcp"}
	for _, protocol := range cases {
		t.Run(protocol, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: protocol, CIDR: "203.0.113.0/24", Action: "pass",
				PortRangeMin: 22, PortRangeMax: 22,
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestAddNetworkACLRuleProtocolPortRestrictionsRefused checks the port
// range each protocol requires: "ANY" must be the full range, "icmp" must
// be the full range or 0 and 0 together, and "tcp" and "udp" must not
// leave both PortRangeMin and PortRangeMax at 0, which sends a literal
// port "0" that may mean every port; any other pair is refused before any
// request.
func TestAddNetworkACLRuleProtocolPortRestrictionsRefused(t *testing.T) {
	cases := []struct {
		name             string
		protocol         string
		portMin, portMax int
	}{
		{"ANY single port refused", "ANY", 22, 22},
		{"any zero-zero refused", "any", 0, 0},
		{"icmp arbitrary port refused", "icmp", 22, 22},
		{"icmp partial range refused", "ICMP", 0, 100},
		{"tcp zero-zero refused", "tcp", 0, 0},
		{"udp zero-zero refused", "UDP", 0, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: tt.protocol, CIDR: "203.0.113.0/24", Action: "pass",
				PortRangeMin: tt.portMin, PortRangeMax: tt.portMax,
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestRemoveNetworkACLRuleBadPriority(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: -1})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// --- AddNetworkACLRule read-merge ---

func TestAddNetworkACLRuleRequestBodyHoldsDefaultAndNewRules(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule}, nil), // pre-PUT recheck: unchanged
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule, {Type: "inbound", SeqNumber: 100, Protocol: "tcp", Port: "443", Source: "203.0.113.0/24", Action: "pass"}}, nil), // confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v2/project-1/network-acl/acl-1/rules" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		if body["aclId"] != "acl-1" {
			t.Fatalf("aclId in body = %v, want acl-1", body["aclId"])
		}
		rules, _ := body["detailAclRuleList"].([]any)
		if len(rules) != 2 {
			t.Fatalf("rules in body = %+v, want 2 entries (the default rule resent, plus the new one)", rules)
		}
		first, _ := rules[0].(map[string]any)
		if first["seqNumber"] != float64(2000) || first["system"] != false {
			t.Fatalf("first rule = %+v, want the deny-all default rule resent unchanged, System false as read", first)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(out.ACL.Rules) != 2 {
		t.Fatalf("Rules = %+v, want 2 entries", out.ACL.Rules)
	}
}

func TestAddNetworkACLRuleAlreadyPresentSameFieldsNoOp(t *testing.T) {
	existing := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "tcp", Port: "443", Source: "203.0.113.0/24", Action: "pass"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{existing}, nil)))
		default:
			t.Fatalf("unexpected method %s: an unchanged add must send no PUT", r.Method)
		}
	}))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		// Direction differs only in case, which the key comparison ignores.
		NetworkACLID: "acl-1", Direction: "INBOUND", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAddNetworkACLRuleConflictingFieldsErrInvalidInput(t *testing.T) {
	existing := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "tcp", Port: "443", Source: "203.0.113.0/24", Action: "pass"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{existing}, nil)))
		default:
			t.Fatalf("unexpected method %s: a conflicting add must send no PUT", r.Method)
		}
	}))

	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "UDP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestAddNetworkACLRulePreWriteBoundErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("UPDATING", false, nil, nil)))
		default:
			t.Fatalf("unexpected method %s: a busy ACL must send no PUT", r.Method)
		}
	})))

	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAddNetworkACLRulePreWriteRecheckMismatchErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule, {Type: "outbound", SeqNumber: 200, Protocol: "udp", Port: "53-53", Source: "198.51.100.0/24", Action: "pass"}}, nil), // pre-PUT recheck: changed
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT: a mismatched recheck must send nothing")
		}
	})))

	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAddNetworkACLRuleConfirmMismatchErrNotSettled(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, nil, nil), // pre-write read
		aclJSON("ACTIVE", false, nil, nil), // pre-PUT recheck: unchanged
		aclJSON("ACTIVE", false, []aclRuleEntry{{Type: "outbound", SeqNumber: 999, Protocol: "ANY", Port: "0-65535", Source: "0.0.0.0/0", Action: "pass"}}, nil), // confirm: wrong rules
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
		}
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || !out.Changed {
		t.Fatal("out = nil or Changed = false: the PUT was sent even though the confirm read did not match")
	}
}

func TestAddNetworkACLRuleWaitErrFailed(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, nil, nil), // pre-write read
		aclJSON("ACTIVE", false, nil, nil), // pre-PUT recheck: unchanged
		aclJSON("ERROR", false, nil, nil),  // confirm read: the ACL reached ERROR
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
		}
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.ACL.Status != "ERROR" {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR ACL", out)
	}
}

func TestAddNetworkACLRuleNoWaitSkipsPostWritePoll(t *testing.T) {
	var getCalls int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls != 2 {
		t.Fatalf("GET calls = %d, want 2 (the pre-write read and the pre-PUT recheck): NoWait must skip only the post-write poll", getCalls)
	}
	if len(out.ACL.Rules) != 1 {
		t.Fatalf("Rules = %+v, want the one rule just sent", out.ACL.Rules)
	}
}

// --- RemoveNetworkACLRule read-merge ---

func TestRemoveNetworkACLRuleRequestBodyDropsOnlyTheNamedRuleKeepsDefault(t *testing.T) {
	removed := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "tcp", Port: "443", Source: "203.0.113.0/24", Action: "pass"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule, removed}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule, removed}, nil), // pre-PUT recheck: unchanged
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule}, nil),          // confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		body := decodeBody(t, r)
		rules, _ := body["detailAclRuleList"].([]any)
		if len(rules) != 1 {
			t.Fatalf("rules in body = %+v, want 1 entry (the default rule, resent)", rules)
		}
		entry, _ := rules[0].(map[string]any)
		if entry["seqNumber"] != float64(2000) {
			t.Fatalf("remaining rule = %+v, want the deny-all default rule kept", entry)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: removed.Type, Priority: removed.SeqNumber})
	if err != nil {
		t.Fatalf("RemoveNetworkACLRule() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(out.ACL.Rules) != 1 {
		t.Fatalf("Rules = %+v, want 1 entry", out.ACL.Rules)
	}
}

func TestRemoveNetworkACLRuleAbsentReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule}, nil)))
		default:
			t.Fatalf("unexpected method %s: removing an absent rule must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 999})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

// TestRemoveNetworkACLRulePriorityZeroPassAllRemovable checks fact 3
// (confirmed live): a PUT that leaves out the seqNumber-0 pass-all rule
// removes it, so this SDK must let a caller remove it too, unlike a rule
// at seqNumber 2000 or above.
func TestRemoveNetworkACLRulePriorityZeroPassAllRemovable(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule, denyAllInboundRule}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule, denyAllInboundRule}, nil), // pre-PUT recheck: unchanged
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule}, nil),                     // confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		body := decodeBody(t, r)
		rules, _ := body["detailAclRuleList"].([]any)
		if len(rules) != 1 {
			t.Fatalf("rules in body = %+v, want 1 entry (only the deny-all default rule)", rules)
		}
		entry, _ := rules[0].(map[string]any)
		if entry["seqNumber"] != float64(2000) {
			t.Fatalf("remaining rule = %+v, want the deny-all default rule, seqNumber 0 dropped", entry)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 0})
	if err != nil {
		t.Fatalf("RemoveNetworkACLRule() error = %v, want the priority-0 pass-all rule to be removable", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

// TestRemoveNetworkACLRuleAtPriority1999Removable checks the boundary
// opposite TestRemoveNetworkACLRuleDenyAllAtPriority2000Refused: 1999,
// confirmed live as a working user priority, is an ordinary rule, not a
// default one.
func TestRemoveNetworkACLRuleAtPriority1999Removable(t *testing.T) {
	atBound := aclRuleEntry{Type: "inbound", SeqNumber: 1999, Protocol: "tcp", Port: "22", Source: "203.0.113.0/24", Action: "pass"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule, atBound}, nil)))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 1999, NoWait: true})
	if err != nil {
		t.Fatalf("RemoveNetworkACLRule() error = %v, want priority 1999 to be removable", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

// TestRemoveNetworkACLRuleDenyAllAtPriority2000Refused checks fact 3
// (confirmed live): a PUT that leaves out the seqNumber-2000 deny-all rule
// keeps it, so this SDK refuses to remove it at all, nothing sent.
func TestRemoveNetworkACLRuleDenyAllAtPriority2000Refused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule, denyAllInboundRule}, nil)))
		default:
			t.Fatalf("unexpected method %s: removing a default rule must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 2000})
	if err == nil || !strings.Contains(err.Error(), "default rule") {
		t.Fatalf("err = %v, want a default-rule refusal", err)
	}
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

func TestRemoveNetworkACLRuleDefaultBySystemFlagRefused(t *testing.T) {
	// A default rule inside the user priority range, marked only by its
	// decoded System flag.
	systemFlagged := aclRuleEntry{Type: "inbound", SeqNumber: 500, Protocol: "ANY", Port: "0-65535", Source: "0.0.0.0/0", Action: "deny", System: true}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{passAllInboundRule, systemFlagged}, nil)))
		default:
			t.Fatalf("unexpected method %s: removing a default rule must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 500})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

func TestRemoveNetworkACLRulePreWriteRecheckMismatchErrBusy(t *testing.T) {
	removed := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "tcp", Port: "443", Source: "203.0.113.0/24", Action: "pass"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule, removed}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{denyAllInboundRule}, nil),          // pre-PUT recheck: removed is already gone
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT: a mismatched recheck must send nothing")
		}
	})))

	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: removed.Type, Priority: removed.SeqNumber})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAddNetworkACLRulePUT4xxSurfacesAPIError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad rule"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
}

// TestAddNetworkACLRulePUTBusyMapsToErrBusy checks the busy window
// confirmed live: a rules PUT sent while the ACL is still busy from a
// previous write returns 400 with a message naming the ACL busy; this SDK
// maps that to ErrBusy, since the PUT was rejected outright and changed
// nothing.
func TestAddNetworkACLRulePUTBusyMapsToErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The ACL with id acl-1 is busy doing something"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "tcp", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if out != nil {
		t.Fatalf("out = %+v, want nil: nothing was changed", out)
	}
}

// TestAddNetworkACLRulePUT5xxNotSettledSingleAttempt checks that the rules
// PUT is sent with Once true: a 502, 503, or 504 is never retried by the
// transport, which could otherwise land a second attempt in the ACL's own
// roughly 18-second busy window and be misread as ErrBusy when the first
// attempt may already have reached the server. Such a failure wraps
// ErrNotSettled instead, never ErrBusy.
func TestAddNetworkACLRulePUT5xxNotSettledSingleAttempt(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var putCalls atomic.Int64
			c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
				case http.MethodPut:
					putCalls.Add(1)
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"upstream error"}`))
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			})))

			out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "tcp", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
			})
			if !errors.Is(err, ErrNotSettled) {
				t.Fatalf("err = %v, want ErrNotSettled", err)
			}
			if errors.Is(err, ErrBusy) {
				t.Fatalf("err = %v, must not also be ErrBusy: the PUT may have reached the server", err)
			}
			if out != nil {
				t.Fatalf("out = %+v, want nil: whether the write landed is unknown", out)
			}
			if putCalls.Load() != 1 {
				t.Fatalf("PUT calls = %d, want exactly 1: Once must stop the transport from retrying a %d", putCalls.Load(), status)
			}
		})
	}
}

// TestAddNetworkACLRulePUT404SurfacesNotFound checks that a plain 404 on
// the rules PUT, unlike a busy 400 or a 5xx, is returned as is: it is a 4xx
// the server rejected outright, so it is not wrapped in ErrBusy or
// ErrNotSettled.
func TestAddNetworkACLRulePUT404SurfacesNotFound(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case http.MethodPut:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "tcp", CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
	})
	if !core.IsNotFound(err) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
	if errors.Is(err, ErrBusy) || errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, must not be ErrBusy or ErrNotSettled: a plain 404 is a clean rejection", err)
	}
	if out != nil {
		t.Fatalf("out = %+v, want nil", out)
	}
}

// TestAddNetworkACLRuleNoOpComparesParsedCIDRAndFoldedAction checks that an
// existing rule's CIDR and Action are compared to the caller's own as a
// parsed prefix and a case-folded value, not as raw strings, so an
// equivalent but differently spelled CIDR or Action is still a no-op
// instead of a false ErrInvalidInput conflict.
func TestAddNetworkACLRuleNoOpComparesParsedCIDRAndFoldedAction(t *testing.T) {
	existing := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "ANY", Port: "0-65535", Source: "2001:db8::/32", Action: "pass"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{existing}, nil)))
		default:
			t.Fatalf("unexpected method %s: an equivalent CIDR and Action must be a no-op, no PUT", r.Method)
		}
	}))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "any",
		CIDR: "2001:DB8::/32", Action: "PASS", PortRangeMin: 0, PortRangeMax: 65535,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false: an equivalent CIDR and Action must be a no-op")
	}
}

// TestAddNetworkACLRuleSendsCanonicalCIDR checks that a new rule's CIDR is
// sent in its canonical net/netip.Prefix form, not the caller's own
// spelling, so a rerun and the post-write confirm compare like for like.
func TestAddNetworkACLRuleSendsCanonicalCIDR(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case http.MethodPut:
			body := decodeBody(t, r)
			rules, _ := body["detailAclRuleList"].([]any)
			entry, _ := rules[0].(map[string]any)
			if entry["source"] != "2001:db8::/32" {
				t.Fatalf("source in body = %v, want the canonical 2001:db8::/32", entry["source"])
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "ANY",
		CIDR: "2001:DB8::/32", Action: "pass", PortRangeMin: 0, PortRangeMax: 65535, NoWait: true,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
}
