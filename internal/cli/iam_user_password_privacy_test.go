package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

type iamReadTokens struct{ calls int }

func (s *iamReadTokens) Token(context.Context) (transport.Token, error) {
	s.calls++
	value := "first-read-canary"
	if s.calls > 1 {
		value = "second-read-canary"
	}
	return transport.Token{AccessToken: value, ExpiresAt: time.Unix(1<<40, 0)}, nil
}
func (*iamReadTokens) Invalidate(string) {}

func assertIAMReadErrorPrivate(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, value := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(value, "read-canary") {
			t.Fatal("credential reached error node")
		}
	}
	switch e := err.(type) { //nolint:errorlint // Inspect every direct node and its children.
	case interface{ Unwrap() error }:
		assertIAMReadErrorPrivate(t, e.Unwrap())
	case interface{ Unwrap() []error }:
		for _, child := range e.Unwrap() {
			assertIAMReadErrorPrivate(t, child)
		}
	}
}

func TestIAMPublicUserEchoOutput(t *testing.T) {
	for _, list := range []bool{false, true} {
		for _, field := range []string{"id", "username"} {
			for _, echo := range []string{"direct", "old-after-refresh", "new-after-refresh"} {
				for _, format := range []string{"json", "table", "text"} {
					t.Run(strconv.FormatBool(list)+"/"+field+"/"+echo+"/"+format, func(t *testing.T) {
						withCleanEnv(t)
						withTestOptions(t, vngcloud.WithStaticToken("unused"))
						calls := 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls++
							if echo != "direct" && calls == 1 {
								w.WriteHeader(http.StatusUnauthorized)
								return
							}
							token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
							if echo == "old-after-refresh" {
								token = "first-read-canary"
							}
							row := map[string]string{"id": "user-1", "username": "ordinary"}
							row[field] = "prefix-" + token + "-suffix"
							var body any = row
							if list {
								body = map[string]any{"data": []map[string]string{row}}
							}
							_ = json.NewEncoder(w).Encode(body)
						}))
						defer server.Close()
						cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: new(iamReadTokens)}))
						var stdout, stderr bytes.Buffer
						e := &env{flags: &globalFlags{}, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
						root := newTestRoot(e)
						root.AddCommand(Service(e, "iam", "IAM", func(vngcloud.Config) *iam.Client { return iam.New(cfg) }, iamOps...))
						args := []string{"iam", "get-user", "--user-id", "user-1", "--region", "hcm-3", "--output", format}
						if list {
							args = []string{"iam", "list-users", "--region", "hcm-3", "--output", format}
						}
						root.SetArgs(args)
						err := root.Execute()
						assertIAMReadErrorPrivate(t, err)
						if strings.Contains(stdout.String()+stderr.String(), "read-canary") {
							t.Fatal("credential reached CLI output")
						}
						if err == nil || stdout.Len() != 0 {
							t.Fatal("credential-bearing response rendered")
						}
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
	}
}
