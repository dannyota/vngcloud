package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestCreateUserOutcomes(t *testing.T) {
	for _, generated := range []bool{false, true} {
		for _, tc := range []struct {
			name, body         string
			status, confirm    int
			want               error
			created, candidate bool
			gets               int
		}{
			{"confirmed", `{"id":"user-1"}`, 201, 200, nil, true, false, 1},
			{"confirmation-failed", `{"id":"user-1"}`, 201, 404, ErrCreateUnconfirmed, true, false, 1},
			{"empty", ``, 201, 200, ErrUserCreateUncertain, false, true, 2},
			{"missing", `{}`, 201, 200, ErrUserCreateUncertain, false, true, 2},
			{"empty-id", `{"id":""}`, 201, 200, ErrUserCreateUncertain, false, true, 2},
			{"null-id", `{"id":null}`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"non-string", `{"id":17}`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"invalid-id", `{"id":"../x"}`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"null", `null`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"array", `[]`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"whitespace", ` `, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"partial", `{"id":"user-1"`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"duplicate-key", `{"id":"user-1","id":"other"}`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"alias", `{"id":"user-1","ID":"other"}`, 201, 200, ErrUserCreateUncertain, false, false, 0},
			{"duplicate-username", `{"code":"echo","message":"echo"}`, 400, 200, nil, false, false, 0},
			{"unknown", `{}`, 502, 200, ErrUserCreateUncertain, false, false, 0},
		} {
			t.Run(tc.name+"/generated="+strconv.FormatBool(generated), func(t *testing.T) {
				posts, gets := 0, 0
				var submitted string
				c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts++
						var body map[string]string
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						submitted = body["password"]
						if r.URL.Path != "/accounts-api/v1/iam-users" || body["username"] != "new-user" || len(body) != 2 {
							t.Error("bad request")
						}
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(tc.body))
						return
					}
					gets++
					if r.URL.Query().Get("name") != "" {
						if r.URL.Query().Get("name") != "new-user" || r.URL.Query().Get("pageSize") != "1000" || r.URL.Query().Get("pageNumber") != "0" {
							t.Error("bad lookup")
						}
						_, _ = w.Write([]byte(`{"data":[{"id":"user-1","username":"new-user"}],"pageNumber":0,"pageSize":1000,"totalItems":1,"totalPages":1}`))
						return
					}
					w.WriteHeader(tc.confirm)
					_, _ = w.Write([]byte(`{"id":"user-1","username":"new-user","from":"iam"}`))
				})))
				in := &CreateUserInput{Username: "new-user", Password: vngcloud.Secret("supplied synthetic")}
				if generated {
					in.Password = ""
				}
				original := *in
				out, err := c.CreateUser(context.Background(), in)
				if *in != original || posts != 1 || gets != tc.gets {
					t.Fatalf("input/counts: posts=%d gets=%d", posts, gets)
				}
				if tc.status == 400 {
					if out != nil || err == nil {
						t.Fatal("rejection must have nil output")
					}
					return
				}
				if (err != nil) != (tc.want != nil) || tc.want != nil && !errors.Is(err, tc.want) || out == nil {
					t.Fatalf("out=%v err=%v", out, err)
				}
				if (out.CreatedID != "") != tc.created || (out.CandidateID != "") != tc.candidate {
					t.Fatal("ID provenance failed")
				}
				if generated && (out.Password.Reveal() != submitted || !passwordClasses([]byte(submitted))) || !generated && out.Password != "" {
					t.Fatal("credential retention failed")
				}
				if errors.Is(tc.want, ErrCreateUnconfirmed) && !errors.Is(err, vngcloud.ErrNotFound) {
					t.Fatal("confirmation identity lost")
				}
				if errors.Is(tc.want, ErrUserCreateUncertain) && errors.Is(err, ErrCreateUnconfirmed) {
					t.Fatal("incorrect outcome sentinel")
				}
			})
		}
	}
}

func TestCreateUserValidation(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	for _, in := range []*CreateUserInput{nil, {}, {Username: " "}, {Username: "x\n"}, {Username: string([]byte{255})}, {Username: strings.Repeat("x", 4097)}, {Username: "x", Password: vngcloud.Secret("x\n")}, {Username: "x", Password: vngcloud.Secret(string([]byte{255}))}, {Username: "x", Password: vngcloud.Secret(strings.Repeat("x", 4097))}, {Username: "x", Password: vngcloud.Secret("x"), PasswordLength: 8}, {Username: "x", PasswordLength: 7}, {Username: "x", PasswordLength: 51}} {
		out, err := c.CreateUser(context.Background(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("validation: %v", err)
		}
	}
	if calls != 0 {
		t.Fatal("validation made requests")
	}
}

func TestCreateUserNoResendStatuses(t *testing.T) {
	for _, status := range []int{200, 202, 204, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(status)
			})))
			out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
			if err == nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if status >= 400 && status < 500 {
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.StatusCode != status {
					t.Fatal("definite rejection lost")
				}
			} else if out == nil || out.Password == "" || !errors.Is(err, ErrUserCreateUncertain) {
				t.Fatal("uncertain outcome lost")
			}
		})
	}
}

