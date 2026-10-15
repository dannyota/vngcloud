package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
)

func TestPolicyIsPrivilegedMalformedStatements(t *testing.T) {
	writeActions := writeActionNames([]Action{{Action: "CreatePolicy", Label: "Write"}})
	cases := []struct {
		name       string
		document   string
		privileged bool
	}{
		{"null statement", `{"statements":[null]}`, true},
		{"empty statement", `{"statements":[{}]}`, true},
		{"allow null actions", `{"statements":[{"effect":"ALLOW","actions":null}]}`, true},
		{"allow empty actions", `{"statements":[{"effect":"ALLOW","actions":[]}]}`, true},
		{"allow missing actions", `{"statements":[{"effect":"ALLOW"}]}`, true},
		{"unknown effect without actions", `{"statements":[{"effect":"Audit"}]}`, true},
		{"unknown effect harmless actions", `{"statements":[{"effect":"Audit","actions":["vserver:List*"]}]}`, true},
		{"empty effect harmless actions", `{"statements":[{"effect":"","actions":["vserver:List*"]}]}`, true},
		{"deny without actions", `{"statements":[{"effect":"Deny"}]}`, false},
		{"deny privileged actions", `{"statements":[{"effect":"Deny","actions":["iam:*"]}]}`, false},
		{"allow privileged actions", `{"statements":[{"effect":"AlLoW","actions":["iam:CreatePolicy"]}]}`, true},
		{"allow harmless actions", `{"statements":[{"effect":"AlLoW","actions":["vserver:List*"]}]}`, false},
		{"no statements", `{"statements":[]}`, true},
		{"null statements", `{"statements":null}`, true},
		{"missing statements", `{}`, true},
		{"deny beside malformed", `{"statements":[{"effect":"Deny"},{}]}`, true},
		{"deny beside harmless allow", `{"statements":[{"effect":"Deny"},{"effect":"allow","actions":["vserver:List*"]}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var policy Policy
			if err := json.Unmarshal([]byte(tc.document), &policy); err != nil {
				t.Fatal(err)
			}
			if got := policyIsPrivileged(&policy, writeActions); got != tc.privileged {
				t.Errorf("policyIsPrivileged() = %v, want %v", got, tc.privileged)
			}
			if got := statementsArePrivileged(policy.Statements, writeActions); got != tc.privileged {
				t.Errorf("statementsArePrivileged() = %v, want %v", got, tc.privileged)
			}
		})
	}
}

func TestGuardRefusesMalformedStatements(t *testing.T) {
	cases := []struct {
		name     string
		document string
		setup    func(*guardFixture)
		call     func(*Client) error
	}{
		{
			name:     "user attach null statement",
			document: `{"id":"policy-malformed","manager":"user","statements":[null]}`,
			setup: func(g *guardFixture) {
				g.userPolicyIDs = map[string][]string{"user-x": {"policy-malformed"}}
			},
			call: func(c *Client) error {
				_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
				return err
			},
		},
		{
			name:     "user detach null statement",
			document: `{"id":"policy-malformed","manager":"user","statements":[null]}`,
			setup: func(g *guardFixture) {
				g.userPolicyIDs = map[string][]string{"user-x": {"policy-malformed"}}
			},
			call: func(c *Client) error {
				_, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
				return err
			},
		},
		{
			name:     "group attach empty allow actions",
			document: `{"id":"policy-malformed","manager":"user","statements":[{"effect":"allow","actions":[]}]}`,
			setup: func(g *guardFixture) {
				g.groups = map[string]Group{"group-1": {ID: "group-1", PolicyIDs: []string{"policy-malformed"}}}
			},
			call: func(c *Client) error {
				_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
				return err
			},
		},
		{
			name:     "group membership empty allow actions",
			document: `{"id":"policy-malformed","manager":"user","statements":[{"effect":"allow","actions":[]}]}`,
			setup: func(g *guardFixture) {
				g.groups = map[string]Group{"group-1": {ID: "group-1", PolicyIDs: []string{"policy-malformed"}}}
			},
			call: func(c *Client) error {
				_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
				return err
			},
		},
		{
			name:     "update current unknown effect",
			document: `{"id":"policy-malformed","manager":"user","statements":[{"effect":"Audit","actions":["vserver:List*"]}]}`,
			setup:    func(_ *guardFixture) {},
			call: func(c *Client) error {
				name := "updated"
				_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-malformed", Name: &name})
				return err
			},
		},
		{
			name:     "service account attach empty statement",
			document: `{"id":"policy-malformed","manager":"user","statements":[{}]}`,
			setup: func(g *guardFixture) {
				g.attachedPolicyIDs = []string{"policy-malformed"}
			},
			call: func(c *Client) error {
				_, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
				return err
			},
		},
		{
			name:     "service account empty statement",
			document: `{"id":"policy-malformed","manager":"user","statements":[{}]}`,
			setup: func(g *guardFixture) {
				g.attachedPolicyIDs = []string{"policy-malformed"}
			},
			call: func(c *Client) error {
				_, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := unprivilegedUserAttachFixture()
			tc.setup(&g)
			var writes atomic.Int32
			c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
				mux.HandleFunc("GET /policies-api/v1/policies/policy-malformed", func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(tc.document))
				})
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes.Add(1)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					t.Errorf("unexpected read: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				})
			})
			if err := tc.call(c); !errors.Is(err, ErrPrivilegedChange) {
				t.Errorf("guarded write error = %v, want ErrPrivilegedChange", err)
			}
			if got := writes.Load(); got != 0 {
				t.Errorf("write requests = %d, want 0", got)
			}
		})
	}
}

func TestGuardCreatePolicyRefusesMissingActions(t *testing.T) {
	statements := []Statement{{Effect: "allow", Resources: []string{"*"}}}
	for _, directGuard := range []bool{false, true} {
		name := "public validation"
		if directGuard {
			name = "guard"
		}
		t.Run(name, func(t *testing.T) {
			g := unprivilegedGuardFixture()
			var writes atomic.Int32
			c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
				mux.HandleFunc("POST /policies-api/v1/policies", func(w http.ResponseWriter, r *http.Request) {
					writes.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				})
			})
			var err error
			want := core.ErrInvalidInput
			if directGuard {
				err = c.guardCreatePolicy(context.Background(), "iam.CreatePolicy", statements)
				want = ErrPrivilegedChange
			} else {
				_, err = c.CreatePolicy(context.Background(), &CreatePolicyInput{Name: "policy", Statements: statements})
			}
			if !errors.Is(err, want) {
				t.Errorf("CreatePolicy refusal = %v, want %v", err, want)
			}
			if got := writes.Load(); got != 0 {
				t.Errorf("write requests = %d, want 0", got)
			}
		})
	}
}
