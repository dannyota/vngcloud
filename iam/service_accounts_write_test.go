package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// guardFixture wires a fake accounts+policies API for the guard's own reads
// (userinfo, actions, a service account's attached policies, and each
// policy by ID), so a write test only has to add the handler for the write
// path itself.
type guardFixture struct {
	caller            userInfoResponse
	actions           []Action
	attachedPolicyIDs []string
	policies          map[string]Policy

	// userinfoCalls and actionsCalls, when non-nil, count how many times
	// each guard-cached read actually reached the fake server, so a test
	// can prove the Client-level cache in guard.go is used.
	userinfoCalls *atomic.Int32
	actionsCalls  *atomic.Int32

	// actionsHandler and attachmentsHandler, when non-nil, replace the
	// default actions and service-account-attachments handlers entirely,
	// for a test that needs a raw or dynamic response shape the actions and
	// attachedPolicyIDs fields cannot express, such as a missing key, a
	// mismatched totalItems, or a response that differs across calls.
	actionsHandler     http.HandlerFunc
	attachmentsHandler http.HandlerFunc

	// policyGroups, policyUserIDs, and policyServiceAccountIDs are the
	// attachments ListPolicyAttachments reports for the one policy a
	// CreatePolicy, UpdatePolicy, or DeletePolicy guard test targets (its
	// path parameter is accepted as any {id}, so these fixtures only ever
	// represent a single policy under test at a time, as attachedPolicyIDs
	// does for a single service account).
	policyGroups            []GroupSummary
	policyUserIDs           []string
	policyServiceAccountIDs []string

	// groups is keyed by group ID, for GetGroup.
	groups map[string]Group

	// userPolicyIDs and userGroups are each keyed by IAM user ID, for a
	// user's direct policy attachments (guardUserPolicySummaries) and the
	// groups it belongs to (ListUserGroups).
	userPolicyIDs map[string][]string
	userGroups    map[string][]Group

	// policyHandler, policyGroupsHandler, groupHandler, and
	// userGroupsHandler, when non-nil, replace the default GetPolicy,
	// policies/{id}/groups, GetGroup, and a user's groups handlers entirely,
	// for a test that needs a raw or failing response shape the policies,
	// policyGroups, groups, and userGroups fields cannot express, such as a
	// null attachment list or a read that fails outright.
	policyHandler       http.HandlerFunc
	policyGroupsHandler http.HandlerFunc
	groupHandler        http.HandlerFunc
	userGroupsHandler   http.HandlerFunc
	userPoliciesHandler http.HandlerFunc
}

