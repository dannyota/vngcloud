package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestMatchesWriteAction(t *testing.T) {
	writeActions := writeActionNames([]Action{
		{Action: "CreatePolicy", Label: "Write"},
		{Action: "AttachPolicyToIamUser", Label: "Write"},
		{Action: "ListPolicies", Label: "List"},
		{Action: "GetPolicy", Label: "Read"},
		{Action: "TagPolicy", Label: "Tagging"},
	})

	privileged := []string{"*", "*:*", "iam:*", "IAM:attach*", "iam:CreatePolicy"}
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

// TestMatchesWriteActionOddPatternsPrivileged checks that an action pattern
// with whitespace, an internal space, a stray control character, a period,
// or a literal backslash is treated as privileged rather than parsed as a
// wildcard: none of these ever match, since they are not built only from
// letters, digits, "*", and one optional ":", so a policy using one must
// never be waved through as unprivileged just because path.Match happens to
// find no match for it.
func TestMatchesWriteActionOddPatternsPrivileged(t *testing.T) {
	writeActions := writeActionNames([]Action{{Action: "CreatePolicy", Label: "Write"}})
	// zeroWidthSpace is built at runtime, rather than written as a literal
	// character or a "​" escape, so the source file holds no invisible
	// Unicode format character for a linter or a reviewer to trip over.
	zeroWidthSpace := string(rune(0x200b))
	odd := []string{
		" iam:*",
		"iam:* ",
		"iam :*",
		"iam:create*\t",
		"iam:" + zeroWidthSpace + "*",
		"iam:Create.*",
		`iam:\*`,
	}
	for _, pattern := range odd {
		if !matchesWriteAction(pattern, writeActions) {
			t.Errorf("matchesWriteAction(%q) = false, want true (odd pattern must be privileged)", pattern)
		}
	}

	ordinary := []string{"vserver:ListServers", "vserver:List*"}
	for _, pattern := range ordinary {
		if matchesWriteAction(pattern, writeActions) {
			t.Errorf("matchesWriteAction(%q) = true, want false (an ordinary, unrelated pattern)", pattern)
		}
	}
}

// TestMatchesWriteActionAlwaysPrivilegedIgnoresActionList checks that "*",
// "*:*", and "iam:*" match even against an empty write action list: these
// three patterns grant every IAM write action by their own shape, so they
// must never depend on the account's fetched action list to be recognized.
func TestMatchesWriteActionAlwaysPrivilegedIgnoresActionList(t *testing.T) {
	for _, pattern := range []string{"*", "*:*", "iam:*", "IAM:*"} {
		if !matchesWriteAction(pattern, nil) {
			t.Errorf("matchesWriteAction(%q, nil) = false, want true", pattern)
		}
	}
	// A narrower pattern still depends on the action list and must not
	// match when it is empty.
	if matchesWriteAction("iam:CreatePolicy", nil) {
		t.Error("matchesWriteAction(\"iam:CreatePolicy\", nil) = true, want false")
	}
}

// TestWriteActionNamesStripsExistingIAMPrefix checks that an action value
// the server already prefixes with "iam:" is normalized to a single prefix,
// so it still matches a policy pattern such as "iam:createpolicy" instead
// of silently becoming "iam:iam:createpolicy".
func TestWriteActionNamesStripsExistingIAMPrefix(t *testing.T) {
	names := writeActionNames([]Action{
		{Action: "iam:CreatePolicy", Label: "Write"},
		{Action: "IAM:AttachPolicyToIamUser", Label: "Write"},
	})
	want := map[string]bool{"iam:createpolicy": true, "iam:attachpolicytoiamuser": true}
	if len(names) != len(want) {
		t.Fatalf("writeActionNames() = %v, want %v", names, want)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("writeActionNames() contains unexpected entry %q", n)
		}
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

// TestPolicyIsPrivilegedNoStatements checks that a policy with no statements
// counts as privileged: a document with nothing to read is refused rather
// than treated as harmless.
func TestPolicyIsPrivilegedNoStatements(t *testing.T) {
	writeActions := writeActionNames([]Action{{Action: "CreatePolicy", Label: "Write"}})
	empty := &Policy{}
	if !policyIsPrivileged(empty, writeActions) {
		t.Error("policyIsPrivileged() = false for a policy with no statements, want true")
	}
}

// TestPolicyIsPrivilegedNonDenyEffectCountsAsAllow checks that a statement
// whose effect is neither "allow" nor "deny" is still treated as a grant:
// only an explicit "deny" is excluded.
func TestPolicyIsPrivilegedNonDenyEffectCountsAsAllow(t *testing.T) {
	writeActions := writeActionNames([]Action{{Action: "CreatePolicy", Label: "Write"}})
	garbled := &Policy{Statements: []Statement{
		{Effect: "permit", Actions: []string{"iam:CreatePolicy"}},
	}}
	if !policyIsPrivileged(garbled, writeActions) {
		t.Error("policyIsPrivileged() = false for a non-deny, non-allow effect, want true")
	}
}

// TestPolicyIsPrivilegedAllowNextToDeny checks that a policy with one deny
// statement and one privileged allow statement is still privileged: the deny
// only excuses itself, never a separate allow the loop has yet to reach.
func TestPolicyIsPrivilegedAllowNextToDeny(t *testing.T) {
	writeActions := writeActionNames([]Action{{Action: "CreatePolicy", Label: "Write"}})
	mixed := &Policy{Statements: []Statement{
		{Effect: "deny", Actions: []string{"iam:*"}},
		{Effect: "allow", Actions: []string{"iam:CreatePolicy"}},
	}}
	if !policyIsPrivileged(mixed, writeActions) {
		t.Error("policyIsPrivileged() = false for an allow next to a deny, want true")
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

// TestGuardRefusesEmptyActionList checks that an account whose action list
// names no write action (empty, or every action labeled List, Read, or
// Tagging) refuses every guarded write instead of treating it as evidence
// that nothing is privileged.
func TestGuardRefusesEmptyActionList(t *testing.T) {
	for _, actions := range [][]Action{
		{},
		{{Action: "ListPolicies", Label: "List"}, {Action: "GetPolicy", Label: "Read"}},
	} {
		g := guardFixture{caller: userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser}, actions: actions}
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("no write request expected")
			})
		})
		if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err == nil {
			t.Fatalf("actions %v: DeleteServiceAccount() error = nil, want a fail-closed refusal", actions)
		}
	}
}

