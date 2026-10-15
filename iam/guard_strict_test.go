package iam

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const strictSafePolicy = `{"id":"policy-1","manager":"user","statements":[{"effect":"allow","actions":["vserver:List*"]}]}`

func strictGuardClient(t *testing.T, overrides map[string]string) (*Client, *atomic.Int32) {
	t.Helper()
	bodies := map[string]string{
		"/accounts-api/v1/auth/userinfo":                                   `{"userId":"caller-1","userType":"iam-user"}`,
		"/policies-api/v1/actions":                                         `[{"action":"CreatePolicy","label":"Write"}]`,
		"/policies-api/v1/policies/policy-1":                               strictSafePolicy,
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": `{"data":[{"id":"policy-1"}],"totalItems":1}`,
		"/policies-api/v1/user-attachments/iam-users/user-2/policies":      `{"data":[],"totalItems":0}`,
		"/policies-api/v1/user-attachments/iam-users/user-2/groups":        `[]`,
		"/policies-api/v1/groups/group-1":                                  `{"id":"group-1","name":"group","mode":"iam","iamUsers":[],"policies":[]}`,
		"/policies-api/v1/policies/policy-1/groups":                        `[]`,
		"/policies-api/v1/policies/policy-1/iam-users":                     `[]`,
		"/policies-api/v1/policies/policy-1/service-accounts":              `[]`,
	}
	for path, body := range overrides {
		bodies[path] = body
	}
	writes := new(atomic.Int32)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, ok := bodies[r.URL.Path]
		if !ok {
			t.Errorf("unexpected read %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	return c, writes
}

func strictDeleteSA(c *Client) error {
	_, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-1"})
	return err
}

func strictAttachUser(c *Client) error {
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-2"})
	return err
}

func TestGuardStrictPolicy(t *testing.T) {
	bodies := map[string]string{
		"actions alias":       `{"manager":"user","statements":[{"effect":"allow","actions":["iam:*"],"Actions":["vserver:List*"]}]}`,
		"repeated statements": `{"manager":"user","statements":[{"effect":"allow","actions":["vserver:List*"]}],"statements":[null]}`,
	}
	for name, body := range bodies {
		for op, call := range map[string]func(*Client) error{"delete service account": strictDeleteSA, "attach user": strictAttachUser} {
			t.Run(name+"/"+op, func(t *testing.T) {
				c, writes := strictGuardClient(t, map[string]string{"/policies-api/v1/policies/policy-1": body})
				err := call(c)
				var api *vngcloud.APIError
				if !errors.As(err, &api) {
					t.Fatalf("want transport decode error, got %v", err)
				}
				if err == nil || writes.Load() != 0 {
					t.Fatalf("err = %v, writes = %d; want refusal and no writes", err, writes.Load())
				}
			})
		}
	}
}

func TestGuardStrictReads(t *testing.T) {
	deleteGroup := func(c *Client) error {
		_, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"})
		return err
	}
	deletePolicy := func(c *Client) error {
		_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: "policy-1"})
		return err
	}
	cases := []struct {
		name, path, body string
		call             func(*Client) error
	}{
		{"caller userId", "/accounts-api/v1/auth/userinfo", `{"userId":"user-2","userId":"caller-1","userType":"iam-user"}`, strictAttachUser},
		{"caller userType", "/accounts-api/v1/auth/userinfo", `{"userId":"caller-1","userType":"user-sa","userType":"iam-user"}`, strictDeleteSA},
		{"page data", "/policies-api/v1/user-attachments/service-accounts/sa-1/policies", `{"data":[{"id":"hidden"}],"data":[],"totalItems":0}`, strictDeleteSA},
		{"page totalItems", "/policies-api/v1/user-attachments/service-accounts/sa-1/policies", `{"data":[],"totalItems":1,"totalItems":0}`, strictDeleteSA},
		{"group users", "/policies-api/v1/groups/group-1", `{"mode":"iam","iamUsers":["caller-1"],"iamUsers":[],"policies":[]}`, deleteGroup},
		{"group policies", "/policies-api/v1/groups/group-1", `{"mode":"iam","iamUsers":[],"policies":["hidden"],"policies":[]}`, deleteGroup},
		{"actions label", "/policies-api/v1/actions", `[{"action":"CreatePolicy","label":"Write","label":"Read"},{"action":"Other","label":"Write"}]`, strictDeleteSA},
		{"user groups", "/policies-api/v1/user-attachments/iam-users/user-2/groups", `[{"id":"group-1","policies":["hidden"],"Policies":[]}]`, strictAttachUser},
		{"page summary alias", "/policies-api/v1/user-attachments/service-accounts/sa-1/policies", `{"data":[{"id":"hidden","Id":"policy-1"}],"totalItems":1}`, strictDeleteSA},
		{"group summary alias", "/policies-api/v1/policies/policy-1/groups", `[{"id":"hidden","Id":"group-1"}]`, func(c *Client) error {
			_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1"})
			return err
		}},
		{"policy attachments", "/policies-api/v1/policies/policy-1/groups", `[{"id":"hidden","id":"group-1"}]`, deletePolicy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, writes := strictGuardClient(t, map[string]string{tc.path: tc.body})
			err := tc.call(c)
			var api *vngcloud.APIError
			if !errors.As(err, &api) {
				t.Fatalf("want transport decode error, got %v", err)
			}
			if err == nil || writes.Load() != 0 {
				t.Fatalf("err = %v, writes = %d; want refusal and no writes", err, writes.Load())
			}
		})
	}
}