func (g guardFixture) mux(t *testing.T, extra func(mux *http.ServeMux)) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /accounts-api/v1/auth/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if g.userinfoCalls != nil {
			g.userinfoCalls.Add(1)
		}
		if err := json.NewEncoder(w).Encode(g.caller); err != nil {
			t.Fatal(err)
		}
	})
	if g.actionsHandler != nil {
		mux.HandleFunc("GET /policies-api/v1/actions", g.actionsHandler)
	} else {
		mux.HandleFunc("GET /policies-api/v1/actions", func(w http.ResponseWriter, r *http.Request) {
			if g.actionsCalls != nil {
				g.actionsCalls.Add(1)
			}
			if err := json.NewEncoder(w).Encode(g.actions); err != nil {
				t.Fatal(err)
			}
		})
	}
	if g.attachmentsHandler != nil {
		mux.HandleFunc("GET /policies-api/v1/user-attachments/service-accounts/{id}/policies", g.attachmentsHandler)
	} else {
		mux.HandleFunc("GET /policies-api/v1/user-attachments/service-accounts/{id}/policies", func(w http.ResponseWriter, r *http.Request) {
			rows := make([]PolicySummary, 0, len(g.attachedPolicyIDs))
			for _, id := range g.attachedPolicyIDs {
				rows = append(rows, PolicySummary{ID: id})
			}
			resp := struct {
				Data       []PolicySummary `json:"data"`
				TotalItems int             `json:"totalItems"`
				TotalPages int             `json:"totalPages"`
			}{Data: rows, TotalItems: len(rows), TotalPages: 1}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Fatal(err)
			}
		})
	}
	mux.HandleFunc("GET /policies-api/v1/policies/{id}", func(w http.ResponseWriter, r *http.Request) {
		if g.policyHandler != nil {
			g.policyHandler(w, r)
			return
		}
		p, ok := g.policies[r.PathValue("id")]
		if !ok {
			t.Fatalf("unexpected policy id: %s", r.PathValue("id"))
		}
		if err := json.NewEncoder(w).Encode(p); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("GET /policies-api/v1/policies/{id}/groups", func(w http.ResponseWriter, r *http.Request) {
		if g.policyGroupsHandler != nil {
			g.policyGroupsHandler(w, r)
			return
		}
		groups := g.policyGroups
		if groups == nil {
			groups = []GroupSummary{}
		}
		if err := json.NewEncoder(w).Encode(groups); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("GET /policies-api/v1/policies/{id}/iam-users", func(w http.ResponseWriter, r *http.Request) {
		ids := g.policyUserIDs
		if ids == nil {
			ids = []string{}
		}
		if err := json.NewEncoder(w).Encode(ids); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("GET /policies-api/v1/policies/{id}/service-accounts", func(w http.ResponseWriter, r *http.Request) {
		ids := g.policyServiceAccountIDs
		if ids == nil {
			ids = []string{}
		}
		if err := json.NewEncoder(w).Encode(ids); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("GET /policies-api/v1/groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		if g.groupHandler != nil {
			g.groupHandler(w, r)
			return
		}
		group, ok := g.groups[r.PathValue("id")]
		if !ok {
			t.Fatalf("unexpected group id: %s", r.PathValue("id"))
		}
		// A fixture that leaves Mode unset means "an ordinary iam-mode
		// group", since almost every guard test fixture is about something
		// other than mode; a test of the mode check itself sets Group.Mode or
		// uses groupHandler directly.
		if group.Mode == "" {
			group.Mode = "iam"
		}
		// A fixture that leaves UserIDs or PolicyIDs unset means "this group
		// has none", encoded as a real empty array: encoding/json marshals a
		// nil slice as JSON null, which the guard's own fail-closed decode
		// (guardGetGroupAttachments) would otherwise mistake for a broken
		// response and refuse.
		if group.PolicyIDs == nil {
			group.PolicyIDs = []string{}
		}
		if group.UserIDs == nil {
			group.UserIDs = []string{}
		}
		if err := json.NewEncoder(w).Encode(group); err != nil {
			t.Fatal(err)
		}
	})
	if g.userPoliciesHandler != nil {
		mux.HandleFunc("GET /policies-api/v1/user-attachments/iam-users/{id}/policies", g.userPoliciesHandler)
	} else {
		mux.HandleFunc("GET /policies-api/v1/user-attachments/iam-users/{id}/policies", func(w http.ResponseWriter, r *http.Request) {
			ids := g.userPolicyIDs[r.PathValue("id")]
			rows := make([]PolicySummary, 0, len(ids))
			for _, id := range ids {
				rows = append(rows, PolicySummary{ID: id})
			}
			resp := struct {
				Data       []PolicySummary `json:"data"`
				TotalItems int             `json:"totalItems"`
				TotalPages int             `json:"totalPages"`
			}{Data: rows, TotalItems: len(rows), TotalPages: 1}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Fatal(err)
			}
		})
	}
	mux.HandleFunc("GET /policies-api/v1/user-attachments/iam-users/{id}/groups", func(w http.ResponseWriter, r *http.Request) {
		if g.userGroupsHandler != nil {
			g.userGroupsHandler(w, r)
			return
		}
		groups := g.userGroups[r.PathValue("id")]
		if groups == nil {
			groups = []Group{}
		}
		// As the GetGroup handler above does: a fixture Group with PolicyIDs
		// left unset means "holds no policies", encoded as a real empty
		// array rather than the null a nil slice would otherwise marshal to.
		normalized := make([]Group, len(groups))
		for i, group := range groups {
			if group.PolicyIDs == nil {
				group.PolicyIDs = []string{}
			}
			normalized[i] = group
		}
		if err := json.NewEncoder(w).Encode(normalized); err != nil {
			t.Fatal(err)
		}
	})
	extra(mux)
	return mux
}

func newGuardTestClient(t *testing.T, g guardFixture, extra func(mux *http.ServeMux)) *Client {
	t.Helper()
	server := httptest.NewServer(g.mux(t, extra))
	t.Cleanup(server.Close)
	tc := transport.New(transport.Config{HTTPClient: server.Client()})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{
		Region:    "hcm-3",
		Dashboard: server.URL + "/",
		IAM:       server.URL + "/",
	}, tc)
	return New(cfg)
}

func unprivilegedGuardFixture() guardFixture {
	return guardFixture{
		caller:  userInfoResponse{UserID: "user-1", UserType: callerTypeIAMUser},
		actions: []Action{{Action: "CreatePolicy", Label: "Write"}},
	}
}

// TestGuardRefusesSelfChange checks that a service-account caller acting on
// its own ID gets ErrSelfChange with no write request sent, and that it
// wins over ErrPrivilegedChange when both would apply.
func TestGuardRefusesSelfChange(t *testing.T) {
	g := guardFixture{
		caller:            userInfoResponse{UserID: "sa-1", UserType: callerTypeUserSA},
		actions:           []Action{{Action: "CreatePolicy", Label: "Write"}},
		attachedPolicyIDs: []string{"policy-1"},
		policies: map[string]Policy{
			"policy-1": {ID: "policy-1", Manager: "user", Statements: []Statement{
				{Effect: "allow", Actions: []string{"iam:*"}},
			}},
		},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})

	_, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("DeleteServiceAccount() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestGuardRefusesPrivilegedServiceAccount checks that a target service
// account holding a privileged policy refuses Update, Delete, and Reset,
// each with no write request sent.
func TestGuardRefusesPrivilegedServiceAccount(t *testing.T) {
	g := guardFixture{
		caller:            userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions:           []Action{{Action: "AttachPolicyToIamUser", Label: "Write"}},
		attachedPolicyIDs: []string{"policy-1"},
		policies: map[string]Policy{
			"policy-1": {ID: "policy-1", Manager: "VNG CLOUD", Statements: []Statement{
				{Effect: "allow", Actions: []string{"IAM:attach*"}},
			}},
		},
	}
	noWrite := func(mux *http.ServeMux) {
		mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
		mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	}

	c := newGuardTestClient(t, g, noWrite)
	if _, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: "sa-1", Description: vngcloud.Ptr("x")}); !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("UpdateServiceAccount() err = %v, want ErrPrivilegedChange", err)
	}

	c = newGuardTestClient(t, g, noWrite)
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DeleteServiceAccount() err = %v, want ErrPrivilegedChange", err)
	}

	c = newGuardTestClient(t, g, noWrite)
	if _, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"}); !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("ResetServiceAccountSecret() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestGuardAllowsUnprivilegedServiceAccount checks that a target with no
// privileged policy attached proceeds to send the write.
func TestGuardAllowsUnprivilegedServiceAccount(t *testing.T) {
	g := guardFixture{
		caller:            userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions:           []Action{{Action: "CreatePolicy", Label: "Write"}},
		attachedPolicyIDs: []string{"policy-1"},
		policies: map[string]Policy{
			"policy-1": {ID: "policy-1", Manager: "user", Statements: []Statement{
				{Effect: "allow", Actions: []string{"vserver:List*"}},
			}},
		},
	}
	var deleted atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})

	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("DeleteServiceAccount() error = %v", err)
	}
	if !deleted.Load() {
		t.Fatal("delete request was never sent")
	}
}

