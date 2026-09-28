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
)

// aclJSON builds GetNetworkACL's, CreateNetworkACL's, or a write's own
// response envelope: data.uuid, interfaceNetworkUuid, name, defaultAcl,
// status, subnetAssociationList, and aclPolicyRules, the fields
// GetNetworkACLOutput and every ACL write's Output decode. Every ACL it
// builds is "acl-1" in "vpc-1", the only ACL and VPC every test in this
// file needs.
func aclJSON(name string, defaultACL bool, subnetIDs []string, rules []map[string]any) string {
	body := map[string]any{
		"uuid": "acl-1", "interfaceNetworkUuid": "vpc-1", "name": name,
		"defaultAcl": defaultACL, "status": "ACTIVE",
		"subnetAssociationList": subnetIDs, "aclPolicyRules": rules,
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// ruleJSON builds one entry of aclPolicyRules, the shape a GetNetworkACL
// read (and so a rules PUT's own post-write confirm read) returns for one
// rule: uuid, type, seqNumber, protocol, port, source, and action. Every
// rule this file builds is inbound, the only direction any test here needs,
// so type is always "inbound" rather than a parameter.
func ruleJSON(uuid string, priority int, protocol, port, cidr, action string) map[string]any {
	return map[string]any{
		"uuid": uuid, "type": "inbound", "seqNumber": priority,
		"protocol": protocol, "port": port, "source": cidr, "action": action,
	}
}

// defaultInboundPassAllRuleJSON is one default rule most tests in this file
// use: an inbound rule at priority 0 that passes every port from every
// source, the shape confirmed live for a new ACL's own pass-all rule.
// Confirmed live, this rule carries no "system" field: it is an ordinary
// rule a caller may remove, unlike the deny-all rule at priority 2000 (see
// defaultInboundDenyAllRuleJSON), which is default by priority alone.
func defaultInboundPassAllRuleJSON() map[string]any {
	return ruleJSON("aclr-default", 0, "ANY", "0-65535", "0.0.0.0/0", "pass")
}

// defaultInboundDenyAllRuleJSON is a new ACL's other default rule: an
// inbound rule at priority 2000 that denies every port from every source,
// the shape confirmed live for a new ACL's own deny-all rule. It carries no
// "system" field either; its priority alone marks it default
// (isDefaultACLRule).
func defaultInboundDenyAllRuleJSON() map[string]any {
	return ruleJSON("aclr-deny", 2000, "ANY", "0-65535", "0.0.0.0/0", "deny")
}

// scriptedACLGetHandler answers each successive GET with the next body of
// bodies, holding on the last one once the script runs out, so a pre-write
// read and a post-write confirm read can each see a different snapshot of
// the same ACL, the same shape scriptedRouteTableGetHandler uses for route
// tables.
func scriptedACLGetHandler(bodies ...string) func(http.ResponseWriter, *http.Request) {
	i := 0
	return func(w http.ResponseWriter, _ *http.Request) {
		body := bodies[i]
		if i < len(bodies)-1 {
			i++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// unexpectedRequestHandler fails the test if the fixture ever routes a
// request to it, for a path a test asserts must never be called.
func unexpectedRequestHandler(t *testing.T) func(http.ResponseWriter, *http.Request) {
	return func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

func TestNetworkGetNetworkACLEndToEnd(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": jsonHandler(http.StatusOK, aclJSON("web", false,
			[]string{"sub-1"}, []map[string]any{defaultInboundPassAllRuleJSON()})),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "get-network-acl", "--network-acl-id", "acl-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-network-acl: %v (stderr=%s)", err, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, `"UUID": "acl-1"`) || !strings.Contains(got, `"VPCID": "vpc-1"`) ||
		!strings.Contains(got, `"DefaultACL": false`) || !strings.Contains(got, `"Priority": 0`) {
		t.Fatalf("stdout = %s, want the ACL with its VPC, default marker, and rule", got)
	}
}

// TestNetworkCreateNetworkACLEndToEnd checks the flag-to-body mapping
// (--vpc-id and --name become vpc and name) and that the create response,
// already ACTIVE with no wait, comes back on stdout.
func TestNetworkCreateNetworkACLEndToEnd(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(aclJSON("web", false, nil, nil)))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-network-acl", "--vpc-id", "vpc-1", "--name", "web",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-network-acl: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "web" || decoded["vpc"] != "vpc-1" {
		t.Fatalf("body = %s, want name=web and vpc=vpc-1", body)
	}
	got := stdout.String()
	if !strings.Contains(got, `"UUID": "acl-1"`) || !strings.Contains(got, `"Status": "ACTIVE"`) {
		t.Fatalf("stdout = %s, want the settled ACTIVE ACL", got)
	}
}

// TestNetworkCreateNetworkACLCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests
// checks --cli-input-json's strictness: a key that names no Input field is
// refused before any request.
func TestNetworkCreateNetworkACLCLIInputJSONUnknownKeyIsUsageErrorWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "create-network-acl", "--vpc-id", "vpc-1", "--name", "web",
		"--cli-input-json", `{"Bogus":"x"}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestNetworkDeleteNetworkACLRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "delete-network-acl", "--network-acl-id", "acl-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDeleteNetworkACLWithYesSendsGetThenDelete checks the success
// path's exact request sequence: the pre-delete read, then the DELETE.
func TestNetworkDeleteNetworkACLWithYesSendsGetThenDelete(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(aclJSON("web", false, nil, nil)))
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-network-acl", "--network-acl-id", "acl-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-network-acl: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (the pre-delete read, then the delete)", n)
	}
}

// TestNetworkDeleteNetworkACLInUseWithSubnetsExitsResourceInUseWithNoDelete
// checks the ErrInUse guard end to end: an associated subnet stops the
// delete before any DELETE, with the ResourceInUse code.
func TestNetworkDeleteNetworkACLInUseWithSubnetsExitsResourceInUseWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": jsonHandler(http.StatusOK, aclJSON("web", false, []string{"sub-1"}, nil)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-network-acl", "--network-acl-id", "acl-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-delete read only)", n)
	}
}

// TestNetworkDeleteNetworkACLDefaultACLExitsDefaultResourceWithNoDelete
// checks the ErrDefaultResource guard end to end.
func TestNetworkDeleteNetworkACLDefaultACLExitsDefaultResourceWithNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": jsonHandler(http.StatusOK, aclJSON("default", true, nil, nil)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "delete-network-acl", "--network-acl-id", "acl-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "DefaultResource" {
		t.Fatalf("Code = %q, want DefaultResource (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-delete read only)", n)
	}
}

// TestNetworkAddNetworkACLRuleRequiresYesWithZeroRequests checks
// requireYesForACLChange: without --yes, add-network-acl-rule sends
// nothing, not even the pre-write read.
func TestNetworkAddNetworkACLRuleRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "add-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "100", "--protocol", "TCP",
		"--cidr", "203.0.113.0/24", "--action", "pass",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkAddNetworkACLRuleEndToEndSendsDefaultPlusNewRule checks the
// flag-to-entry mapping (--direction, --priority, --protocol, --cidr,
// --action, and --port-range-min) and the read-merge body: the PUT holds
// the default rule the pre-write read found, resent unchanged, plus the new
// rule, and the settled ACL (from the post-write confirm read) comes back
// with Changed true.
func TestNetworkAddNetworkACLRuleEndToEndSendsDefaultPlusNewRule(t *testing.T) {
	var putBody []byte
	newRule := ruleJSON("aclr-2", 100, "tcp", "443", "203.0.113.0/24", "pass")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": scriptedACLGetHandler(
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON()}),
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON()}),
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON(), newRule}),
		),
		"/v2/proj-1/network-acl/acl-1/rules": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "add-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "100", "--protocol", "TCP",
		"--cidr", "203.0.113.0/24", "--action", "pass", "--port-range-min", "443",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("add-network-acl-rule: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		DetailACLRuleList []struct {
			Type      string `json:"type"`
			SeqNumber int    `json:"seqNumber"`
			Protocol  string `json:"protocol"`
			Port      string `json:"port"`
			Source    string `json:"source"`
			Action    string `json:"action"`
			System    bool   `json:"system"`
		} `json:"detailAclRuleList"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.DetailACLRuleList) != 2 {
		t.Fatalf("rules in body = %+v, want 2 entries (the default rule plus the new one)", decoded.DetailACLRuleList)
	}
	var sawDefault, sawNew bool
	for _, r := range decoded.DetailACLRuleList {
		switch r.SeqNumber {
		case 0:
			sawDefault = true
			if r.System {
				t.Fatalf("default rule entry = %+v, want System false: confirmed live, the priority-0 pass-all rule carries no system field", r)
			}
		case 100:
			sawNew = true
			if r.System || r.Protocol != "tcp" || r.Port != "443" || r.Source != "203.0.113.0/24" || r.Action != "pass" {
				t.Fatalf("new rule entry = %+v, want the flags mapped in and System false", r)
			}
		}
	}
	if !sawDefault || !sawNew {
		t.Fatalf("rules in body = %+v, want both the default rule and the new one", decoded.DetailACLRuleList)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkRemoveNetworkACLRuleRequiresPriorityFlagWithZeroRequests checks
// requireYesAndPriorityToRemoveACLRule's own --priority requirement: --yes
// alone is not enough, and nothing is sent, not even the pre-write read.
func TestNetworkRemoveNetworkACLRuleRequiresPriorityFlagWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-network-acl-rule", "--network-acl-id", "acl-1", "--direction", "inbound",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --priority")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "priority") {
		t.Fatalf("error = %q, want it to name --priority", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkRemoveNetworkACLRuleRequiresYesWithZeroRequests checks that
// --priority alone is not enough either.
func TestNetworkRemoveNetworkACLRuleRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "remove-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "100",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkRemoveNetworkACLRuleEndToEndSendsRemainingRules checks the
// read-merge body: the PUT holds the default rule the pre-write read found,
// resent unchanged, but not the removed user rule, and the settled ACL
// comes back with Changed true.
func TestNetworkRemoveNetworkACLRuleEndToEndSendsRemainingRules(t *testing.T) {
	var putBody []byte
	userRule := ruleJSON("aclr-2", 100, "tcp", "443", "203.0.113.0/24", "pass")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": scriptedACLGetHandler(
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON(), userRule}),
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON(), userRule}),
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON()}),
		),
		"/v2/proj-1/network-acl/acl-1/rules": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "100",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("remove-network-acl-rule: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		DetailACLRuleList []struct {
			SeqNumber int `json:"seqNumber"`
		} `json:"detailAclRuleList"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.DetailACLRuleList) != 1 || decoded.DetailACLRuleList[0].SeqNumber != 0 {
		t.Fatalf("rules in body = %+v, want only the default rule (seqNumber 0)", decoded.DetailACLRuleList)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkRemoveNetworkACLRulePriorityZeroSendsPUTWithoutTheRemovedRule
// checks that the CLI lets --priority 0 through to the SDK, rather than
// mistaking it for "the flag was not given" (requireYesAndPriorityToRemoveACLRule
// checks cobra's Changed instead of the merged value for that reason), and
// that removal succeeds: confirmed live, a new ACL's own priority-0
// pass-all rule carries no "system" field and is an ordinary rule a caller
// may remove, unlike the priority-2000 deny-all rule (see the test below).
// The PUT resends the deny-all rule unchanged and drops the pass-all one.
func TestNetworkRemoveNetworkACLRulePriorityZeroSendsPUTWithoutTheRemovedRule(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": scriptedACLGetHandler(
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON(), defaultInboundDenyAllRuleJSON()}),
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON(), defaultInboundDenyAllRuleJSON()}),
			aclJSON("web", false, nil, []map[string]any{defaultInboundDenyAllRuleJSON()}),
		),
		"/v2/proj-1/network-acl/acl-1/rules": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "0",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("remove-network-acl-rule: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		DetailACLRuleList []struct {
			SeqNumber int `json:"seqNumber"`
		} `json:"detailAclRuleList"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.DetailACLRuleList) != 1 || decoded.DetailACLRuleList[0].SeqNumber != 2000 {
		t.Fatalf("rules in body = %+v, want only the deny-all rule (seqNumber 2000)", decoded.DetailACLRuleList)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkRemoveNetworkACLRulePriorityTwoThousandExitsDefaultResourceWithNoPUT
// checks the other side of the test above: a priority-2000 rule, the
// server's own deny-all default, is refused with DefaultResource and no
// PUT, even though --priority 0 is allowed. isDefaultACLRule treats any
// priority of 2000 or above as default regardless of its "system" field.
func TestNetworkRemoveNetworkACLRulePriorityTwoThousandExitsDefaultResourceWithNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": jsonHandler(http.StatusOK,
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON(), defaultInboundDenyAllRuleJSON()})),
		"/v2/proj-1/network-acl/acl-1/rules": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "2000",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "DefaultResource" {
		t.Fatalf("Code = %q, want DefaultResource (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-write read only)", n)
	}
}

