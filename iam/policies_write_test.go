package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func readOnlyStatements() []Statement {
	return []Statement{{Effect: "allow", Actions: []string{"vserver:ListServers"}, Resources: []string{"*"}}}
}

func privilegedStatements() []Statement {
	return []Statement{{Effect: "allow", Actions: []string{"iam:*"}, Resources: []string{"*"}}}
}

// TestCheckPolicyStatementsRefusals checks every documented statement shape
// refusal, each with no request sent: no statements, a bad effect, empty
// actions or resources, and an empty string in either.
func TestCheckPolicyStatementsRefusals(t *testing.T) {
	cases := []struct {
		name       string
		statements []Statement
	}{
		{"no statements", nil},
		{"bad effect", []Statement{{Effect: "permit", Actions: []string{"a"}, Resources: []string{"*"}}}},
		{"empty actions", []Statement{{Effect: "allow", Actions: []string{}, Resources: []string{"*"}}}},
		{"empty resources", []Statement{{Effect: "allow", Actions: []string{"a"}, Resources: []string{}}}},
		{"empty string action", []Statement{{Effect: "allow", Actions: []string{""}, Resources: []string{"*"}}}},
		{"empty string resource", []Statement{{Effect: "allow", Actions: []string{"a"}, Resources: []string{""}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "p", Statements: tc.statements})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("CreatePolicy() err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestUpdatePolicyStatementsShapeChecked checks that UpdatePolicy runs the
// same shape check on a non-nil Statements field, before any request.
func TestUpdatePolicyStatementsShapeChecked(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request expected")
	}))
	bad := []Statement{{Effect: "allow"}}
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Statements: &bad})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePolicyRequestBodyAndResponse(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			var body createPolicyBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "app-read" || body.Description != "read only" {
				t.Fatalf("unexpected body: %+v", body)
			}
			if len(body.Statements) != 1 || body.Statements[0].Effect != "allow" {
				t.Fatalf("unexpected statements: %+v", body.Statements)
			}
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/iam/create_policy.json")
		})
		mux.HandleFunc("GET /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/iam/get_policy_customer.json")
		})
	})

	out, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app-read", Description: "read only", Statements: readOnlyStatements()})
	if err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	if out.Policy.ID != "policy-1" {
		t.Fatalf("unexpected policy: %+v", out.Policy)
	}
}

// TestCreatePolicyGuardRefusesPrivilegedStatements checks that privileged
// statements refuse the create with no request sent.
func TestCreatePolicyGuardRefusesPrivilegedStatements(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: privilegedStatements()})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("CreatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestCreatePolicyRefusesUnknownCallerType checks that CreatePolicy, which
// targets no principal, still refuses for an unclassified caller type, per
// the design's Terms section.
func TestCreatePolicyRefusesUnknownCallerType(t *testing.T) {
	g := guardFixture{caller: userInfoResponse{UserID: "user-1", UserType: callerTypeRoot}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("CreatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

func TestCreatePolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGuardFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()}); err == nil {
			t.Fatalf("status %d: CreatePolicy() error = nil, want an error", status)
		}
	}
}

func TestCreatePolicyNoRetryOn502(t *testing.T) {
	g := unprivilegedGuardFixture()
	var calls int32
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()})
	if err == nil {
		t.Fatal("CreatePolicy() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "list-policies") {
		t.Fatalf("error does not name list-policies: %v", err)
	}
}

func TestCreatePolicyMissingID(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		})
	})
	if _, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()}); err == nil {
		t.Fatal("CreatePolicy() error = nil, want an error for a response with no id")
	}
}

// TestCreatePolicyReadFailureKeepsOutput checks that a failed confirm read
// after a landed create wraps ErrNotSettled and keeps a non-nil Output
// holding the create response's own id, mirroring
// TestCreateServiceAccountReadFailureKeepsOutput.
func TestCreatePolicyReadFailureKeepsOutput(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"policy-1"}`))
		})
		mux.HandleFunc("GET /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	})
	out, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("CreatePolicy() err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want the create's id kept despite the failed read-back")
	}
	if out.Policy.ID != "policy-1" {
		t.Fatalf("Output.Policy.ID = %q, want policy-1", out.Policy.ID)
	}
}

func TestCreatePolicyDuplicateNameNotWrapped(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"name already exists"}`))
		})
	})
	_, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *vngcloud.APIError", err)
	}
	if strings.Contains(err.Error(), "list-policies") {
		t.Fatalf("a 4xx error should not get the ambiguous-create hint: %v", err)
	}
}