// TestGuardRefusesUnknownCallerType checks that a caller type the guard does
// not recognize, including root-user, refuses every guarded write with no
// request sent.
func TestGuardRefusesUnknownCallerType(t *testing.T) {
	for _, userType := range []string{callerTypeRoot, "", "something-else"} {
		g := guardFixture{caller: userInfoResponse{UserID: "user-1", UserType: userType}}
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("no write request expected")
			})
		})
		if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); !errors.Is(err, ErrPrivilegedChange) {
			t.Fatalf("userType %q: DeleteServiceAccount() err = %v, want ErrPrivilegedChange", userType, err)
		}
	}
}

// TestGuardErrorTextNamesNoDetail checks that a guard refusal's error text
// never holds a statement, action, or policy name.
func TestGuardErrorTextNamesNoDetail(t *testing.T) {
	g := guardFixture{
		caller:            userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser},
		actions:           []Action{{Action: "CreatePolicy", Label: "Write"}},
		attachedPolicyIDs: []string{"policy-secret-name"},
		policies: map[string]Policy{
			"policy-secret-name": {ID: "policy-secret-name", Name: "TopSecretPolicyName", Manager: "user", Statements: []Statement{
				{Effect: "allow", Actions: []string{"iam:*"}},
			}},
		},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
	if err == nil {
		t.Fatal("DeleteServiceAccount() error = nil, want ErrPrivilegedChange")
	}
	if strings.Contains(err.Error(), "TopSecretPolicyName") || strings.Contains(err.Error(), "policy-secret-name") {
		t.Fatalf("error names the policy: %v", err)
	}
}

