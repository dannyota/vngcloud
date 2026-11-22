package containerregistry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCreateUserRequiresNamedSearchRows(t *testing.T) {
	cases := []struct {
		name        string
		row         string
		wantCreates int32
	}{
		{"missing", `{"uuid":"ra-existing"}`, 0},
		{"null", `{"uuid":"ra-existing","name":null}`, 0},
		{"empty", `{"uuid":"ra-existing","name":""}`, 0},
		{"control", `{"uuid":"ra-existing","name":"other\u0000user"}`, 0},
		{"wrong type", `{"uuid":"ra-existing","name":7}`, 0},
		{"named nonmatch", `{"uuid":"ra-existing","name":"other-user"}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var creates atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost:
					creates.Add(1)
					_, _ = io.WriteString(w, `{"secretKey":"issued-once"}`)
				case strings.HasSuffix(r.URL.Path, "/permissions"):
					_, _ = io.WriteString(w, `[{"uuid":"p-1","action":"Pull Images"}]`)
				default:
					_, _ = io.WriteString(w, `{"data":[`+tc.row+`],"totalPage":1,"totalItem":1}`)
				}
			}))
			out, err := c.CreateUser(context.Background(), &CreateUserInput{
				Name: "app-ci", Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"Pull Images"}}},
			})
			if creates.Load() != tc.wantCreates {
				t.Errorf("POST count = %d, want %d", creates.Load(), tc.wantCreates)
			}
			if tc.wantCreates == 0 && (out != nil || err == nil) {
				t.Errorf("incomplete name evidence: output=%v error=%v", out, err)
			}
			if tc.wantCreates == 1 && (out == nil || out.SecretKey.Reveal() != "issued-once" || !errors.Is(err, ErrUserNotFound)) {
				t.Errorf("valid nonmatch blocked create: output=%v error=%v", out, err)
			}
		})
	}
}
