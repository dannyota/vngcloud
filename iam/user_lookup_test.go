package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func userLookupFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../testdata/iam/list_users_filtered.json")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestLookupUserCurrentMatch(t *testing.T) {
	for _, emptyPages := range []int{-1, 0, 1} {
		t.Run(strconv.Itoa(emptyPages), func(t *testing.T) {
			body := userLookupFixture(t)
			wantCalls := 2
			if emptyPages >= 0 {
				body["data"] = []any{}
				body["totalItems"] = 0
				body["totalPages"] = emptyPages
				wantCalls = 1
			}
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					if r.Method != http.MethodGet || r.URL.Path != "/accounts-api/v1/iam-users" || r.URL.Query().Get("name") != "vngcloud-probe-0000aaaa" || r.URL.Query().Get("pageSize") != "1000" || r.URL.Query().Get("pageNumber") != "0" || len(r.URL.Query()) != 3 {
						t.Fatalf("unexpected request: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(body)
				} else {
					if r.URL.Path != "/accounts-api/v1/iam-users/00000000-0000-4000-8000-000000000001" {
						t.Fatalf("unexpected path: %s", r.URL.Path)
					}
					raw, err := os.ReadFile("../testdata/iam/get_user.json")
					if err != nil {
						t.Fatal(err)
					}
					_, _ = w.Write(raw)
				}
			}))
			user, err := c.lookupUser(context.Background(), "vngcloud-probe-0000aaaa", userReadOptions{})
			if err != nil || calls != wantCalls || (user != nil) != (emptyPages < 0) {
				t.Fatalf("lookup = %+v, %v; calls=%d", user, err, calls)
			}
		})
	}
}

func TestLookupUserRejectsIncompleteOrAmbiguousList(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"null data":     func(b map[string]any) { b["data"] = nil },
		"wrong data":    func(b map[string]any) { b["data"] = "bad" },
		"wrong page":    func(b map[string]any) { b["pageNumber"] = 1 },
		"negative page": func(b map[string]any) { b["pageNumber"] = -1 },
		"zero size":     func(b map[string]any) { b["pageSize"] = 0 },
		"large size":    func(b map[string]any) { b["pageSize"] = 1001 },
		"small size": func(b map[string]any) {
			b["pageSize"] = 1
			b["data"] = append(b["data"].([]any), map[string]any{"id": "user-2", "username": "other"})
			b["totalItems"] = 2
		},
		"negative total":      func(b map[string]any) { b["totalItems"] = -1 },
		"inconsistent total":  func(b map[string]any) { b["totalItems"] = 2 },
		"multiple pages":      func(b map[string]any) { b["totalPages"] = 2 },
		"negative pages":      func(b map[string]any) { b["totalPages"] = -1 },
		"zero pages nonempty": func(b map[string]any) { b["totalPages"] = 0 },
		"substring": func(b map[string]any) {
			b["data"].([]any)[0].(map[string]any)["username"] = "vngcloud-probe-0000aaaa-extra"
		},
		"case only":  func(b map[string]any) { b["data"].([]any)[0].(map[string]any)["username"] = "VNGCLOUD-probe-0000aaaa" },
		"invalid id": func(b map[string]any) { b["data"].([]any)[0].(map[string]any)["id"] = "../bad" },
		"null row":   func(b map[string]any) { b["data"] = []any{nil} },
		"duplicate id": func(b map[string]any) {
			row := b["data"].([]any)[0].(map[string]any)
			b["data"] = append(b["data"].([]any), map[string]any{"id": strings.ToUpper(row["id"].(string)), "username": "other"})
			b["totalItems"] = 2
		},
		"duplicate exact name": func(b map[string]any) {
			b["data"] = append(b["data"].([]any), map[string]any{"id": "user-2", "username": "vngcloud-probe-0000aaaa"})
			b["totalItems"] = 2
		},
	}
	for _, key := range []string{"data", "pageNumber", "pageSize", "totalItems", "totalPages"} {
		mutations["missing "+key] = func(b map[string]any) { delete(b, key) }
		mutations["null "+key] = func(b map[string]any) { b[key] = nil }
		mutations["wrong type "+key] = func(b map[string]any) { b[key] = true }
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := userLookupFixture(t)
			mutate(b)
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _ = json.NewEncoder(w).Encode(b) }))
			user, err := c.lookupUser(context.Background(), "vngcloud-probe-0000aaaa", userReadOptions{})
			if err == nil || user != nil || calls != 1 {
				t.Fatalf("lookup=%+v, %v; calls=%d", user, err, calls)
			}
		})
	}
}

