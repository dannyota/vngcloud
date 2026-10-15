package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestPublicUserCredentialEchoes(t *testing.T) {
	for _, list := range []bool{false, true} {
		fields := []string{"id", "username", "from"}
		if list {
			fields = []string{"id", "username", "createdAt"}
		}
		for _, field := range fields {
			for _, refresh := range []bool{false, true} {
				for _, echo := range []string{"first-bearer", "refreshed-bearer"} {
					if !refresh && echo == "refreshed-bearer" {
						continue
					}
					t.Run(strconv.FormatBool(list)+"/"+field+"/"+strconv.FormatBool(refresh)+"/"+echo, func(t *testing.T) {
						calls := 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls++
							want := "first-bearer"
							if calls > 1 {
								want = "refreshed-bearer"
							}
							if r.Header.Get("Authorization") != "Bearer "+want {
								t.Error("wrong sent credential")
							}
							if refresh && calls == 1 {
								w.WriteHeader(http.StatusUnauthorized)
								return
							}
							row := map[string]string{"id": "user-1", "username": "ordinary", "from": "iam"}
							row[field] = "prefix-" + echo + "-suffix"
							var body any = row
							if list {
								body = map[string]any{"data": []map[string]string{{"id": "safe", "username": "safe"}, row}}
							}
							_ = json.NewEncoder(w).Encode(body)
						}))
						defer server.Close()
						source := new(userWriteTokens)
						cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source}))
						c := New(cfg)
						if list {
							out, err := c.ListUsers(context.Background(), nil)
							if out != nil || err == nil {
								t.Fatal("credential-bearing list accepted")
							}
							assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer")
						} else {
							out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
							if out != nil || err == nil {
								t.Fatal("credential-bearing user accepted")
							}
							assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer")
						}
						if calls != 1+boolInt(refresh) {
							t.Fatal("unexpected request count")
						}
					})
				}
			}
		}
	}
}

func TestPublicUserCredentialsArePerCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// The first call's old token is ordinary peer text on a later call.
		_, _ = w.Write([]byte(`{"id":"user-1","username":"first-bearer"}`))
	}))
	defer server.Close()
	source := new(userWriteTokens)
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source}))
	c := New(cfg)
	_, _ = c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
	out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
	if err != nil || out == nil || !strings.Contains(out.User.Username, "first-bearer") || calls != 3 {
		t.Fatal("credentials retained across calls")
	}
}

// Each retained string needs an end-to-end echo case as well as the generic
// response walker. Adding a model field must update this coverage inventory.
func TestUserResponseStringCoverage(t *testing.T) {
	for _, tc := range []struct {
		model   any
		covered []string
	}{
		{UserDetail{}, []string{"ID", "Username", "From"}},
		{User{}, []string{"ID", "Username", "CreatedAt"}},
		{GetCallerIdentityOutput{}, []string{"UserID", "UserType", "Username"}},
	} {
		typ := reflect.TypeOf(tc.model)
		t.Run(typ.Name(), func(t *testing.T) {
			covered := make(map[string]bool)
			for _, name := range tc.covered {
				covered[name] = true
			}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.PkgPath == "" && field.Type.Kind() == reflect.String {
					if !covered[field.Name] {
						t.Errorf("%s needs a credential echo case", field.Name)
					}
					delete(covered, field.Name)
				}
			}
			if len(covered) != 0 {
				t.Fatal("coverage inventory has obsolete fields")
			}
		})
	}
}

func TestCallerIdentityCredentialEchoes(t *testing.T) {
	for _, field := range []string{"userId", "userType", "username"} {
		for _, echo := range []string{"direct", "old-after-refresh", "new-after-refresh"} {
			t.Run(field+"/"+echo, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if echo != "direct" && calls == 1 {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					if echo == "old-after-refresh" {
						token = "first-bearer"
					}
					row := map[string]string{"userId": "caller", "userType": "iam-user", "username": "ordinary"}
					row[field] = "prefix-" + token + "-suffix"
					_ = json.NewEncoder(w).Encode(row)
				}))
				defer server.Close()
				cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: new(userWriteTokens)}))
				out, err := New(cfg).GetCallerIdentity(context.Background(), nil)
				if out != nil || err == nil {
					t.Fatal("credential-bearing caller accepted")
				}
				assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer")
				want := 1
				if echo != "direct" {
					want = 2
				}
				if calls != want {
					t.Fatal("wrong request count")
				}
			})
		}
	}
}

func TestUserResponseCredentialWalk(t *testing.T) {
	type label string
	type extra struct{ Future label }
	sent := []string{"sent-canary"}
	opts := userReadOptions{SentCredentials: &sent, Redact: []string{"password-canary"}}
	for _, tc := range []struct {
		name  string
		value any
		bad   bool
	}{
		{"future-field", &struct {
			ID    string
			Extra extra
		}{ID: "safe", Extra: extra{Future: "prefix-sent-canary-suffix"}}, true},
		{"password", extra{Future: "prefix-password-canary-suffix"}, true},
		{"pointer", &extra{Future: "sent-canary"}, true},
		{"interface", struct{ Value any }{Value: extra{Future: "sent-canary"}}, true},
		{"slice", []extra{{Future: "safe"}, {Future: "sent-canary"}}, true},
		{"array", [1]extra{{Future: "sent-canary"}}, true},
		{"map-key", map[label]int{"sent-canary": 1}, true},
		{"map-value", map[string]extra{"safe": {Future: "sent-canary"}}, true},
		{"safe", extra{Future: "safe"}, false},
		{"nil", nil, false},
		{"nil-pointer", (*extra)(nil), false},
		{"nil-map", map[string]string(nil), false},
		{"nil-slice", []string(nil), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if opts.containsResponseCredential(tc.value) != tc.bad {
				t.Fatal("incorrect response credential decision")
			}
		})
	}
}
