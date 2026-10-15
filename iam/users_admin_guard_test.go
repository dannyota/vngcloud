package iam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestResetUserPasswordGuards(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		want             error
		reads            int
	}{
		{"self", "/accounts-api/v1/auth/userinfo", `{"userId":"USER-1","userType":"iam-user"}`, ErrSelfChange, 1},
		{"root", "/accounts-api/v1/auth/userinfo", `{"userId":"caller","userType":"root-user"}`, ErrPrivilegedChange, 1},
		{"unknown-caller", "/accounts-api/v1/auth/userinfo", `{"userId":"caller","userType":"unknown"}`, ErrPrivilegedChange, 1},
		{"missing-caller-id", "/accounts-api/v1/auth/userinfo", `{"userType":"iam-user"}`, ErrPrivilegedChange, 1},
		{"invalid-caller-id", "/accounts-api/v1/auth/userinfo", `{"userId":"../x","userType":"iam-user"}`, ErrPrivilegedChange, 1},
		{"non-iam", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":"idp"}`, ErrOriginNotIAM, 2},
		{"null-origin", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":null}`, ErrOriginNotIAM, 2},
		{"wrong-origin-type", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":17}`, ErrOriginNotIAM, 2},
		{"mismatched-id", "/accounts-api/v1/iam-users/user-1", `{"id":"other","from":"iam"}`, ErrOriginNotIAM, 2},
		{"duplicate-target", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","ID":"other","from":"iam"}`, ErrOriginNotIAM, 2},
		{"direct-privilege", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`, ErrPrivilegedChange, 5},
		{"null-statement", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[null]}`, ErrPrivilegedChange, 5},
		{"unknown-effect", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"unknown","actions":["safe:read"]}]}`, ErrPrivilegedChange, 5},
		{"missing-actions", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"allow"}]}`, ErrPrivilegedChange, 5},
		{"user-sa", "/accounts-api/v1/auth/userinfo", `{"userId":"user-1","userType":"user-sa"}`, nil, 6},
		{"service-sa", "/accounts-api/v1/auth/userinfo", `{"userId":"user-1","userType":"service-sa"}`, nil, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, posts := 0, 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					w.WriteHeader(204)
					return
				}
				reads++
				body := resetGuardBody(r.URL.Path)
				if r.URL.Path == tc.path {
					body = tc.body
				}
				_, _ = io.WriteString(w, body)
			}))
			out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "synthetic-password"})
			if reads != tc.reads || (tc.want != nil && !errors.Is(err, tc.want)) || (err == nil) != (tc.want == nil) || (out != nil) != (tc.want == nil) {
				t.Fatalf("reads=%d posts=%d err=%v", reads, posts, err)
			}
			if (posts == 1) != (tc.want == nil) {
				t.Fatal("guard sent write")
			}
		})
	}
}

func TestResetUserPasswordIncompleteGuards(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/policies-api/v1/actions", `[]`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/policies", `{"data":[],"totalItems":1}`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/policies", `{"data":null,"totalItems":0}`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/groups", `[{"id":"group-1","policies":null}]`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/groups", `[{"id":"group-1"}]`},
	} {
		posts := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts++
				return
			}
			body := resetGuardBody(r.URL.Path)
			if r.URL.Path == tc.path {
				body = tc.body
			}
			_, _ = io.WriteString(w, body)
		}))
		out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
		if out != nil || err == nil || posts != 0 {
			t.Fatal("incomplete guard authorized reset")
		}
	}
}

func TestResetUserPasswordGroupPrivilege(t *testing.T) {
	posts := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			return
		}
		body := resetGuardBody(r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/user-1/policies") {
			body = `{"data":[],"totalItems":0}`
		}
		if strings.HasSuffix(r.URL.Path, "/policies/policy-1") {
			body = `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`
		}
		_, _ = io.WriteString(w, body)
	}))
	out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
	if out != nil || posts != 0 || !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("group protection: %v", err)
	}
}

func TestResetUserPasswordLengthsAndFormatting(t *testing.T) {
	for _, length := range []int{0, 8, 32, 50} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(204)
					return
				}
				_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
			}))
			out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", PasswordLength: length})
			if err != nil || out == nil {
				t.Fatalf("generation: %v", err)
			}
			if length == 0 {
				length = 32
			}
			if len(out.Password.Reveal()) != length {
				t.Fatal("wrong generated length")
			}
			if !passwordClasses([]byte(out.Password.Reveal())) {
				t.Fatal("missing password class")
			}
			// Secret supports safe standard formatting and JSON via its shared methods.
			if out.Password.String() == out.Password.Reveal() {
				t.Fatal("unredacted output")
			}
		})
	}
	for _, password := range []vngcloud.Secret{"x", " x ", "雪"} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(204)
				return
			}
			_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
		}))
		out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: password})
		if err != nil || out == nil || out.Password != "" {
			t.Fatalf("supplied shape: %v", err)
		}
	}
}

func TestResetUserPasswordUnknownActionLabel(t *testing.T) {
	posts := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			return
		}
		body := resetGuardBody(r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/actions") {
			body = `[{"action":"CreatePolicy","label":"Unknown"}]`
		}
		if strings.HasSuffix(r.URL.Path, "/policies/policy-1") {
			body = `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`
		}
		_, _ = io.WriteString(w, body)
	}))
	out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
	if out != nil || posts != 0 || !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("unknown label: %v", err)
	}
}

func TestResetUserPasswordPrivilegeChangesBeforeSend(t *testing.T) {
	posts := 0
	changed := false
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			if !changed {
				t.Error("expected concurrent privilege change")
			}
			w.WriteHeader(403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/user-1/groups") {
			changed = true
		}
		_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
	}))
	// Guards prove the observed reads only. The server still authorizes the
	// mutation after a concurrent rights change and may refuse it.
	out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
	if out != nil || posts != 1 || !errors.Is(err, vngcloud.ErrPermission) {
		t.Fatalf("concurrent change: %v", err)
	}
}
