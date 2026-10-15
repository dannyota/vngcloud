package iam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestGuardMalformedActionCatalog(t *testing.T) {
	for _, operation := range []string{"reset", "attach"} {
		for _, tc := range []struct{ name, row string }{
			{"null", `null`},
			{"missing", `{"label":"Write"}`},
			{"null-action", `{"action":null,"label":"Write"}`},
			{"empty", `{"action":"","label":"Write"}`},
			{"prefix-only", `{"action":"iam:","label":"Write"}`},
			{"uppercase-prefix-only", `{"action":"IAM:","label":"Write"}`},
			{"space", `{"action":"Create Policy","label":"Write"}`},
			{"trailing-space", `{"action":"CreatePolicy ","label":"Write"}`},
			{"control", `{"action":"CreatePolicy\t","label":"Write"}`},
			{"punctuation", `{"action":"Create.Policy","label":"Write"}`},
			{"action-hyphen", `{"action":"Create-Policy","label":"Write"}`},
			{"other-product", `{"action":"other:CreatePolicy","label":"Write"}`},
			{"double-prefix", `{"action":"iam:iam:CreatePolicy","label":"Write"}`},
			{"malformed-read", `{"action":"","label":"Read"}`},
		} {
			for _, prefix := range []string{"", `{"action":"DeletePolicy","label":"Write"},`} {
				t.Run(operation+"/"+tc.name+"/mixed="+strconv.FormatBool(prefix != ""), func(t *testing.T) {
					catalog := `[` + prefix + tc.row + `]`
					writes, actionReads := 0, 0
					protected := true
					c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet {
							writes++
							w.WriteHeader(http.StatusNoContent)
							return
						}
						body := resetGuardBody(r.URL.Path)
						if strings.HasSuffix(r.URL.Path, "/actions") {
							actionReads++
							body = catalog
						}
						if protected && strings.HasSuffix(r.URL.Path, "/policies/policy-1") {
							body = `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`
						}
						_, _ = io.WriteString(w, body)
					}))
					call := func() error { return catalogGuardWrite(c, operation) }
					for range 2 {
						if err := call(); err == nil || writes != 0 || len(c.guardWriteActions) != 0 {
							t.Fatalf("malformed catalog: error=%v writes=%d cached=%d", err, writes, len(c.guardWriteActions))
						}
					}
					if actionReads != 2 {
						t.Fatalf("action reads=%d, want 2 uncached reads", actionReads)
					}
					catalog = `[{"action":"IAM:CreatePolicy","label":"Write"}]`
					if err := call(); !errors.Is(err, ErrPrivilegedChange) || writes != 0 {
						t.Fatalf("protected user: error=%v writes=%d", err, writes)
					}
					protected = false
					if err := call(); err != nil || writes != 1 || actionReads != 3 {
						t.Fatalf("normal catalog: error=%v writes=%d action reads=%d", err, writes, actionReads)
					}
				})
			}
		}
	}
}

func catalogGuardWrite(c *Client, operation string) error {
	if operation == "reset" {
		_, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "synthetic-password"})
		return err
	}
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{UserID: "user-1", PolicyID: "policy-1"})
	return err
}