// TestCreatePolicyNoResendAfter401 checks that Once keeps the create POST
// from being resent after a 401, which would otherwise create a second
// policy. Unlike CreateServiceAccount, CreatePolicy has a guard that reads
// first, so only the create POST itself, not the guard's own reads, is made
// to fail: the fixture's userinfo and actions handlers still succeed.
func TestCreatePolicyNoResendAfter401(t *testing.T) {
	var calls atomic.Int32
	g := unprivilegedGuardFixture()
	server := httptest.NewServer(g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		})
	}))
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &sequentialTokenSource{}})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()}); err == nil {
		t.Fatal("CreatePolicy() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no resend after 401)", got)
	}
}

// TestCreatePolicyNoFollowRedirect307 checks that Once keeps the create POST
// from following a 307, which would otherwise create a second policy.
func TestCreatePolicyNoFollowRedirect307(t *testing.T) {
	var calls atomic.Int32
	g := unprivilegedGuardFixture()
	server := httptest.NewServer(g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Location", r.URL.String())
			w.WriteHeader(http.StatusTemporaryRedirect)
		})
	}))
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client()})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "app", Statements: readOnlyStatements()}); err == nil {
		t.Fatal("CreatePolicy() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no redirect followed)", got)
	}
}

// unprivilegedPolicyFixture builds a guardFixture whose policy-1 is a
// customer policy with unprivileged statements and no attachments, so
// UpdatePolicy and DeletePolicy guard checks pass through to the write.
func unprivilegedPolicyFixture() guardFixture {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Name: "app-read", Manager: "user", Statements: readOnlyStatements()},
	}
	return g
}

func TestUpdatePolicyFillsNilFieldsFromRead(t *testing.T) {
	g := unprivilegedPolicyFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			var body updatePolicyBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "app-read" {
				t.Fatalf("Name = %q, want it filled from the read", body.Name)
			}
			if body.Description != "new desc" {
				t.Fatalf("Description = %q, want new desc", body.Description)
			}
			if len(body.Statements) != 1 || body.Statements[0].Actions[0] != "vserver:ListServers" {
				t.Fatalf("Statements = %+v, want them filled from the read", body.Statements)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})

	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{
		PolicyID:    "policy-1",
		Description: vngcloud.Ptr("new desc"),
	}); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
}

func TestUpdatePolicyStatementsRequestBody(t *testing.T) {
	g := unprivilegedPolicyFixture()
	newStatements := []Statement{{Effect: "allow", Actions: []string{"vserver:GetServer"}, Resources: []string{"*"}}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			var body updatePolicyBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Statements) != 1 || body.Statements[0].Actions[0] != "vserver:GetServer" {
				t.Fatalf("Statements = %+v, want the caller's new value", body.Statements)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})

	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{
		PolicyID:   "policy-1",
		Statements: &newStatements,
	}); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
}

func TestUpdatePolicyManagedRefused(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "VNG CLOUD", Statements: readOnlyStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrManagedPolicy) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrManagedPolicy", err)
	}
}

func TestUpdatePolicyCurrentStatementsPrivilegedRefused(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestUpdatePolicyPrivilegedAndSelfReportsSelfChange checks that a policy
// whose own current statements are privileged, and which is also attached to
// the caller directly, reports ErrSelfChange rather than ErrPrivilegedChange:
// guardUpdatePolicy checks a policy's attachments for self before it reports
// the current statements as privileged, so self wins even though the
// privileged-statements refusal would otherwise fire first.
func TestUpdatePolicyPrivilegedAndSelfReportsSelfChange(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: privilegedStatements()},
	}
	g.policyUserIDs = []string{"user-1"}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