// TestNetworkRemoveNetworkACLRuleNotFoundWhenMissingSendsNoPUT checks the
// design's missing-rule rule: removing a direction and priority the ACL
// does not have sends no PUT and returns NotFound.
func TestNetworkRemoveNetworkACLRuleNotFoundWhenMissingSendsNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": jsonHandler(http.StatusOK,
			aclJSON("web", false, nil, []map[string]any{defaultInboundPassAllRuleJSON()})),
		"/v2/proj-1/network-acl/acl-1/rules": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "remove-network-acl-rule", "--network-acl-id", "acl-1",
		"--direction", "inbound", "--priority", "999",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotFound" {
		t.Fatalf("Code = %q, want NotFound (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 4 {
		t.Fatalf("exitCode = %d, want 4", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the pre-write read only)", n)
	}
}

// TestNetworkAssociateNetworkACLSubnetRequiresYesWithZeroRequests checks
// requireYesForACLChange for associate-network-acl-subnet.
func TestNetworkAssociateNetworkACLSubnetRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "associate-network-acl-subnet", "--network-acl-id", "acl-1", "--subnet-id", "sub-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkAssociateNetworkACLSubnetEndToEndReadsSubnetThenSendsPUT checks
// the success path's exact request sequence: the pre-write ACL read, the
// subnet read under the ACL's own VPC, the pre-PUT recheck read, the PUT,
// then the confirm read, and that the PUT's subnet list holds the new
// subnet.
func TestNetworkAssociateNetworkACLSubnetEndToEndReadsSubnetThenSendsPUT(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": scriptedACLGetHandler(
			aclJSON("web", false, nil, nil),
			aclJSON("web", false, nil, nil),
			aclJSON("web", false, []string{"sub-1"}, nil),
		),
		"/v2/proj-1/networks/vpc-1/subnets/sub-1": jsonHandler(http.StatusOK,
			`{"uuid":"sub-1","status":"ACTIVE","cidr":"10.0.1.0/24","networkUuid":"vpc-1","name":"sub-1"}`),
		"/v2/proj-1/network-acl/acl-1/subnets": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "associate-network-acl-subnet", "--network-acl-id", "acl-1", "--subnet-id", "sub-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("associate-network-acl-subnet: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		SubnetUUIDs []string `json:"subnetUuids"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.SubnetUUIDs) != 1 || decoded.SubnetUUIDs[0] != "sub-1" {
		t.Fatalf("subnetUuids in body = %+v, want only sub-1", decoded.SubnetUUIDs)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
	if n := fixture.requestCount(); n != 5 {
		t.Fatalf("requestCount = %d, want 5 (acl read, subnet read, recheck read, put, confirm read)", n)
	}
}

// TestNetworkAssociateNetworkACLSubnetOtherVPCSubnetExitsNotFoundWithNoPUT
// checks that a subnet GetSubnet cannot find under the ACL's own VPC (a
// subnet of a different VPC, or an unknown id) is refused with NotFound
// before any PUT.
func TestNetworkAssociateNetworkACLSubnetOtherVPCSubnetExitsNotFoundWithNoPUT(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1":            jsonHandler(http.StatusOK, aclJSON("web", false, nil, nil)),
		"/v2/proj-1/networks/vpc-1/subnets/sub-2": jsonHandler(http.StatusNotFound, `{"message":"not found"}`),
		"/v2/proj-1/network-acl/acl-1/subnets":    unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "associate-network-acl-subnet", "--network-acl-id", "acl-1", "--subnet-id", "sub-2",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotFound" {
		t.Fatalf("Code = %q, want NotFound (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (acl read, subnet read, no put)", n)
	}
}

// TestNetworkDisassociateNetworkACLSubnetRequiresYesWithZeroRequests checks
// requireYesForACLChange for disassociate-network-acl-subnet.
func TestNetworkDisassociateNetworkACLSubnetRequiresYesWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": unexpectedRequestHandler(t),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"network", "disassociate-network-acl-subnet", "--network-acl-id", "acl-1", "--subnet-id", "sub-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestNetworkDisassociateNetworkACLSubnetEndToEndSendsRemainingSubnets
// checks the read-merge body: the PUT holds every subnet the pre-write read
// found except the one named, and the settled ACL comes back with Changed
// true.
func TestNetworkDisassociateNetworkACLSubnetEndToEndSendsRemainingSubnets(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/network-acl/acl-1": scriptedACLGetHandler(
			aclJSON("web", false, []string{"sub-1"}, nil),
			aclJSON("web", false, []string{"sub-1"}, nil),
			aclJSON("web", false, nil, nil),
		),
		"/v2/proj-1/network-acl/acl-1/subnets": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"network", "disassociate-network-acl-subnet", "--network-acl-id", "acl-1", "--subnet-id", "sub-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("disassociate-network-acl-subnet: %v (stderr=%s)", err, stderr.String())
	}

	var decoded struct {
		SubnetUUIDs []string `json:"subnetUuids"`
	}
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if len(decoded.SubnetUUIDs) != 0 {
		t.Fatalf("subnetUuids in body = %+v, want none", decoded.SubnetUUIDs)
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want Changed true", got)
	}
}

// TestNetworkACLWritesReadOnlyRefusedWithZeroRequests checks that a
// profile's own read_only setting refuses create-network-acl,
// delete-network-acl, add-network-acl-rule, remove-network-acl-rule,
// associate-network-acl-subnet, and disassociate-network-acl-subnet, before
// any request. The destructive and --yes-guarded commands also pass --yes
// (and, for remove-network-acl-rule, --priority), so the read-only refusal
// is unambiguously the reason in every case.
func TestNetworkACLWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"create-network-acl", []string{"create-network-acl", "--vpc-id", "vpc-1", "--name", "web"}},
		{"delete-network-acl", []string{"delete-network-acl", "--network-acl-id", "acl-1", "--yes"}},
		{"add-network-acl-rule", []string{"add-network-acl-rule", "--network-acl-id", "acl-1",
			"--direction", "inbound", "--priority", "100", "--protocol", "TCP",
			"--cidr", "203.0.113.0/24", "--action", "pass", "--yes"}},
		{"remove-network-acl-rule", []string{"remove-network-acl-rule", "--network-acl-id", "acl-1",
			"--direction", "inbound", "--priority", "100", "--yes"}},
		{"associate-network-acl-subnet", []string{"associate-network-acl-subnet",
			"--network-acl-id", "acl-1", "--subnet-id", "sub-1", "--yes"}},
		{"disassociate-network-acl-subnet", []string{"disassociate-network-acl-subnet",
			"--network-acl-id", "acl-1", "--subnet-id", "sub-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/network-acl":                  unexpectedRequestHandler(t),
				"/v2/proj-1/network-acl/acl-1":            unexpectedRequestHandler(t),
				"/v2/proj-1/network-acl/acl-1/rules":      unexpectedRequestHandler(t),
				"/v2/proj-1/network-acl/acl-1/subnets":    unexpectedRequestHandler(t),
				"/v2/proj-1/networks/vpc-1/subnets/sub-1": unexpectedRequestHandler(t),
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "network"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}
