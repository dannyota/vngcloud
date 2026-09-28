package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// aclJSON builds a {"data": {...}} network ACL envelope matching
// GetNetworkACL's decode shape, for tests that need to control the rule or
// subnet list, status, or DefaultACL flag a GET returns. Every ACL it
// builds is "acl-1" in "vpc-1", the fixed ids this package's ACL write
// tests key their other responses on.
func aclJSON(status string, defaultACL bool, rules []aclRuleEntry, subnetIDs []string) string {
	type ruleJSON struct {
		UUID                   string `json:"uuid"`
		SeqNumber              int    `json:"seqNumber"`
		Protocol               string `json:"protocol"`
		Type                   string `json:"type"`
		Port                   string `json:"port"`
		Source                 string `json:"source"`
		Action                 string `json:"action"`
		System                 bool   `json:"system"`
		InterfaceACLPolicyUUID string `json:"interfaceAclPolicyUuid"`
	}
	rs := make([]ruleJSON, len(rules))
	for i, r := range rules {
		rs[i] = ruleJSON{
			UUID: "aclr-x", SeqNumber: r.SeqNumber, Protocol: r.Protocol,
			Type: r.Type, Port: r.Port, Source: r.Source, Action: r.Action,
			System: r.System, InterfaceACLPolicyUUID: "acl-1",
		}
	}
	var fixture struct {
		Data struct {
			UUID                  string     `json:"uuid"`
			Status                string     `json:"status"`
			DefaultACL            bool       `json:"defaultAcl"`
			InterfaceNetworkUUID  string     `json:"interfaceNetworkUuid"`
			CreatedAt             string     `json:"createdAt"`
			SubnetAssociationList []string   `json:"subnetAssociationList"`
			ACLPolicyRules        []ruleJSON `json:"aclPolicyRules"`
		} `json:"data"`
	}
	fixture.Data.UUID = "acl-1"
	fixture.Data.Status = status
	fixture.Data.DefaultACL = defaultACL
	fixture.Data.InterfaceNetworkUUID = "vpc-1"
	fixture.Data.CreatedAt = "2026-01-01T00:00:00Z"
	fixture.Data.SubnetAssociationList = subnetIDs
	fixture.Data.ACLPolicyRules = rs
	b, err := json.Marshal(fixture)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// passAllInboundRule is the ordinary rule a new ACL starts with: an inbound
// pass-all at priority (seqNumber) 0. Confirmed live, a new ACL's own GET
// carries no system field at all for this rule, so it decodes System
// false; isDefaultACLRule does not mark it default by Priority alone, and
// a caller may remove it like any other rule.
var passAllInboundRule = aclRuleEntry{Type: "inbound", SeqNumber: 0, Protocol: "ANY", Port: "0-65535", Source: "0.0.0.0/0", Action: "pass"}

// denyAllInboundRule is one of the default rules a new ACL cannot go
// without: an inbound deny-all at priority (seqNumber) 2000, confirmed
// live. Like passAllInboundRule, its GET carries no system field; unlike
// it, isDefaultACLRule marks this one default by Priority alone (2000 is
// at aclDefaultRulePriority, one past aclMaxUserPriority).
var denyAllInboundRule = aclRuleEntry{Type: "inbound", SeqNumber: 2000, Protocol: "ANY", Port: "0-65535", Source: "0.0.0.0/0", Action: "deny"}

// --- GetNetworkACL ---

func TestGetNetworkACLDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/project-1/network-acl/acl-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/get_network_acl.json")
	}))

	out, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: "acl-1"})
	if err != nil {
		t.Fatalf("GetNetworkACL() error = %v", err)
	}
	acl := out.ACL
	if acl.UUID != "acl-1" || acl.VPCID != "vpc-1" || acl.Status != "ACTIVE" || acl.DefaultACL {
		t.Fatalf("unexpected ACL: %+v", acl)
	}
	if len(acl.SubnetIDs) != 2 || acl.SubnetIDs[0] != "subnet-1" || acl.SubnetIDs[1] != "subnet-2" {
		t.Fatalf("SubnetIDs = %+v, want [subnet-1 subnet-2]", acl.SubnetIDs)
	}
	if len(acl.Rules) != 5 {
		t.Fatalf("Rules = %+v, want 5 entries (pass-all in/out, the user rule, deny-all in/out)", acl.Rules)
	}
	if acl.Rules[0].Priority != 0 || acl.Rules[0].Direction != "inbound" || acl.Rules[0].Action != "pass" {
		t.Fatalf("default pass-all rule = %+v, unexpected", acl.Rules[0])
	}
	if acl.Rules[2].Priority != 100 || acl.Rules[2].Protocol != "tcp" || acl.Rules[2].Port != "443" {
		t.Fatalf("user rule = %+v, unexpected", acl.Rules[2])
	}
	for _, i := range []int{3, 4} {
		if acl.Rules[i].Priority != 2000 || acl.Rules[i].Protocol != "ANY" || acl.Rules[i].Port != "0-65535" ||
			acl.Rules[i].CIDR != "0.0.0.0/0" || acl.Rules[i].Action != "deny" {
			t.Fatalf("default deny-all rule = %+v, unexpected", acl.Rules[i])
		}
	}
	if acl.Rules[3].Direction != "inbound" || acl.Rules[4].Direction != "outbound" {
		t.Fatalf("deny-all rules directions = %q, %q, want inbound, outbound", acl.Rules[3].Direction, acl.Rules[4].Direction)
	}
}