func TestUpdatePolicyProposedStatementsPrivilegedRefused(t *testing.T) {
	g := unprivilegedPolicyFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	privileged := privilegedStatements()
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Statements: &privileged})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestUpdatePolicyAttachedToProtectedGroupItself checks that a policy
// attached to a group that itself holds a privileged policy refuses the
// update.
func TestUpdatePolicyAttachedToProtectedGroupItself(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groups = map[string]Group{
		"group-z": {ID: "group-z", PolicyIDs: []string{"policy-priv"}},
	}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestUpdatePolicyRefusesGroupAttachedInIdpMode checks that
// policyAttachedToProtected refuses when a policy is attached to a group in
// idp mode, the same as every other guard that reads a group.
func TestUpdatePolicyRefusesGroupAttachedInIdpMode(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"group-z","mode":"idp","iamUsers":[],"policies":[]}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a refusal for an idp-mode attached group")
	}
}

// TestUpdatePolicyAttachedToGroupProtectedOnlyByMember checks that a group
// with no privileged policy of its own, but a member who holds one
// directly, still refuses the update: "a group protected only through a
// member."
func TestUpdatePolicyAttachedToGroupProtectedOnlyByMember(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groups = map[string]Group{
		"group-z": {ID: "group-z", UserIDs: []string{"user-m"}},
	}
	g.userPolicyIDs = map[string][]string{"user-m": {"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestUpdatePolicyAttachedToUserProtectedOnlyByGroup checks that an IAM user
// with no privileged policy directly attached, but who belongs to a group
// that has one, still refuses the update: "a user protected only through a
// group."
func TestUpdatePolicyAttachedToUserProtectedOnlyByGroup(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-x"}
	g.userGroups = map[string][]Group{
		"user-x": {{ID: "group-y", PolicyIDs: []string{"policy-priv"}}},
	}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestUpdatePolicyAttachedToCallerRefused checks that a policy attached to
// the caller's own IAM user refuses as a self-change, not a privileged one:
// the design says a change to the caller's own rights is ErrSelfChange, and
// it wins even though the caller also counts as a protected principal.
func TestUpdatePolicyAttachedToCallerRefused(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-1"}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestUpdatePolicyMultiAttachmentSelfWinsAcrossAttachments checks that self
// wins even when a different, privileged group is checked before the
// caller's own direct attachment: policyAttachedToProtected combines every
// attachment's own result rather than returning as soon as the first
// non-notProtected one is found, so a later self-match still upgrades the
// result.
func TestUpdatePolicyMultiAttachmentSelfWinsAcrossAttachments(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groups = map[string]Group{"group-z": {ID: "group-z", PolicyIDs: []string{"policy-priv"}}}
	g.policyUserIDs = []string{"user-1"}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestUpdatePolicyServiceAccountCallerRefusesServiceAccountAttachment checks
// that a service-account caller updating a policy attached to any service
// account at all refuses as a self-change, mirroring guardServiceAccountWrite
// (see the design's open question on the caller identity form): the caller's
// UserID is not confirmed to use the same form as a target service account's
// ID, so the guard cannot rule out that the attached account is the caller
// itself, and refuses without even checking whether it holds a privileged
// policy.
func TestUpdatePolicyServiceAccountCallerRefusesServiceAccountAttachment(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.caller = userInfoResponse{UserID: "sa-caller", UserType: callerTypeUserSA}
	g.policyServiceAccountIDs = []string{"sa-1"}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestUpdatePolicyServiceAccountCallerSelfWinsAfterGroupPrivilege checks that
// a service-account caller's blanket self-refusal wins even when a
// privileged group attachment is found first: policyAttachedToProtected must
// not stop at the first non-notProtected result before it ever reaches the
// service-account attachments.
func TestUpdatePolicyServiceAccountCallerSelfWinsAfterGroupPrivilege(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.caller = userInfoResponse{UserID: "sa-caller", UserType: callerTypeUserSA}
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groups = map[string]Group{"group-z": {ID: "group-z", PolicyIDs: []string{"policy-priv"}}}
	g.policyServiceAccountIDs = []string{"sa-1"}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestUpdatePolicyAttachedToProtectedServiceAccount checks that a policy
// attached to a service account holding a privileged policy refuses the
// update, reusing serviceAccountIsProtected through policyAttachedToProtected.
func TestUpdatePolicyAttachedToProtectedServiceAccount(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyServiceAccountIDs = []string{"sa-1"}
	g.attachedPolicyIDs = []string{"policy-priv"}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestUpdatePolicyAllowsUnprivilegedAttachments checks that a policy
// attached to an unprivileged group, user, and service account proceeds to
// send the PUT.
func TestUpdatePolicyAllowsUnprivilegedAttachments(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groups = map[string]Group{"group-z": {ID: "group-z", UserIDs: []string{"user-m"}}}
	g.policyUserIDs = []string{"user-x"}
	g.policyServiceAccountIDs = []string{"sa-1"}
	var put atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			put.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
	if !put.Load() {
		t.Fatal("PUT request was never sent")
	}
}

func TestUpdatePolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedPolicyFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
			t.Fatalf("status %d: UpdatePolicy() error = nil, want an error", status)
		}
	}
}

// TestUpdatePolicyReadFailureKeepsOutput checks that a failed confirm read
// after a landed PUT wraps ErrNotSettled and keeps a non-nil Output holding
// the target policy's own id, mirroring TestCreatePolicyReadFailureKeepsOutput.
// The fixture's GET handler distinguishes the guard's own pre-write read,
// which must succeed for the PUT to be sent at all, from the confirm read
// after it, which is made to fail.
func TestUpdatePolicyReadFailureKeepsOutput(t *testing.T) {
	g := unprivilegedPolicyFixture()
	var policyGETs int
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			policyGETs++
			if policyGETs == 1 {
				_ = json.NewEncoder(w).Encode(g.policies["policy-1"])
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		})
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	out, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("UpdatePolicy() err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want the policy's id kept despite the failed confirm read")
	}
	if out.Policy.ID != "policy-1" {
		t.Fatalf("Output.Policy.ID = %q, want policy-1", out.Policy.ID)
	}
}

// TestUpdatePolicyRefusesWhenGetPolicyFails checks that a guard-read failure
// on the policy itself refuses UpdatePolicy with no PUT sent.
func TestUpdatePolicyRefusesWhenGetPolicyFails(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a guard-read failure")
	}
}

// TestUpdatePolicyRefusesWhenPolicyAttachmentsFail checks that a failure of
// one of guardPolicyAttachments' own reads refuses UpdatePolicy with no PUT
// sent.
func TestUpdatePolicyRefusesWhenPolicyAttachmentsFail(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroupsHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a guard-read failure")
	}
}

// TestUpdatePolicyRefusesWhenGetGroupFails checks that a failure reading an
// attached group's own attachments refuses UpdatePolicy with no PUT sent.
func TestUpdatePolicyRefusesWhenGetGroupFails(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a guard-read failure")
	}
}

// TestUpdatePolicyRefusesWhenListUserGroupsFails checks that a failure
// reading an attached user's groups refuses UpdatePolicy with no PUT sent.
func TestUpdatePolicyRefusesWhenListUserGroupsFails(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-x"}
	g.userGroupsHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a guard-read failure")
	}
}