// TestGuardCachesCallerAndActionsAcrossCalls checks that userinfo and
// actions are each fetched once per Client, even across two separate
// guarded writes.
func TestGuardCachesCallerAndActionsAcrossCalls(t *testing.T) {
	var userinfoCalls, actionsCalls atomic.Int32
	g := unprivilegedGuardFixture()
	g.userinfoCalls = &userinfoCalls
	g.actionsCalls = &actionsCalls
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})

	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("first DeleteServiceAccount() error = %v", err)
	}
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-2"}); err != nil {
		t.Fatalf("second DeleteServiceAccount() error = %v", err)
	}
	if got := userinfoCalls.Load(); got != 1 {
		t.Fatalf("userinfo was called %d times across two guarded writes, want 1", got)
	}
	if got := actionsCalls.Load(); got != 1 {
		t.Fatalf("actions was called %d times across two guarded writes, want 1", got)
	}
}

func TestCreateServiceAccountRequestBodyAndResponse(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
			return
		}
		if r.URL.Path != "/accounts-api/v1/service-accounts" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body createServiceAccountBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Name != "app" || body.Description != "an app" {
			t.Fatalf("unexpected body: %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/iam/create_service_account.json")
	}))

	out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app", Description: "an app"})
	if err != nil {
		t.Fatalf("CreateServiceAccount() error = %v", err)
	}
	if out.ServiceAccount.ID != "sa-1" {
		t.Fatalf("unexpected service account: %+v", out.ServiceAccount)
	}
	if out.ClientSecret.Reveal() != "<secret>" {
		t.Fatalf("Reveal() = %q, want the fixture value", out.ClientSecret.Reveal())
	}
}

// TestCreateServiceAccountFetchesAfterCreate checks that the create's own
// response need not carry the full ServiceAccount: a follow-up
// GetServiceAccount fills it in.
func TestCreateServiceAccountFetchesAfterCreate(t *testing.T) {
	var getCalled bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"sa-1","clientSecret":"<secret>"}`))
			return
		}
		getCalled = true
		testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
	}))

	out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	if err != nil {
		t.Fatalf("CreateServiceAccount() error = %v", err)
	}
	if !getCalled {
		t.Fatal("GetServiceAccount was never called after create")
	}
	if out.ServiceAccount.ClientID != "<client-id>" {
		t.Fatalf("unexpected service account: %+v", out.ServiceAccount)
	}
}

// TestCreateServiceAccountNoSecretReturnsErrNoSecret checks that a create
// response with no client secret returns ErrNoSecret rather than silently
// succeeding, while still keeping the created service account in Output:
// the account was really created, and the caller (or the CLI) needs its ID
// to reset the secret or clean up.
func TestCreateServiceAccountNoSecretReturnsErrNoSecret(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"sa-1"}`))
			return
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
	}))

	out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	if !errors.Is(err, ErrNoSecret) {
		t.Fatalf("CreateServiceAccount() err = %v, want ErrNoSecret", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want the created service account kept despite the missing secret")
	}
	if out.ServiceAccount.ID != "sa-1" {
		t.Fatalf("Output.ServiceAccount.ID = %q, want sa-1", out.ServiceAccount.ID)
	}
	if out.ClientSecret.Reveal() != "" {
		t.Fatal("ClientSecret is not empty for a create response with no secret")
	}
}

