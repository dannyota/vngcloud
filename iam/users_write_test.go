package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestCreateUserFixture(t *testing.T) {
	posts := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["username"] != "vngcloud-probe-0000aaaa" || body["password"] != "synthetic input" || len(body) != 2 {
				t.Error("incorrect create body")
			}
			w.WriteHeader(201)
			testutil.WriteFixture(t, w, "../testdata/iam/create_user.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_user.json")
	}))
	out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "vngcloud-probe-0000aaaa", Password: vngcloud.Secret("synthetic input")})
	// Both create and confirmation decode the sanitized raw fixtures.
	if posts != 1 || out == nil || err != nil || out.CreatedID != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("posts=%d out=%v err=%v", posts, out, err)
	}
}