// TestGuardPolicyAttachmentsRefusesNullGroups checks that UpdatePolicy
// refuses when a policy's own groups attachment list comes back JSON null,
// rather than reading it as "no groups attached": guardPolicyAttachments,
// not the public, tolerant ListPolicyAttachments, backs this guard read.
func TestGuardPolicyAttachmentsRefusesNullGroups(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroupsHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`null`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a refusal for a null groups list")
	}
}

// TestGuardGroupAttachmentsRefusesNullPolicies checks that UpdatePolicy
// refuses when a group's own "policies" field comes back JSON null, rather
// than reading it as "the group holds no policies".
func TestGuardGroupAttachmentsRefusesNullPolicies(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"group-z","policies":null,"iamUsers":[]}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a refusal for a null policies field")
	}
}

// TestGuardUserGroupsRefusesNullPolicies checks that UpdatePolicy refuses
// when one of a user's groups has a null "policies" field, rather than
// reading it as "that group holds no policies".
func TestGuardUserGroupsRefusesNullPolicies(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-x"}
	g.userGroupsHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"group-y","policies":null}]`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a refusal for a null policies field")
	}
}

// TestGuardUserPolicySummariesPartialPageRefusesUpdate checks that
// UpdatePolicy refuses when a user's own direct policy attachment page holds
// fewer rows than its own reported totalItems, extending the same coverage
// TestGuardRefusesAttachmentListPartial gives the service-account endpoint to
// the IAM-user one guardUserPolicySummaries reads.
func TestGuardUserPolicySummariesPartialPageRefusesUpdate(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-x"}
	g.userPoliciesHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"policy-1"}],"totalItems":2,"totalPages":1}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a refusal for a partial page")
	}
}

// TestGuardUserPolicySummariesMissingTotalsRefusesUpdate is
// TestGuardUserPolicySummariesPartialPageRefusesUpdate for a response with no
// totalItems key at all.
func TestGuardUserPolicySummariesMissingTotalsRefusesUpdate(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-x"}
	g.userPoliciesHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want a refusal for a missing totalItems key")
	}
}

// TestUpdatePolicyRetriesOn502 checks that the PUT, idempotent regardless of
// any request field, is retried by the transport after a 502.
func TestUpdatePolicyRetriesOn502(t *testing.T) {
	g := unprivilegedPolicyFixture()
	var putCalls int32
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&putCalls, 1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")}); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
	if putCalls != 2 {
		t.Fatalf("server received %d PUT request(s), want 2 (retried once after 502)", putCalls)
	}
}

func TestDeletePolicy(t *testing.T) {
	g := unprivilegedPolicyFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"}); err != nil {
		t.Fatalf("DeletePolicy() error = %v", err)
	}
}

func TestDeletePolicyManagedRefused(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "VNG CLOUD", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"})
	if !errors.Is(err, ErrManagedPolicy) {
		t.Fatalf("DeletePolicy() err = %v, want ErrManagedPolicy", err)
	}
}

func TestDeletePolicyAttachedToGroupRefused(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroups = []GroupSummary{{ID: "group-z"}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("DeletePolicy() err = %v, want ErrInUse", err)
	}
}

func TestDeletePolicyAttachedToUserRefused(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyUserIDs = []string{"user-x"}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("DeletePolicy() err = %v, want ErrInUse", err)
	}
}

func TestDeletePolicyAttachedToServiceAccountRefused(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyServiceAccountIDs = []string{"sa-1"}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("DeletePolicy() err = %v, want ErrInUse", err)
	}
}

// TestDeletePolicyRefusesWhenGetPolicyFails checks that a guard-read failure
// on the policy itself refuses DeletePolicy with no DELETE sent.
func TestDeletePolicyRefusesWhenGetPolicyFails(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"}); err == nil {
		t.Fatal("DeletePolicy() error = nil, want a guard-read failure")
	}
}

// TestDeletePolicyRefusesWhenPolicyAttachmentsFail checks that a failure of
// one of guardPolicyAttachments' own reads refuses DeletePolicy with no
// DELETE sent.
func TestDeletePolicyRefusesWhenPolicyAttachmentsFail(t *testing.T) {
	g := unprivilegedPolicyFixture()
	g.policyGroupsHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"}); err == nil {
		t.Fatal("DeletePolicy() error = nil, want a guard-read failure")
	}
}

func TestDeletePolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedPolicyFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"}); err == nil {
			t.Fatalf("status %d: DeletePolicy() error = nil, want an error", status)
		}
	}
}

// TestPolicyGuardErrorTextNamesNoDetail checks that a policy guard refusal's
// error text never holds a statement, action, or policy name.
func TestPolicyGuardErrorTextNamesNoDetail(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Name: "TopSecretPolicyName", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("x")})
	if err == nil {
		t.Fatal("UpdatePolicy() error = nil, want ErrPrivilegedChange")
	}
	if strings.Contains(err.Error(), "TopSecretPolicyName") {
		t.Fatalf("error names the policy: %v", err)
	}
}
