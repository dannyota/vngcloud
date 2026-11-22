package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestServiceAccountCreateRetainsSecretWithoutIdentity(t *testing.T) {
	for _, id := range []string{"", "bad/id", "issued-secret"} {
		t.Run(id, func(t *testing.T) {
			var reads atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads.Add(1)
					_, _ = io.WriteString(w, `{"id":"sa-1"}`)
					return
				}
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "clientSecret": "issued-secret"})
			}))
			out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
			if out == nil || out.ClientSecret.Reveal() != "issued-secret" || out.ServiceAccount.ID != "" || !errors.Is(err, ErrCreateUnconfirmed) || reads.Load() != 0 {
				t.Fatalf("lost one-time secret or exposed cleanup ID: output=%v error=%v reads=%d", out, err, reads.Load())
			}
		})
	}
}

func TestServiceAccountCreateWithholdsDependentRead(t *testing.T) {
	for _, body := range []string{`{"id":"sa-1","description":"issued-secret"}`, `{"id":"sa-2"}`, `{"id":"sa-1","ID":"sa-2"}`} {
		t.Run(body, func(t *testing.T) {
			var captures atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(201)
					_, _ = io.WriteString(w, `{"id":"sa-1","clientSecret":"issued-secret"}`)
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), Capture: func(transport.Capture) { captures.Add(1) }}))
			out, err := New(cfg).CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
			raw, _ := json.Marshal(out) //nolint:gosec // G117: Secret.MarshalJSON redacts ClientSecret; this test checks the encoded output.
			if out == nil || out.ClientSecret.Reveal() != "issued-secret" || out.ServiceAccount.ID != "sa-1" || out.ServiceAccount.Description != "" || !errors.Is(err, ErrCreateUnconfirmed) || strings.Contains(string(raw), "issued-secret") || captures.Load() != 0 {
				t.Fatalf("unsafe dependent read: output=%s error=%v captures=%d", raw, err, captures.Load())
			}
		})
	}
}

func TestServiceAccountAcceptedUpdateRetainsPartial(t *testing.T) {
	c := newGuardTestClient(t, unprivilegedGuardFixture(), func(mux *http.ServeMux) {
		mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		mux.HandleFunc("GET /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
	})
	out, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: "sa-1", Description: vngcloud.Ptr("new")})
	if out == nil || out.ServiceAccount.ID != "sa-1" || !errors.Is(err, ErrNotSettled) {
		t.Fatalf("accepted update lost: output=%v error=%v", out, err)
	}
}

func TestSwappedPolicyBlocksMutation(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policyHandler = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"policy-2","manager":"user","statements":[{"effect":"deny","actions":["iam:CreatePolicy"]}]}`)
	}
	var writes atomic.Int32
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, _ *http.Request) { writes.Add(1); w.WriteHeader(204) })
	})
	_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"})
	if err == nil || writes.Load() != 0 {
		t.Fatalf("swapped policy authorized mutation: error=%v writes=%d", err, writes.Load())
	}
}

func TestSwappedGroupBlocksMutation(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.groupHandler = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"group-2","mode":"iam","policies":[],"iamUsers":[]}`)
	}
	var writes atomic.Int32
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, _ *http.Request) { writes.Add(1); w.WriteHeader(204) })
	})
	_, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"})
	if err == nil || writes.Load() != 0 {
		t.Fatalf("swapped group authorized mutation: error=%v writes=%d", err, writes.Load())
	}
}