func TestCreateUserCanceledBeforeSend(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, password := range []vngcloud.Secret{"", vngcloud.Secret("synthetic input")} {
		out, err := c.CreateUser(ctx, &CreateUserInput{Username: "new-user", Password: password})
		if out != nil || !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestCreateUserInvalidConfirmation(t *testing.T) {
	for _, body := range []string{`{"id":"other","username":"new-user","from":"iam"}`, `{"id":"user-1","username":"New-user","from":"iam"}`, `{"id":"user-1","username":"new-user","from":"root"}`, `{"id":"user-1","username":"new-user","createdAt":{"$numberLong":"timestamp-canary"}}`, `{"id":"user-1","username":"new-user","root":"bad","from":17}`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(201)
				_, _ = w.Write([]byte(`{"id":"user-1"}`))
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
		if out == nil || out.CreatedID != "user-1" || out.User.ID != "user-1" || out.User.From != "" || !errors.Is(err, ErrCreateUnconfirmed) || strings.Contains(fmt.Sprint(err), "canary") {
			t.Fatalf("confirmation: out=%v err=%v", out, err)
		}
	}
}

func TestCreateUserGeneratedLengthsAndSuppliedShape(t *testing.T) {
	for _, tc := range []struct {
		length   int
		supplied vngcloud.Secret
	}{{8, ""}, {32, ""}, {50, ""}, {0, vngcloud.Secret(" x ")}, {0, vngcloud.Secret("x")}} {
		var submitted string
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				submitted = body["password"]
				w.WriteHeader(201)
				_, _ = w.Write([]byte(`{"id":"USER-1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"user-1","username":"new-user","from":"iam"}`))
		}))
		in := &CreateUserInput{Username: "new-user", Password: tc.supplied, PasswordLength: tc.length}
		original := *in
		out, err := c.CreateUser(context.Background(), in)
		if err != nil || out == nil || *in != original || out.CreatedID != "USER-1" || out.User.ID != "user-1" {
			t.Fatalf("create: %v", err)
		}
		if tc.supplied != "" {
			if submitted != tc.supplied.Reveal() || out.Password != "" {
				t.Fatal("supplied shape changed")
			}
			continue
		}
		if len(submitted) != tc.length || out.Password.Reveal() != submitted {
			t.Fatal("generated length or value changed")
		}
		for _, b := range []byte(submitted) {
			if !strings.ContainsRune(passwordAlphabet, rune(b)) {
				t.Fatal("generator alphabet changed")
			}
		}
		raw, _ := json.Marshal(out) //nolint:gosec // Verify Secret redaction in the public output.
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("created", "output", out, "input", in)
		if strings.Contains(logs.String(), submitted) {
			t.Fatal("generated password leaked in slog")
		}
		for _, formatted := range []string{string(raw), fmt.Sprintf("%v", out), fmt.Sprintf("%+v", out), fmt.Sprintf("%#v", out)} {
			if strings.Contains(formatted, submitted) {
				t.Fatal("generated password leaked")
			}
		}
	}
}

func TestUserWriteDeadlineIdentity(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request after deadline") }))
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	out, err := c.CreateUser(ctx, &CreateUserInput{Username: "new-user", Password: vngcloud.Secret("synthetic supplied")})
	if out != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("create deadline: %v", err)
	}
	deleted, err := c.DeleteUser(ctx, &DeleteUserInput{UserID: "user-1"})
	if deleted != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("delete deadline: %v", err)
	}
}

func TestCreateUserCredentialBearingRequestedName(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"user-1"}`))
	}))
	out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "name-input-canary", Password: vngcloud.Secret("input-canary")})
	if calls != 1 || out == nil || out.CreatedID != "user-1" || !errors.Is(err, ErrCreateUnconfirmed) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if strings.Contains(out.User.Username, "input-canary") {
		t.Fatal("credential in retained name")
	}
	assertUserErrorPrivate(t, err, "input-canary")
}
