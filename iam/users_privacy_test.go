package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestPublicUserCredentialEchoes(t *testing.T) {
	for _, list := range []bool{false, true} {
		for _, field := range []string{"id", "username"} {
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