// TestCreateServiceAccountReadFailureKeepsOutput checks that a failed
// read-back after a successful create returns a non-nil Output carrying the
// create's own ID and secret, alongside ErrCreateUnconfirmed, instead of
// dropping them: the account and its secret are real even though the read
// that would fill in the rest of the ServiceAccount fields failed.
func TestCreateServiceAccountReadFailureKeepsOutput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"sa-1","clientSecret":"<secret>"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))

	out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	if !errors.Is(err, ErrCreateUnconfirmed) {
		t.Fatalf("CreateServiceAccount() err = %v, want ErrCreateUnconfirmed", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want the create's id and secret kept despite the failed read-back")
	}
	if out.ServiceAccount.ID != "sa-1" {
		t.Fatalf("Output.ServiceAccount.ID = %q, want sa-1", out.ServiceAccount.ID)
	}
	if out.ClientSecret.Reveal() != "<secret>" {
		t.Fatalf("Output.ClientSecret = %q, want the create's secret kept", out.ClientSecret.Reveal())
	}
}

func TestCreateServiceAccountMissingID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"clientSecret":"<secret>"}`))
	}))

	if _, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"}); err == nil {
		t.Fatal("CreateServiceAccount() error = nil, want an error for a response with no id")
	}
}

func TestCreateServiceAccountNoRetryOn502(t *testing.T) {
	var calls int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	if err == nil {
		t.Fatal("CreateServiceAccount() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "list-service-accounts") {
		t.Fatalf("error does not name list-service-accounts: %v", err)
	}
}

func TestCreateServiceAccountDuplicateNameNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"name already exists"}`))
	}))

	_, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *vngcloud.APIError", err)
	}
	if strings.Contains(err.Error(), "list-service-accounts") {
		t.Fatalf("a 4xx error should not get the ambiguous-create hint: %v", err)
	}
}

// TestCreateServiceAccountNeverCaptured checks that the Sensitive create
// request never reaches the configured response-capture hook, while an
// ordinary read on the same Client still does.
func TestCreateServiceAccountNeverCaptured(t *testing.T) {
	var captured []transport.Capture
	cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/iam/create_service_account.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
	}), func(c transport.Capture) {
		captured = append(captured, c)
	})
	c := New(cfg)

	if _, err := c.GetServiceAccount(context.Background(), &GetServiceAccountInput{ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("GetServiceAccount() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured = %d after GetServiceAccount, want 1", len(captured))
	}

	if _, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"}); err != nil {
		t.Fatalf("CreateServiceAccount() error = %v", err)
	}
	// The create itself must never be captured; the follow-up
	// GetServiceAccount read is not Sensitive and does add one capture.
	if len(captured) != 2 {
		t.Fatalf("captured = %d after CreateServiceAccount, want 2 (create itself never captured)", len(captured))
	}
}

// TestCreateServiceAccountSecretRedaction checks that ClientSecret never
// leaks the fixture secret through fmt verbs, slog, or json.Marshal.
func TestCreateServiceAccountSecretRedaction(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/iam/create_service_account.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
	}))

	out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	if err != nil {
		t.Fatalf("CreateServiceAccount() error = %v", err)
	}

	forms := map[string]string{
		"%v on Output":  fmt.Sprintf("%v", out),
		"%+v on Output": fmt.Sprintf("%+v", out),
		"%#v on Output": fmt.Sprintf("%#v", out),
		"%s on Secret":  fmt.Sprintf("%s", out.ClientSecret),
	}
	for name, got := range forms {
		if strings.Contains(got, "<secret>") {
			t.Fatalf("%s = %q, holds the fixture secret", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s = %q, want it to contain [redacted]", name, got)
		}
	}

	data, err := json.Marshal(out) //nolint:gosec // G117: ClientSecret is a vngcloud.Secret; its MarshalJSON redacts it, which this test itself verifies below
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "<secret>") {
		t.Fatalf("json.Marshal() = %s, holds the fixture secret", data)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("created service account", "secret", out.ClientSecret)
	if strings.Contains(buf.String(), "<secret>") {
		t.Fatalf("slog output = %q, holds the fixture secret", buf.String())
	}
}

func TestUpdateServiceAccountRequestBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts-api/v1/auth/userinfo":
			_ = json.NewEncoder(w).Encode(userInfoResponse{UserID: "caller-1", UserType: callerTypeIAMUser})
		case "/policies-api/v1/actions":
			// A real IAM account always has write actions; an empty list is
			// covered separately by the fail-closed guard tests in
			// guard_test.go, not this request-body test.
			_ = json.NewEncoder(w).Encode([]Action{{Action: "CreatePolicy", Label: "Write"}})
		case "/policies-api/v1/user-attachments/service-accounts/sa-1/policies":
			_, _ = w.Write([]byte(`{"data":[],"totalItems":0,"totalPages":1}`))
		case "/accounts-api/v1/service-accounts/sa-1":
			if r.Method == http.MethodPatch {
				var body updateServiceAccountBody
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Description == nil || *body.Description != "new desc" {
					t.Fatalf("unexpected body: %+v", body)
				}
				if body.AccessTokenLifeSpan != nil {
					t.Fatalf("AccessTokenLifeSpan sent when nil: %+v", body)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))

	out, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{
		ServiceAccountID: "sa-1",
		Description:      vngcloud.Ptr("new desc"),
	})
	if err != nil {
		t.Fatalf("UpdateServiceAccount() error = %v", err)
	}
	if out.ServiceAccount.ID != "sa-1" {
		t.Fatalf("unexpected service account: %+v", out.ServiceAccount)
	}
}

// TestUpdateServiceAccountAccessTokenLifeSpanRequestBody checks that setting
// only AccessTokenLifeSpan sends it alone, with Description left out of the
// body entirely.
func TestUpdateServiceAccountAccessTokenLifeSpanRequestBody(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			var body updateServiceAccountBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Description != nil {
				t.Fatalf("Description sent when nil: %+v", body)
			}
			if body.AccessTokenLifeSpan == nil || *body.AccessTokenLifeSpan != 3600 {
				t.Fatalf("unexpected body: %+v", body)
			}
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
		})
	})

	if _, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{
		ServiceAccountID:    "sa-1",
		AccessTokenLifeSpan: vngcloud.Ptr(3600),
	}); err != nil {
		t.Fatalf("UpdateServiceAccount() error = %v", err)
	}
}

// TestUpdateServiceAccountRetriesOn502 checks that the PATCH, marked
// Idempotent because it sends the update's full intended state, is retried
// by the transport after a 502, unlike the POST creates and reset.
func TestUpdateServiceAccountRetriesOn502(t *testing.T) {
	var patchCalls int32
	handler := unprivilegedGuardFixture().mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&patchCalls, 1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("GET /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: "sa-1", Description: vngcloud.Ptr("x")}); err != nil {
		t.Fatalf("UpdateServiceAccount() error = %v", err)
	}
	if patchCalls != 2 {
		t.Fatalf("server received %d PATCH request(s), want 2 (retried once after 502)", patchCalls)
	}
}

func TestDeleteServiceAccount(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("DeleteServiceAccount() error = %v", err)
	}
}

func TestDeleteServiceAccountNotFound(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	})
	_, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("DeleteServiceAccount() err = %v, want NotFound", err)
	}
}

func TestResetServiceAccountSecretRequestAndResponse(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/iam/reset_service_account_secret.json")
		})
	})
	out, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"})
	if err != nil {
		t.Fatalf("ResetServiceAccountSecret() error = %v", err)
	}
	if out.ClientSecret.Reveal() != "<secret>" {
		t.Fatalf("Reveal() = %q, want the fixture value", out.ClientSecret.Reveal())
	}
}

// TestResetServiceAccountSecretNoSecretReturnsErrNoSecret checks that a 200
// response with no clientSecret returns ErrNoSecret, with a message that
// says the secret was probably already rotated and to reset again.
func TestResetServiceAccountSecretNoSecretReturnsErrNoSecret(t *testing.T) {
	g := unprivilegedGuardFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		})
	})
	_, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrNoSecret) {
		t.Fatalf("ResetServiceAccountSecret() err = %v, want ErrNoSecret", err)
	}
	if !strings.Contains(err.Error(), "rotated") {
		t.Fatalf("error = %v, want it to say the secret was probably rotated", err)
	}
	if !strings.Contains(err.Error(), "reset") {
		t.Fatalf("error = %v, want it to say to reset again", err)
	}
}