func TestIAMPolicyPagingPreservesMetadata(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[],"pageNumber":2,"pageSize":5,"totalItems":10,"totalPages":2}`)
	}))
	out, err := c.ListPolicies(context.Background(), &ListPoliciesInput{Page: 2, Size: 5})
	raw, _ := json.Marshal(out)
	if err != nil || !strings.Contains(string(raw), `"PageNumber":2`) || !strings.Contains(string(raw), `"TotalItems":10`) {
		t.Fatalf("lost IAM metadata: output=%s error=%v", raw, err)
	}
}

func TestServiceAccountReadRejectsTokenReflection(t *testing.T) {
	var captures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":"sa-1","name":"initial-bearer"}`)
	}))
	defer server.Close()
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &guardOperandTokens{}, Capture: func(transport.Capture) { captures.Add(1) }}))
	out, err := New(cfg).GetServiceAccount(context.Background(), &GetServiceAccountInput{ServiceAccountID: "sa-1"})
	if out != nil || err == nil || strings.Contains(fmt.Sprint(err), "initial-bearer") || captures.Load() != 0 {
		t.Fatalf("token reflection: output=%v error=%v captures=%d", out, err, captures.Load())
	}
}

func TestServiceAccountConfirmationCannotExposeNewCredentialAsID(t *testing.T) {
	tokens := &guardOperandTokens{later: "sa-1"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			tokens.changed.Store(true)
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"sa-1","clientSecret":"issued-secret"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"sa-1"}`)
	}))
	defer server.Close()
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: tokens}))
	out, err := New(cfg).CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
	if out == nil || out.ClientSecret.Reveal() != "issued-secret" || out.ServiceAccount.ID != "" || !errors.Is(err, ErrCreateUnconfirmed) || strings.Contains(fmt.Sprint(err), "sa-1") {
		t.Fatalf("new token escaped through fallback: output=%v error=%v", out, err)
	}
}

func TestPolicyReplacementRequiresPreservedFields(t *testing.T) {
	for _, body := range []string{
		`{"id":"policy-1","manager":"user","name":"old","statements":[{"effect":"deny","actions":["iam:CreatePolicy"]}]}`,
		`{"id":"policy-1","manager":"user","name":null,"description":"d","statements":[{"effect":"deny","actions":["iam:CreatePolicy"]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			g := unprivilegedGuardFixture()
			g.policyHandler = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
			var writes atomic.Int32
			c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
				mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, _ *http.Request) { writes.Add(1); w.WriteHeader(204) })
			})
			_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1"})
			if err == nil || writes.Load() != 0 {
				t.Fatalf("unproven fields replaced: error=%v writes=%d", err, writes.Load())
			}
		})
	}
}

func TestGroupUpdateRequiresMatchingNamedSnapshot(t *testing.T) {
	for _, body := range []string{`{"id":"group-2","name":"old"}`, `{"id":"group-1"}`, `{"id":"group-1","name":null}`} {
		t.Run(body, func(t *testing.T) {
			var writes atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(204)
					return
				}
				_, _ = io.WriteString(w, body)
			}))
			_, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Description: vngcloud.Ptr("new")})
			if err == nil || writes.Load() != 0 {
				t.Fatalf("unproven group snapshot written: error=%v writes=%d", err, writes.Load())
			}
		})
	}
}

func TestAcceptedServiceAccountResponseFailureKeepsOutcome(t *testing.T) {
	c := newGuardTestClient(t, unprivilegedGuardFixture(), func(mux *http.ServeMux) {
		mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, `{`)
		})
	})
	out, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: "sa-1", Description: vngcloud.Ptr("new")})
	if out == nil || out.ServiceAccount.ID != "sa-1" || !errors.Is(err, ErrNotSettled) {
		t.Fatalf("accepted write lost on decode: output=%v error=%v", out, err)
	}
}

