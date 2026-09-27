package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// --- CreateSecurityGroupRule request body ---

func TestCreateSecurityGroupRuleRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateSecurityGroupRuleInput
		want map[string]any
	}{
		{
			name: "IPv4 prefix derives EtherType",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 443,
			},
			want: map[string]any{"etherType": "IPv4", "portRangeMin": float64(443), "portRangeMax": float64(443)},
		},
		{
			name: "IPv6 prefix derives EtherType",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "2001:db8::/32", PortRangeMin: 443,
			},
			want: map[string]any{"etherType": "IPv6", "portRangeMin": float64(443), "portRangeMax": float64(443)},
		},
		{
			name: "PortRangeMax 0 sent as PortRangeMin",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 22,
			},
			want: map[string]any{"portRangeMin": float64(22), "portRangeMax": float64(22)},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/secgroups/secg-1/secgroupRules" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				body := decodeBody(t, r)
				for k, v := range tt.want {
					if body[k] != v {
						t.Fatalf("body[%q] = %v (%T), want %v (%T); full body = %+v", k, body[k], body[k], v, v, body)
					}
				}
				if body["securityGroupId"] != "secg-1" {
					t.Fatalf("securityGroupId = %v, want secg-1", body["securityGroupId"])
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"uuid":"secr-1","secgroupUuid":"secg-1","ruleId":501}`))
			}))

			out, err := c.CreateSecurityGroupRule(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("CreateSecurityGroupRule() error = %v", err)
			}
			if out.SecurityGroupRule.ID != "secr-1" {
				t.Fatalf("ID = %q, want secr-1", out.SecurityGroupRule.ID)
			}
		})
	}
}

// --- Shape refusals: no request sent ---

func TestCreateSecurityGroupRuleShapeRefusals(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateSecurityGroupRuleInput
	}{
		{
			name: "bare address",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.5", PortRangeMin: 22,
			},
		},
		{
			name: "bad prefix",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.999/24", PortRangeMin: 22,
			},
		},
		{
			name: "family mismatch",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", EtherType: "IPv6", PortRangeMin: 22,
			},
		},
		{
			name: "port 70000",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 70000,
			},
		},
		{
			name: "min above max",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 100, PortRangeMax: 50,
			},
		},
		{
			name: "tcp with port 0",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 0,
			},
		},
		{
			name: "udp (any case) with port 0",
			in: &CreateSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "UDP",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 0,
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a shape refusal")
			}))

			_, err := c.CreateSecurityGroupRule(context.Background(), tt.in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateSecurityGroupRuleDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/network/create_security_group_rule.json")
	}))

	out, err := c.CreateSecurityGroupRule(context.Background(), &CreateSecurityGroupRuleInput{
		SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
		RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 22,
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroupRule() error = %v", err)
	}
	if out.SecurityGroupRule.ID != "secr-1" {
		t.Fatalf("ID = %q, want secr-1", out.SecurityGroupRule.ID)
	}
}

func TestCreateSecurityGroupRuleNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"secgroupUuid":"secg-1","ruleId":501}`))
	}))

	_, err := c.CreateSecurityGroupRule(context.Background(), &CreateSecurityGroupRuleInput{
		SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
		RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 22,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "create response had no id" {
		t.Fatalf("err = %v, want an APIError saying the create response had no id", err)
	}
}

func TestCreateSecurityGroupRuleNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateSecurityGroupRule(context.Background(), &CreateSecurityGroupRuleInput{
		SecurityGroupID: "secg-1", Direction: "ingress", Protocol: "tcp",
		RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 22,
	})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
}

// --- DeleteSecurityGroupRule ---

func TestDeleteSecurityGroupRuleNotInGroupRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"id":"secr-other","direction":"ingress"}]}`))
		default:
			t.Fatalf("unexpected method %s: a rule not in the group must send no DELETE", r.Method)
		}
	}))

	_, err := c.DeleteSecurityGroupRule(context.Background(), &DeleteSecurityGroupRuleInput{
		SecurityGroupID: "secg-1", SecurityGroupRuleID: "secr-1",
	})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteSecurityGroupRuleSuccess(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"id":"secr-1","direction":"ingress"}]}`))
		case http.MethodDelete:
			if !strings.HasSuffix(r.URL.Path, "/secgroups/secg-1/secgroupRules/secr-1") {
				t.Fatalf("unexpected delete path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	if _, err := c.DeleteSecurityGroupRule(context.Background(), &DeleteSecurityGroupRuleInput{
		SecurityGroupID: "secg-1", SecurityGroupRuleID: "secr-1",
	}); err != nil {
		t.Fatalf("DeleteSecurityGroupRule() error = %v", err)
	}
}

// --- Path ID checks ---

func TestSecurityGroupRulePathIDRejection(t *testing.T) {
	badIDs := []string{"..", ".", "a/b", "a?b", ""}

	for _, id := range badIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))

			if _, err := c.CreateSecurityGroupRule(context.Background(), &CreateSecurityGroupRuleInput{
				SecurityGroupID: id, Direction: "ingress", Protocol: "tcp",
				RemoteIPPrefix: "203.0.113.0/24", PortRangeMin: 22,
			}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("CreateSecurityGroupRule() SecurityGroupID err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DeleteSecurityGroupRule(context.Background(), &DeleteSecurityGroupRuleInput{
				SecurityGroupID: id, SecurityGroupRuleID: "secr-1",
			}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DeleteSecurityGroupRule() SecurityGroupID err = %v, want ErrInvalidInput", err)
			}
			if id == "" {
				return // CheckRequired already rejects an empty SecurityGroupRuleID.
			}
			if _, err := c.DeleteSecurityGroupRule(context.Background(), &DeleteSecurityGroupRuleInput{
				SecurityGroupID: "secg-1", SecurityGroupRuleID: id,
			}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DeleteSecurityGroupRule() SecurityGroupRuleID err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
