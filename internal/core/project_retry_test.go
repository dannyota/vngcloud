package core_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/project"
)

func TestListProjectsRetriesNonJSONErrorResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "gateway", status: http.StatusServiceUnavailable, body: "<html>unavailable</html>"},
		{name: "unauthorized", status: http.StatusUnauthorized, body: "unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				if tc.status == http.StatusUnauthorized && r.Header.Get("Authorization") != "Bearer second-credential-marker" {
					t.Error("refreshed token not used")
				}
				_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-one","region":"hcm-3"}]}`))
			}))
			defer server.Close()
			credentials := &projectRotatingCredentials{}
			captures := 0
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithCredentialsProvider(credentials), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL}), vngcloud.WithRetry(1, time.Nanosecond), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
			if err != nil {
				t.Fatal(err)
			}
			projects, err := project.New(cfg).ListProjects(context.Background(), nil)
			if err != nil {
				t.Fatalf("calls %d, captures %d, error %v", calls, captures, err)
			}
			wantTokens := 1
			if tc.status == http.StatusUnauthorized {
				wantTokens = 2
			}
			if projects == nil || len(projects.Items) != 1 || calls != 2 || captures != 0 || credentials.n != wantTokens {
				t.Fatalf("calls %d, captures %d, token fetches %d", calls, captures, credentials.n)
			}
		})
	}
}