func TestResetServiceAccountSecretNoRetryOn502(t *testing.T) {
	var calls int32
	handler := unprivilegedGuardFixture().mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: 0})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"})
	if err == nil {
		t.Fatal("ResetServiceAccountSecret() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("error does not mention treating the secret as revoked: %v", err)
	}
}

// TestServiceAccountWriteStatuses checks every documented error status for a
// guarded write, once the guard itself allows it through.
func TestServiceAccountWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGuardFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"}); err == nil {
			t.Fatalf("status %d: DeleteServiceAccount() error = nil, want an error", status)
		}
	}
}

// TestCreateServiceAccountWriteStatuses checks every documented error status
// for the create call, which has no guard to pass first.
func TestCreateServiceAccountWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		if _, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"}); err == nil {
			t.Fatalf("status %d: CreateServiceAccount() error = nil, want an error", status)
		}
	}
}

// TestUpdateServiceAccountWriteStatuses checks every documented error status
// for the update call, once the guard itself allows it through.
func TestUpdateServiceAccountWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGuardFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: "sa-1", Description: vngcloud.Ptr("x")}); err == nil {
			t.Fatalf("status %d: UpdateServiceAccount() error = nil, want an error", status)
		}
	}
}

// TestResetServiceAccountSecretWriteStatuses checks every documented error
// status for the reset call, once the guard itself allows it through.
func TestResetServiceAccountSecretWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGuardFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"}); err == nil {
			t.Fatalf("status %d: ResetServiceAccountSecret() error = nil, want an error", status)
		}
	}
}

// sequentialTokenSource issues a fresh token on every call, for a test that
// must exercise the transport's real 401-recovery path (invalidate and
// refresh), which only runs when a TokenSource is configured.
type sequentialTokenSource struct {
	n atomic.Int64
}

func (s *sequentialTokenSource) Token(context.Context) (transport.Token, error) {
	n := s.n.Add(1)
	return transport.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *sequentialTokenSource) Invalidate(string) {}

// newOnceTestClient builds a Client whose transport has a real TokenSource,
// so a 401 response exercises the invalidate-and-refresh path that a Once
// request must skip, instead of the no-op path a nil TokenSource takes.
func newOnceTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &sequentialTokenSource{}})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	return New(cfg)
}

// TestCreateServiceAccountNoResendAfter401 checks that Once keeps the create
// POST from being resent after a 401: without Once, the transport retries
// once with a refreshed token, which would create a second service account.
func TestCreateServiceAccountNoResendAfter401(t *testing.T) {
	var calls atomic.Int32
	c := newOnceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	if _, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"}); err == nil {
		t.Fatal("CreateServiceAccount() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no resend after 401)", got)
	}
}

// TestCreateServiceAccountNoFollowRedirect307 checks that Once keeps the
// create POST from following a 307: net/http would otherwise resend the
// same request at the redirect's Location, creating a second account.
func TestCreateServiceAccountNoFollowRedirect307(t *testing.T) {
	var calls atomic.Int32
	c := newOnceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", r.URL.String())
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	if _, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"}); err == nil {
		t.Fatal("CreateServiceAccount() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no redirect followed)", got)
	}
}

// TestResetServiceAccountSecretNoResendAfter401 is
// TestCreateServiceAccountNoResendAfter401 for the reset call: a resend
// after a 401 would rotate the secret a second time, past the first
// rotation's value.
func TestResetServiceAccountSecretNoResendAfter401(t *testing.T) {
	var calls atomic.Int32
	g := unprivilegedGuardFixture()
	server := httptest.NewServer(g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		})
	}))
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &sequentialTokenSource{}})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"}); err == nil {
		t.Fatal("ResetServiceAccountSecret() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no resend after 401)", got)
	}
}

// TestResetServiceAccountSecretNoFollowRedirect307 is
// TestCreateServiceAccountNoFollowRedirect307 for the reset call.
func TestResetServiceAccountSecretNoFollowRedirect307(t *testing.T) {
	var calls atomic.Int32
	g := unprivilegedGuardFixture()
	server := httptest.NewServer(g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /accounts-api/v1/service-accounts/sa-1/reset-secret", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Location", r.URL.String())
			w.WriteHeader(http.StatusTemporaryRedirect)
		})
	}))
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client()})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: "sa-1"}); err == nil {
		t.Fatal("ResetServiceAccountSecret() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no redirect followed)", got)
	}
}
