package iam

import "testing"

func TestMatchesWriteAction(t *testing.T) {
	writeActions := writeActionNames([]Action{
		{Action: "CreatePolicy", Label: "Write"},
		{Action: "AttachPolicyToIamUser", Label: "Write"},
		{Action: "ListPolicies", Label: "List"},
		{Action: "GetPolicy", Label: "Read"},
		{Action: "TagPolicy", Label: "Tagging"},
	})

	privileged := []string{"*", "iam:*", "IAM:attach*", "iam:CreatePolicy"}
	for _, pattern := range privileged {
		if !matchesWriteAction(pattern, writeActions) {
			t.Errorf("matchesWriteAction(%q) = false, want true", pattern)
		}
	}

	// iam:List* matches only ListPolicies, whose label is List, so it is
	// never privileged: every action it matches is excluded from
	// writeActions in the first place.
	if matchesWriteAction("iam:List*", writeActions) {
		t.Error("matchesWriteAction(\"iam:List*\") = true, want false (every match is List)")
	}
	if matchesWriteAction("vserver:CreatePolicy", writeActions) {
		t.Error("matchesWriteAction(\"vserver:CreatePolicy\") = true, want false (different product)")
	}
}

func TestWriteActionNamesExcludesListReadTagging(t *testing.T) {
	actions := []Action{
		{Action: "ListPolicies", Label: "List"},
		{Action: "GetPolicy", Label: "Read"},
		{Action: "TagPolicy", Label: "Tagging"},
		{Action: "CreatePolicy", Label: "Write"},
		{Action: "SomeFutureAction", Label: "Something"},
	}
	names := writeActionNames(actions)
	want := map[string]bool{"iam:createpolicy": true, "iam:somefutureaction": true}
	if len(names) != len(want) {
		t.Fatalf("writeActionNames() = %v, want 2 entries", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("writeActionNames() contains unexpected entry %q", n)
		}
	}
}

func TestPolicyIsPrivileged(t *testing.T) {
	writeActions := writeActionNames([]Action{{Action: "CreatePolicy", Label: "Write"}})

	privileged := &Policy{Statements: []Statement{
		{Effect: "allow", Actions: []string{"iam:*"}},
	}}
	if !policyIsPrivileged(privileged, writeActions) {
		t.Error("policyIsPrivileged() = false for an allow iam:* statement, want true")
	}

	denyOnly := &Policy{Statements: []Statement{
		{Effect: "deny", Actions: []string{"iam:*"}},
	}}
	if policyIsPrivileged(denyOnly, writeActions) {
		t.Error("policyIsPrivileged() = true for a deny-only policy, want false")
	}

	readOnly := &Policy{Statements: []Statement{
		{Effect: "allow", Actions: []string{"vserver:List*"}},
	}}
	if policyIsPrivileged(readOnly, writeActions) {
		t.Error("policyIsPrivileged() = true for an unrelated allow statement, want false")
	}
}

func TestIsClassifiedCallerType(t *testing.T) {
	classified := []string{callerTypeIAMUser, callerTypeUserSA, callerTypeServiceSA}
	for _, ct := range classified {
		if !isClassifiedCallerType(ct) {
			t.Errorf("isClassifiedCallerType(%q) = false, want true", ct)
		}
	}
	unclassified := []string{callerTypeRoot, "", "something-else"}
	for _, ct := range unclassified {
		if isClassifiedCallerType(ct) {
			t.Errorf("isClassifiedCallerType(%q) = true, want false", ct)
		}
	}
}
