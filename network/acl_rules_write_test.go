package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// --- AddNetworkACLRule and RemoveNetworkACLRule shape checks ---

func TestAddNetworkACLRuleBadPriority(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: -1, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
	})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
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
		aclJSON("ACTIVE", false, []aclRuleEntry{defaultInboundRule}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{defaultInboundRule, {Type: "inbound", SeqNumber: 100, Protocol: "TCP", Port: "443-443", Source: "203.0.113.0/24", Action: "pass"}}, nil), // confirm read
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
		if first["seqNumber"] != float64(0) || first["system"] != true {
			t.Fatalf("first rule = %+v, want the default rule resent unchanged with system=true", first)
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
	existing := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "TCP", Port: "443-443", Source: "203.0.113.0/24", Action: "pass"}
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
	existing := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "TCP", Port: "443-443", Source: "203.0.113.0/24", Action: "pass"}
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
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAddNetworkACLRuleConfirmMismatchErrNotSettled(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, nil, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{{Type: "outbound", SeqNumber: 999, Protocol: "ANY", Port: "0-65535", Source: "0.0.0.0/0", Action: "pass"}}, nil), // confirm: wrong rules
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
		}
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
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
		aclJSON("ERROR", false, nil, nil),  // confirm read: the ACL reached ERROR
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
		}
	})))

	out, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
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
		NetworkACLID: "acl-1", Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass", NoWait: true,
	})
	if err != nil {
		t.Fatalf("AddNetworkACLRule() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-write read only): NoWait must skip the post-write poll", getCalls)
	}
	if len(out.ACL.Rules) != 1 {
		t.Fatalf("Rules = %+v, want the one rule just sent", out.ACL.Rules)
	}
}

// --- RemoveNetworkACLRule read-merge ---

func TestRemoveNetworkACLRuleRequestBodyDropsOnlyTheNamedRuleKeepsDefault(t *testing.T) {
	removed := aclRuleEntry{Type: "inbound", SeqNumber: 100, Protocol: "TCP", Port: "443-443", Source: "203.0.113.0/24", Action: "pass"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		aclJSON("ACTIVE", false, []aclRuleEntry{defaultInboundRule, removed}, nil), // pre-write read
		aclJSON("ACTIVE", false, []aclRuleEntry{defaultInboundRule}, nil),          // confirm read
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
		if entry["seqNumber"] != float64(0) {
			t.Fatalf("remaining rule = %+v, want the default rule kept", entry)
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
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{defaultInboundRule}, nil)))
		default:
			t.Fatalf("unexpected method %s: removing an absent rule must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 999})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestRemoveNetworkACLRuleDefaultRuleRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, []aclRuleEntry{defaultInboundRule}, nil)))
		default:
			t.Fatalf("unexpected method %s: removing a default rule must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: "acl-1", Direction: "inbound", Priority: 0})
	if err == nil || !strings.Contains(err.Error(), "default rule") {
		t.Fatalf("err = %v, want a default-rule refusal", err)
	}
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}