func TestGetNetworkACL500AbsentFromListReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: "acl-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestGetNetworkACL500ListedReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: "acl-1"})
	if err == nil {
		t.Fatal("err = nil, want the original 500")
	}
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the ACL is still listed", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want a 500 *core.APIError", err)
	}
}

func TestGetNetworkACL500ListFailureReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"list also failed"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: "acl-1"})
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the list call itself failed", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError", err)
	}
}

func TestGetNetworkACL500ListIncompleteReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			// acl-1 is absent from this page, but the list has more items
			// than this one page returned, so absence here is inconclusive.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-2"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: "acl-1"})
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the list has more than one page", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError", err)
	}
}

func TestGetNetworkACLBare404IsNotFoundWithNoListCall(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/network-acl/acl-x" {
			t.Fatalf("unexpected request: %s %s: a plain 404 needs no list confirm", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: "acl-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

// --- CreateNetworkACL ---

func TestCreateNetworkACLRequestBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/network-acl" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		if body["name"] != "web-acl" || body["vpc"] != "vpc-1" {
			t.Fatalf("body = %+v, want name=web-acl vpc=vpc-1", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"uuid":"acl-2"}}`))
	}))

	out, err := c.CreateNetworkACL(context.Background(), &CreateNetworkACLInput{VPCID: "vpc-1", Name: "web-acl"})
	if err != nil {
		t.Fatalf("CreateNetworkACL() error = %v", err)
	}
	if out.ACL.UUID != "acl-2" {
		t.Fatalf("UUID = %q, want acl-2", out.ACL.UUID)
	}
}

func TestCreateNetworkACLDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/network/create_network_acl.json")
	}))

	out, err := c.CreateNetworkACL(context.Background(), &CreateNetworkACLInput{VPCID: "vpc-1", Name: "<name>"})
	if err != nil {
		t.Fatalf("CreateNetworkACL() error = %v", err)
	}
	if out.ACL.UUID != "acl-1" || out.ACL.VPCID != "vpc-1" || out.ACL.Status != "ACTIVE" || out.ACL.DefaultACL {
		t.Fatalf("unexpected ACL: %+v", out.ACL)
	}
}

func TestCreateNetworkACLNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))

	_, err := c.CreateNetworkACL(context.Background(), &CreateNetworkACLInput{VPCID: "vpc-1", Name: "web-acl"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list network ACLs") || !strings.Contains(apiErr.Message, "createdAt") {
		t.Fatalf("err = %v, want an APIError naming list network ACLs and createdAt: ACL names repeat, so a same-name ACL may already exist", err)
	}
}

func TestCreateNetworkACLNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateNetworkACL(context.Background(), &CreateNetworkACLInput{VPCID: "vpc-1", Name: "web-acl"})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list network ACLs") || !strings.Contains(err.Error(), "createdAt") {
		t.Fatalf("err = %v, want a hint to list network ACLs by name and compare createdAt: ACL names repeat, so a same-name ACL may already exist", err)
	}
}

func TestCreateNetworkACLRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateNetworkACL(context.Background(), &CreateNetworkACLInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateNetworkACL(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

// --- DeleteNetworkACL ---

func TestDeleteNetworkACLWithSubnetsRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{"subnet-1"})))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteNetworkACLDefaultACLRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", true, nil, nil)))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

func TestDeleteNetworkACLSuccess(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"}); err != nil {
		t.Fatalf("DeleteNetworkACL() error = %v", err)
	}
}

func TestDeleteNetworkACL500AbsentFromListSucceeds(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"}); err != nil {
		t.Fatalf("DeleteNetworkACL() error = %v, want nil: the ACL is absent from the list confirm", err)
	}
}

func TestDeleteNetworkACL500ListedReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError: the ACL is still listed", err)
	}
}

func TestDeleteNetworkACL500ListFailureReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"list also failed"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError: the list call itself failed", err)
	}
}

// TestDeleteNetworkACLBusyMapsToErrBusy checks the busy window confirmed
// live: a DELETE sent while the ACL is still busy from a previous write
// returns 400 with a message naming the ACL busy; this SDK maps that to
// ErrBusy, the same way the rules and subnets PUT do.
func TestDeleteNetworkACLBusyMapsToErrBusy(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The ACL with id acl-1 is busy doing something"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// TestDeleteNetworkACLBusyBeingUpdatedMapsToErrBusy checks the busy window
// confirmed live after a subnets PUT: a DELETE sent while the ACL is still
// busy from that write returns 400 with a message naming the ACL "is being
// updated" rather than "is busy doing something"; this SDK maps that
// message to ErrBusy too.
func TestDeleteNetworkACLBusyBeingUpdatedMapsToErrBusy(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"The ACL with id acl-1 is being updated"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestDeleteNetworkACL500ListIncompleteReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, nil)))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/network-acl/acl-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/network-acl/list":
			// acl-1 is absent, but the list reports fewer items than
			// TotalItem, so absence here is inconclusive.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError: the list reports more items than this page returned", err)
	}
}

// --- Path ID checks ---

func TestNetworkACLPathIDRejection(t *testing.T) {
	badIDs := []string{"..", ".", "a/b", "a?b", ""}

	for _, id := range badIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))

			if _, err := c.GetNetworkACL(context.Background(), &GetNetworkACLInput{NetworkACLID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("GetNetworkACL() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DeleteNetworkACL() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.AddNetworkACLRule(context.Background(), &AddNetworkACLRuleInput{
				NetworkACLID: id, Direction: "inbound", Priority: 100, Protocol: "TCP", CIDR: "203.0.113.0/24", Action: "pass",
			}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("AddNetworkACLRule() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.RemoveNetworkACLRule(context.Background(), &RemoveNetworkACLRuleInput{NetworkACLID: id, Direction: "inbound", Priority: 100}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("RemoveNetworkACLRule() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: id, SubnetID: "subnet-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("AssociateNetworkACLSubnet() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: id, SubnetID: "subnet-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DisassociateNetworkACLSubnet() err = %v, want ErrInvalidInput", err)
			}

			if id == "" {
				return
			}
			if _, err := c.AssociateNetworkACLSubnet(context.Background(), &AssociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("AssociateNetworkACLSubnet() SubnetID err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DisassociateNetworkACLSubnet(context.Background(), &DisassociateNetworkACLSubnetInput{NetworkACLID: "acl-1", SubnetID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DisassociateNetworkACLSubnet() SubnetID err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