func TestLookupUserRejectsWrongGetIdentity(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"id":"wrong","username":"vngcloud-probe-0000aaaa","from":"iam"}`,
		`{"id":"00000000-0000-4000-8000-000000000001","username":"other","from":"iam"}`,
		`{"id":"00000000-0000-4000-8000-000000000001","username":"vngcloud-probe-0000aaaa"}`,
		`{"id":"00000000-0000-4000-8000-000000000001","username":"vngcloud-probe-0000aaaa","from":null}`,
		`{"id":"00000000-0000-4000-8000-000000000001","username":"vngcloud-probe-0000aaaa","from":"IAM"}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 1 {
					_ = json.NewEncoder(w).Encode(userLookupFixture(t))
				} else {
					_, _ = w.Write([]byte(body))
				}
			}))
			user, err := c.lookupUser(context.Background(), "vngcloud-probe-0000aaaa", userReadOptions{})
			if err == nil || user != nil || calls != 2 {
				t.Fatalf("lookup=%+v, %v; calls=%d", user, err, calls)
			}
		})
	}
}

func TestLookupUserStrictAndCap(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `{} {}`,
		`{"data":[],"data":[]}`, `{"data":[],"DATA":[]}`,
		`{"pageNumber":0,"PAGENUMBER":0}`, `{"pageSize":1000,"PAGESIZE":1000}`,
		`{"totalItems":0,"TOTALITEMS":0}`, `{"totalPages":0,"TOTALPAGES":0}`,
		`{"data":[{"id":"user-1","ID":"user-2"}]}`,
		`{"data":[{"username":"one","USERNAME":"two"}]}`,
		`{"unknown":"` + strings.Repeat("x", 4<<20) + `"}`,
	} {
		t.Run(fmt.Sprint(len(body), body[:min(len(body), 24)]), func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(body)) }))
			user, err := c.lookupUser(context.Background(), "test", userReadOptions{})
			if err == nil || user != nil || calls != 1 {
				t.Fatalf("lookup=%+v, %v; calls=%d", user, err, calls)
			}
		})
	}
}

func TestUserConfirmationPrivacy(t *testing.T) {
	for _, target := range []string{"list", "get", "error"} {
		t.Run(target, func(t *testing.T) {
			calls, captures := 0, 0
			var credentials []string
			opts := userReadOptions{
				Redact: []string{"response-canary"}, SentCredentials: &credentials,
				WithholdMessage: "confirmation failed",
				ClassifyError:   func(int, string) string { return "ConfirmationFailed" },
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if target == "error" {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"code":"response-canary","message":"response-canary"}`))
					return
				}
				if calls == 1 {
					b := userLookupFixture(t)
					if target == "list" {
						b["data"].([]any)[0].(map[string]any)["id"] = "response-canary"
					}
					_ = json.NewEncoder(w).Encode(b)
				} else {
					// Synthetic hostile response echoes the credential this request sent.
					id := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "username": "vngcloud-probe-0000aaaa", "from": "iam"})
				}
			}))
			defer server.Close()
			cfg, cfgErr := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-bearer-canary"), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: server.URL + "/"}), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
			if cfgErr != nil {
				t.Fatal(cfgErr)
			}
			c := New(cfg)
			user, err := c.lookupUser(context.Background(), "vngcloud-probe-0000aaaa", opts)
			if user != nil || err == nil || captures != 0 || strings.Contains(fmt.Sprintf("%+v", err), "response-canary") {
				t.Fatalf("lookup=%+v, %v; captures=%d", user, err, captures)
			}
			if len(credentials) == 0 {
				t.Fatal("missing sent credentials")
			}
			for _, credential := range credentials {
				if credential != "" && strings.Contains(err.Error(), credential) {
					t.Fatal("credential in error")
				}
			}
			wantCalls := 1
			if target == "get" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d", calls)
			}
			if target == "error" {
				var api *vngcloud.APIError
				if !errors.Is(err, vngcloud.ErrPermission) || !errors.As(err, &api) || api.Code != "ConfirmationFailed" || api.Message != "confirmation failed" {
					t.Fatalf("error=%v", err)
				}
			}
		})
	}
}

func TestLookupUserCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		cancel()
		_ = json.NewEncoder(w).Encode(userLookupFixture(t))
	}))
	user, err := c.lookupUser(ctx, "vngcloud-probe-0000aaaa", userReadOptions{})
	if user != nil || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("lookup=%+v, %v; calls=%d", user, err, calls)
	}
}

func TestUserReadPriorCredentialRedaction(t *testing.T) {
	// Synthetic credentials from an earlier request must also be scrubbed.
	credentials := []string{"prior-bearer-canary"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"prior-bearer-canary","message":"prior-bearer-canary"}`))
	}))
	_, err := c.getUser(context.Background(), "iam.CreateUser", "user-1", userReadOptions{SentCredentials: &credentials})
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), credentials[0]) {
		t.Fatalf("error=%v", err)
	}
}