func TestIAMAttachedPolicyAndUserPagesKeepMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Client) (any, error)
	}{
		{"users", func(c *Client) (any, error) {
			return c.ListUsers(context.Background(), &ListUsersInput{Page: 1, Size: 2})
		}},
		{"group-policies", func(c *Client) (any, error) {
			return c.ListGroupPolicies(context.Background(), &ListGroupPoliciesInput{GroupID: "group-1", Page: 1, Size: 2})
		}},
		{"user-policies", func(c *Client) (any, error) {
			return c.ListUserPolicies(context.Background(), &ListUserPoliciesInput{UserID: "user-1", Page: 1, Size: 2})
		}},
		{"service-account-policies", func(c *Client) (any, error) {
			return c.ListServiceAccountPolicies(context.Background(), &ListServiceAccountPoliciesInput{ServiceAccountID: "sa-1", Page: 1, Size: 2})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("pageNumber") != "1" || r.URL.Query().Get("pageSize") != "2" {
					t.Error("page selection lost")
				}
				_, _ = io.WriteString(w, `{"data":[],"pageNumber":1,"pageSize":2,"totalItems":2,"totalPages":2}`)
			}))
			out, err := tc.call(c)
			raw, _ := json.Marshal(out)
			if err != nil || !strings.Contains(string(raw), `"PageNumber":1`) || !strings.Contains(string(raw), `"TotalPages":2`) {
				t.Fatalf("page metadata lost: output=%s error=%v", raw, err)
			}
		})
	}
}

func TestPolicyReplacementSendsOnce(t *testing.T) {
	for _, status := range []int{307, 308, 401, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			g := unprivilegedGuardFixture()
			g.policies = map[string]Policy{"policy-1": {ID: "policy-1", Name: "policy", Description: "old", Manager: "user", Statements: []Statement{{Effect: "deny", Actions: []string{"iam:CreatePolicy"}, Resources: []string{"*"}}}}}
			var writes atomic.Int32
			handler := g.mux(t, func(mux *http.ServeMux) {
				mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(w http.ResponseWriter, _ *http.Request) {
					writes.Add(1)
					w.Header().Set("Location", "/policies-api/v1/policies/policy-1")
					w.WriteHeader(status)
				})
			})
			c := New(testutil.NewRetryConfig(t, handler))
			out, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("new")})
			if writes.Load() != 1 || err == nil {
				t.Fatalf("policy replacement replayed: writes=%d error=%v", writes.Load(), err)
			}
			if status != 401 && (out == nil || !errors.Is(err, ErrNotSettled)) {
				t.Fatalf("uncertain policy replacement lost: output=%v error=%v", out, err)
			}
		})
	}
}

func TestServiceAccountUpdateCarriesEarlierCredentials(t *testing.T) {
	tokens := &guardOperandTokens{later: "refreshed-bearer"}
	g := unprivilegedGuardFixture()
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PATCH /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, _ *http.Request) { tokens.changed.Store(true); w.WriteHeader(200) })
		mux.HandleFunc("GET /accounts-api/v1/service-accounts/sa-1", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"sa-1","description":"initial-bearer"}`)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	var captures atomic.Int32
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: tokens, Capture: func(transport.Capture) { captures.Add(1) }}))
	out, err := New(cfg).UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: "sa-1", Description: vngcloud.Ptr("new")})
	raw, _ := json.Marshal(out)
	if out == nil || !errors.Is(err, ErrNotSettled) || strings.Contains(string(raw), "initial-bearer") || strings.Contains(fmt.Sprint(err), "initial-bearer") || captures.Load() != 0 {
		t.Fatalf("earlier credential escaped: output=%s error=%v captures=%d", raw, err, captures.Load())
	}
}

func TestIAMMetadataFromRecordedPages(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		call       func(*Client) (any, error)
	}{
		{"policies", "../testdata/iam/list_policies.json", func(c *Client) (any, error) { return c.ListPolicies(context.Background(), nil) }},
		{"service-accounts", "../testdata/iam/list_service_accounts.json", func(c *Client) (any, error) { return c.ListServiceAccounts(context.Background(), nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { testutil.WriteFixture(t, w, tc.file) }))
			out, err := tc.call(c)
			raw, _ := json.Marshal(out)
			if err != nil || !strings.Contains(string(raw), `"PageNumber":0`) || !strings.Contains(string(raw), `"TotalPages":1`) {
				t.Fatalf("recorded page metadata lost: output=%s error=%v", raw, err)
			}
		})
	}
}