// TestGuardDoesNotCacheEmptyActionList checks that an empty action list is
// never cached as if it were a real, populated result: a later call that
// sees a real write action must succeed, proving the guard retried the read
// instead of being stuck refusing (or worse, stuck open) forever.
func TestGuardDoesNotCacheEmptyActionList(t *testing.T) {
	var calls int
	g := guardFixture{
		caller: userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actionsHandler: func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				_ = json.NewEncoder(w).Encode([]Action{})
				return
			}
			_ = json.NewEncoder(w).Encode([]Action{{Action: "CreatePolicy", Label: "Write"}})
		},
	}
	var deletes int
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
			deletes++
			w.WriteHeader(http.StatusNoContent)
		})
	})

	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err == nil {
		t.Fatal("first DeleteServiceAccount() error = nil, want a fail-closed refusal on an empty action list")
	}
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-2"}); err != nil {
		t.Fatalf("second DeleteServiceAccount() error = %v, want success once the action list is populated", err)
	}
	if deletes != 1 {
		t.Fatalf("deletes = %d, want 1", deletes)
	}
	if calls != 2 {
		t.Fatalf("actions was fetched %d times, want 2 (the empty result must not be cached)", calls)
	}
}

// TestGuardRefusesAttachmentListMissingData checks that a service-account
// attachment list response with no "data" key refuses the write, rather
// than treating a nil slice as "no attachments".
func TestGuardRefusesAttachmentListMissingData(t *testing.T) {
	g := guardFixture{
		caller:  userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions: []Action{{Action: "CreatePolicy", Label: "Write"}},
		attachmentsHandler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"totalItems":0,"totalPages":1}`))
		},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err == nil {
		t.Fatal("DeleteServiceAccount() error = nil, want a refusal for a missing data key")
	}
}

// TestGuardRefusesAttachmentListMissingTotalItems checks that an attachment
// list response with no totalItems key refuses the write, rather than
// trusting whatever data it did return as complete.
func TestGuardRefusesAttachmentListMissingTotalItems(t *testing.T) {
	g := guardFixture{
		caller:  userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions: []Action{{Action: "CreatePolicy", Label: "Write"}},
		attachmentsHandler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[]}`))
		},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err == nil {
		t.Fatal("DeleteServiceAccount() error = nil, want a refusal for a missing totalItems key")
	}
}

// TestGuardRefusesAttachmentListPartial checks that an attachment list
// response whose data is shorter than its own totalItems refuses the write
// instead of judging the service account by an incomplete page.
func TestGuardRefusesAttachmentListPartial(t *testing.T) {
	g := guardFixture{
		caller:  userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions: []Action{{Action: "CreatePolicy", Label: "Write"}},
		attachmentsHandler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"id":"policy-1"}],"totalItems":2,"totalPages":1}`))
		},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err == nil {
		t.Fatal("DeleteServiceAccount() error = nil, want a refusal when data is shorter than totalItems")
	}
}

// TestGuardTreatsUnknownActionLabelAsPrivileged checks, end to end through a
// guarded write, that an action whose label the guard has never seen before
// still counts as a write action: the account's action list named
// "MysteryAction" with a label of "Something", and a policy granting it is
// refused as privileged.
func TestGuardTreatsUnknownActionLabelAsPrivileged(t *testing.T) {
	g := guardFixture{
		caller:            userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions:           []Action{{Action: "MysteryAction", Label: "Something"}},
		attachedPolicyIDs: []string{"policy-1"},
		policies: map[string]Policy{
			"policy-1": {ID: "policy-1", Manager: "user", Statements: []Statement{
				{Effect: "allow", Actions: []string{"iam:MysteryAction"}},
			}},
		},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DeleteServiceAccount() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestGuardServiceAccountCallerRefusesEveryServiceAccountTarget checks that
// a user-sa or service-sa caller refuses every service-account-targeted
// write, not only a write against its own ID: GetCallerIdentity's UserID for
// a service-account caller is not confirmed to use the same ID form as a
// target's ID or ClientID, so the guard cannot safely tell them apart.
func TestGuardServiceAccountCallerRefusesEveryServiceAccountTarget(t *testing.T) {
	for _, callerType := range []string{callerTypeUserSA, callerTypeServiceSA} {
		g := guardFixture{caller: userInfoResponse{UserID: "caller-sa", UserType: callerType}}
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-other", func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("no write request expected")
			})
		})
		_, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-other"})
		if !errors.Is(err, ErrSelfChange) {
			t.Fatalf("callerType %q: DeleteServiceAccount() err = %v, want ErrSelfChange", callerType, err)
		}
	}
}