func TestGuardStrictBounds(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		cap              int
	}{
		{"policy", "/policies-api/v1/policies/policy-1", strictSafePolicy, 256 << 10},
		{"list", "/policies-api/v1/user-attachments/service-accounts/sa-1/policies", `{"data":[],"totalItems":0}`, 4 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body + strings.Repeat(" ", tc.cap+1-len(tc.body))
			c, writes := strictGuardClient(t, map[string]string{tc.path: body})
			err := strictDeleteSA(c)
			if !errors.Is(err, transport.ErrBodyTooLarge) || writes.Load() != 0 {
				t.Fatalf("err = %v, writes = %d; want ErrBodyTooLarge and no writes", err, writes.Load())
			}
		})
	}
}

func TestGuardStrictNormal(t *testing.T) {
	for name, call := range map[string]func(*Client) error{"delete service account": strictDeleteSA, "attach user": strictAttachUser} {
		t.Run(name, func(t *testing.T) {
			c, writes := strictGuardClient(t, nil)
			if err := call(c); err != nil || writes.Load() != 1 {
				t.Fatalf("err = %v, writes = %d; want one write", err, writes.Load())
			}
		})
	}
}

func TestGuardStrictRawPolicyFixture(t *testing.T) {
	body := testutil.FixtureBody(t, "../testdata/iam/get_policy_customer_numeric_root.json")
	c, _ := strictGuardClient(t, map[string]string{"/policies-api/v1/policies/policy-1": body})
	got, err := c.GetPolicy(context.Background(), &GetPolicyInput{PolicyID: "policy-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Policy.Root == "" || len(got.Policy.Statements) == 0 {
		t.Fatalf("lost wire fields: %+v", got.Policy)
	}
}

func TestGuardStrictWritePrereads(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		call             func(*Client) error
	}{
		{"update policy", "/policies-api/v1/policies/policy-1", `{"manager":"user","statements":[{"effect":"allow","actions":["iam:*"],"Actions":["vserver:List*"]}]}`, func(c *Client) error {
			_, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1"})
			return err
		}},
		{"update group", "/policies-api/v1/groups/group-1", `{"name":"hidden","Name":"replacement","mode":"iam","iamUsers":[],"policies":[]}`, func(c *Client) error {
			_, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, writes := strictGuardClient(t, map[string]string{tc.path: tc.body})
			err := tc.call(c)
			var api *vngcloud.APIError
			if !errors.As(err, &api) || writes.Load() != 0 {
				t.Fatalf("err = %v, writes = %d; want decode refusal", err, writes.Load())
			}
		})
	}
}

func TestGuardStrictRawGroupFixture(t *testing.T) {
	body := testutil.FixtureBody(t, "../testdata/iam/get_group_numeric_root.json")
	c, _ := strictGuardClient(t, map[string]string{"/policies-api/v1/groups/group-1": body})
	got, err := c.GetGroup(context.Background(), &GetGroupInput{GroupID: "group-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Group.Root != "700000000123" || got.Group.CreatedAt != 1750000000000 || len(got.Group.UserIDs) != 1 || len(got.Group.PolicyIDs) != 1 {
		t.Fatalf("lost wire fields: %+v", got.Group)
	}
}

func TestGuardStrictPolicyCondition(t *testing.T) {
	body := `{"statements":[{"effect":"allow","actions":["vserver:List*"],"condition":{"stringEquals":{"Env":"one","env":"two"}}}]}`
	c, _ := strictGuardClient(t, map[string]string{"/policies-api/v1/policies/policy-1": body})
	got, err := c.GetPolicy(context.Background(), &GetPolicyInput{PolicyID: "policy-1"})
	if err != nil {
		t.Fatal(err)
	}
	condition := got.Policy.Statements[0].Condition["stringEquals"]
	if len(condition) != 2 || condition["Env"] != "one" || condition["env"] != "two" {
		t.Fatalf("condition = %v", condition)
	}
}
