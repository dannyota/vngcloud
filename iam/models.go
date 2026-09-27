package iam

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// epochMillis decodes a timestamp the accounts and policies APIs send in
// three forms across otherwise identical rows: a plain epoch-milliseconds
// number, an object {"$numberLong": "<digits>"}, or the key left out
// entirely. A field routed through epochMillis, via the alias-struct
// pattern each model's UnmarshalJSON below uses, reads as 0 when the key is
// missing or null.
type epochMillis int64

func (m *epochMillis) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = 0
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*m = epochMillis(n)
		return nil
	}
	var wrapped struct {
		NumberLong string `json:"$numberLong"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return fmt.Errorf("iam: timestamp: %w", err)
	}
	n, err := strconv.ParseInt(wrapped.NumberLong, 10, 64)
	if err != nil {
		return fmt.Errorf("iam: timestamp: %w", err)
	}
	*m = epochMillis(n)
	return nil
}

// Statement is one allow or deny rule of a policy document, in the API's
// own shape: Effect is lower case ("allow" or "deny"), Actions are
// "<product>:<Action>" with optional "*" wildcards, and Resources use the
// builder formats GET resources documents. Condition's keys are the
// operators the API defines (such as "stringEquals"); the SDK does not
// check them, since the server checks action, resource, and condition
// values itself.
type Statement struct {
	Effect    string                    `json:"effect"`
	Actions   []string                  `json:"actions"`
	Resources []string                  `json:"resources"`
	Condition map[string]map[string]any `json:"condition,omitempty"`
}

// PolicySummary is one row of a policy list: a get-policy adds Description,
// Manager, Scope, Root, and Statements, none of which a list row carries.
type PolicySummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

func (p *PolicySummary) UnmarshalJSON(data []byte) error {
	type alias PolicySummary
	aux := struct {
		CreatedAt epochMillis `json:"createdAt"`
		*alias
	}{alias: (*alias)(p)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	p.CreatedAt = int64(aux.CreatedAt)
	return nil
}

// Policy is a get-policy response. Manager is "VNG CLOUD" for a
// GreenNode-managed policy, which also leaves Root null or absent, and
// "user" for a customer policy, which sets Root to the owning account
// number. Managed reports whether the policy is one the caller cannot
// change or delete.
type Policy struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Manager     string      `json:"manager"`
	Scope       string      `json:"scope"`
	Root        string      `json:"root,omitempty"`
	Statements  []Statement `json:"statements"`
	CreatedAt   int64       `json:"createdAt"`
}

// Managed reports whether p is a GreenNode-managed policy: one the caller
// can read and attach but never update or delete.
func (p Policy) Managed() bool {
	return p.Manager != "user"
}

func (p *Policy) UnmarshalJSON(data []byte) error {
	type alias Policy
	aux := struct {
		CreatedAt epochMillis `json:"createdAt"`
		*alias
	}{alias: (*alias)(p)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	p.CreatedAt = int64(aux.CreatedAt)
	return nil
}

// GroupSummary is one row of a group list: a get-group adds Description,
// Root, UserIDs, and PolicyIDs, none of which a list row carries.
type GroupSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

func (g *GroupSummary) UnmarshalJSON(data []byte) error {
	type alias GroupSummary
	aux := struct {
		CreatedAt epochMillis `json:"createdAt"`
		*alias
	}{alias: (*alias)(g)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	g.CreatedAt = int64(aux.CreatedAt)
	return nil
}

// Group is a get-group response. Mode is "iam" for every group this SDK
// creates; an "idp" group is out of scope (see the design's non-goals).
type Group struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        string   `json:"mode"`
	Root        string   `json:"root,omitempty"`
	UserIDs     []string `json:"iamUsers"`
	PolicyIDs   []string `json:"policies"`
	CreatedAt   int64    `json:"createdAt"`
}

func (g *Group) UnmarshalJSON(data []byte) error {
	type alias Group
	aux := struct {
		CreatedAt epochMillis `json:"createdAt"`
		*alias
	}{alias: (*alias)(g)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	g.CreatedAt = int64(aux.CreatedAt)
	return nil
}

// ServiceAccount has no secret field: the client secret is returned only
// once, by CreateServiceAccount and ResetServiceAccountSecret, as a
// vngcloud.Secret alongside this model. LastUse is 0 when the service
// account has never authenticated.
type ServiceAccount struct {
	ID                  string `json:"id"`
	ClientID            string `json:"clientId"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	AccessTokenLifeSpan int    `json:"accessTokenLifeSpan"`
	CreatedAt           int64  `json:"createdAt"`
	Enabled             bool   `json:"enabled"`
	LastUse             int64  `json:"lastUse"`
}

func (s *ServiceAccount) UnmarshalJSON(data []byte) error {
	type alias ServiceAccount
	aux := struct {
		CreatedAt epochMillis `json:"createdAt"`
		LastUse   epochMillis `json:"lastUse"`
		*alias
	}{alias: (*alias)(s)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	s.CreatedAt = int64(aux.CreatedAt)
	s.LastUse = int64(aux.LastUse)
	return nil
}

// User is an IAM user row from the accounts API. Unlike every other model
// in this package, CreatedAt arrives as an RFC 3339 string, so it needs no
// flexible decode.
type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	CreatedAt string `json:"createdAt"`
}

// Action is one row of the account's IAM action list (GET
// actions?product=iam). Label is "List", "Read", "Write", or "Tagging"; an
// unrecognized label is still a write action for the guard rules in
// guard.go.
type Action struct {
	Action    string   `json:"action"`
	Label     string   `json:"label"`
	Resources []string `json:"resources"`
}
